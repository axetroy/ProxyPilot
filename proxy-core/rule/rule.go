// Package rule 提供网关智能分流：根据目标 host 决定连接「直连」还是「经节点池代理」。
//
// 规则来源同步自上游域名列表（默认 Loyalsoldier/surge-rules），本地只做白名单校验，
// 绝不执行远程内容。匹配优先级见 Match：本机/局域网 → .cn → 代理名单 → 直连名单 →
// geoip(CN) → 默认动作（白名单走代理 / 黑名单直连）。
package rule

import (
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/axetroy/ProxyPilot/proxy-core/bus"
	"github.com/axetroy/ProxyPilot/proxy-core/config"
	"github.com/axetroy/ProxyPilot/proxy-core/geoip"
)

// Action 表示目标 host 的走向。
type Action string

const (
	ActionProxy  Action = "proxy"
	ActionDirect Action = "direct"
)

// 分流模式。
const (
	ModeWhitelist = "whitelist" // 默认走代理，命中直连名单/大陆才直连
	ModeBlacklist = "blacklist" // 默认直连，命中代理名单才走代理
)

// Manager 维护直连/代理域名规则并决定目标 host 的走向。线程安全。
// 开关/模式/规则源/刷新周期在 cfg 之外持有独立副本（ApplyConfig 同步），
// 避免与 api 层并发读写 config.Config 造成数据竞争。
type Manager struct {
	cfg       *config.Config
	bus       *bus.Bus
	cachePath string

	// httpClient 是规则列表拉取复用的 HTTP 客户端。
	// 每次同步都新建 Client 会反复分配连接池结构并残留空闲 TCP 连接，
	// 规则定期刷新（默认 24h）长期运行会累积；共享一个实例复用连接。
	httpClient *http.Client

	mu              sync.RWMutex
	direct          map[string]struct{}
	proxy           map[string]struct{}
	customDirect    map[string]struct{} // 手动直连名单（匹配优先级最高）
	customProxy     map[string]struct{} // 手动代理名单（匹配优先级最高）
	enabled         bool
	mode            string
	directURLs      string
	proxyURLs       string
	refreshInterval time.Duration
	syncAt          time.Time
	syncErr         string
	syncing         bool
}

// NewManager 创建规则管理器。dbPath 用于推导缓存文件路径（与数据库同目录）。
func NewManager(cfg *config.Config, bus *bus.Bus, dbPath string) *Manager {
	m := &Manager{
		cfg:       cfg,
		bus:       bus,
		cachePath: filepath.Join(filepath.Dir(dbPath), "pac_rules.json"),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		direct: make(map[string]struct{}),
		proxy:  make(map[string]struct{}),
	}
	m.ApplyConfig()
	return m
}

// ApplyConfig 从 cfg 同步开关/模式/规则源/刷新周期/手动名单。
// api 层更新配置（/api/pac-config）后必须调用，保证匹配与同步读到最新值。
func (m *Manager) ApplyConfig() {
	m.mu.Lock()
	defer m.mu.Unlock()
	pf := m.cfg.PACSnapshot()
	m.enabled = pf.Enabled
	m.mode = pf.Mode
	m.directURLs = pf.DirectURLs
	m.proxyURLs = pf.ProxyURLs
	m.refreshInterval = pf.RefreshInterval
	m.customDirect = parseDomainList(pf.CustomDirect)
	m.customProxy = parseDomainList(pf.CustomProxy)
}

// parseDomainList 解析逗号分隔的域名名单为集合（规范化：小写、去空白）。
func parseDomainList(list string) map[string]struct{} {
	s := make(map[string]struct{})
	for _, d := range strings.Split(list, ",") {
		d = normalizeHost(d)
		if d == "" {
			continue
		}
		s[d] = struct{}{}
	}
	return s
}

// Enabled 返回分流开关状态。关闭时网关全部走代理（Shunt 恒返回 false）。
func (m *Manager) Enabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.enabled
}

// Match 决定目标 host 的走向。host 是纯域名或纯 IP（不带端口）。
func (m *Manager) Match(host string) Action {
	host = normalizeHost(host)
	if host == "" {
		return ActionProxy
	}
	if ip := net.ParseIP(host); ip != nil {
		if isLocalIP(ip) {
			return ActionDirect
		}
		// 纯 IP 目标：客户端未给域名。白名单按离线 geoip 判定是否大陆；
		// 黑名单默认直连（纯 IP 无法命中域名代理名单，直接放行更符合「默认直连」语义）。
		m.mu.RLock()
		mode := m.mode
		m.mu.RUnlock()
		if mode == ModeBlacklist {
			return ActionDirect
		}
		if loc, ok := geoip.Lookup(ip.String()); ok && loc.Country == "中国" {
			return ActionDirect
		}
		return ActionProxy
	}
	if isLocalDomain(host) {
		return ActionDirect
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	// 手动规则优先于同步名单与 .cn/geoip：用户明确指定的域名（无论模式）
	// 以手动代理 > 手动直连的顺序裁决，保证「强制走代理/强制直连」的意图生效。
	if domainHit(m.customProxy, host) {
		return ActionProxy
	}
	if domainHit(m.customDirect, host) {
		return ActionDirect
	}
	if strings.HasSuffix(host, ".cn") {
		return ActionDirect
	}

	switch m.mode {
	case ModeBlacklist:
		if domainHit(m.proxy, host) {
			return ActionProxy
		}
		return ActionDirect
	default: // ModeWhitelist
		// 代理名单优先于直连名单：保证「该走代理的绝不被误判直连」
		// （直连本应代理的域名可能因 DNS 解析异常解析到假 IP；反之只是多耗节点流量）。
		if domainHit(m.proxy, host) {
			return ActionProxy
		}
		if domainHit(m.direct, host) {
			return ActionDirect
		}
		return ActionProxy
	}
}

// domainHit 后缀匹配：host 自身或其任一父域命中集合即命中。
// 规则列表是裸域名（如 baidu.com），真实流量几乎都是子域名（如 app.baidu.com），
// 因此按完整 host → 逐级去掉最左标签 → 只剩单标签，逐级查询。
func domainHit(set map[string]struct{}, host string) bool {
	for {
		if _, ok := set[host]; ok {
			return true
		}
		dot := strings.IndexByte(host, '.')
		if dot < 0 {
			return false
		}
		host = host[dot+1:]
	}
}

// Shunt 返回适配网关的直连判断函数：true 表示直连、false 走节点池。
// 分流开关关闭时恒返回 false（全部走代理）。
func (m *Manager) Shunt() func(host string) bool {
	return func(host string) bool {
		if !m.Enabled() {
			return false
		}
		return m.Match(host) == ActionDirect
	}
}

// Stats 返回规则统计与最近同步状态（仅同步名单）。
func (m *Manager) Stats() (directCount, proxyCount int, syncAt time.Time, syncErr string, syncing bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.direct), len(m.proxy), m.syncAt, m.syncErr, m.syncing
}

// CustomStats 返回手动规则条数（直连/代理）。
func (m *Manager) CustomStats() (directCount, proxyCount int) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.customDirect), len(m.customProxy)
}

// normalizeHost 规范化 host：小写、去首尾空白、去末尾点。
func normalizeHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	return strings.TrimSuffix(host, ".")
}

// isLocalIP 判断 IP 是否属于本机/内网/链路本地。
func isLocalIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsUnspecified() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}

// isLocalDomain 判断域名是否属于本机/内网命名（.local 后缀或无点的单标签主机名）。
func isLocalDomain(host string) bool {
	return strings.HasSuffix(host, ".local") || !strings.Contains(host, ".")
}