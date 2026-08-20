package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/axetroy/ProxyPilot/proxy-core/storage"
)

// Version 通过 goreleaser 的 ldflags 注入（如 -X ...config.Version={{.Version}}），
// 本地开发时使用默认值。
var Version = "0.1.18"

// 持久化配置项的 key。
// APIBind / DBPath / SessionToken 属于启动期固定配置，不支持通过界面修改。
const (
	KeyProxyPort         = "proxy_port"
	KeyCheckTarget       = "check_target"
	KeyCheckSafetyTgt = "check_safety_target"
	KeyCheckTimeout      = "check_timeout"
	KeyCheckConcurr      = "check_concurrency"
	KeyRefreshPeriod     = "refresh_interval"
	// 订阅导出相关：enabled/listen 通过前端设置，token 由专门的订阅接口管理
	// （不进 Settings() 列表，避免与通用设置表单混在一起）。
	KeySubEnabled = "subscription_enabled"
	KeySubListen  = "subscription_listen"
	KeySubHost    = "subscription_host"
	KeySubToken   = "subscription_token"
	// 固定出口节点 ID（用户指定使用哪个代理）。单独管理，
	// 不进 Settings() 列表（避免与通用设置表单混在一起）。
	KeyPinnedProxy = "pinned_proxy_id"
	// 出口策略（fixed / best / random / weighted / round-robin / chain / auto-chain）。
	// 与固定出口同属「出口路由」管理，不进 Settings() 通用表单。
	KeyEgressStrategy = "egress_strategy"
	// 自动链路（auto-chain）参数：层数与每层选择策略。
	// 与出口策略同属「出口路由」管理，不进 Settings() 通用表单。
	KeyChainHops      = "chain_hops"
	KeyChainSelection = "chain_selection"
	// 链路自动健康检测周期：对启用的链路定时探测，连续失败达阈值自动停用。
	KeyChainCheckInterval = "chain_check_interval"
	// 检测历史保留天数：手动瘦身数据库时，早于该天数的检测历史会被清理。
	KeyHistoryRetention = "history_retention_days"
	// 智能分流相关：开关/模式/规则源/刷新周期。不进 Settings() 通用表单
	// （避免与通用设置表单混在一起），由 /api/pac-config 专门接口管理。
	KeyPACEnabled    = "pac_enabled"
	KeyPACMode       = "pac_mode"
	KeyPACDirectURLs = "pac_direct_urls"
	KeyPACProxyURLs  = "pac_proxy_urls"
	KeyPACRefresh    = "pac_refresh_interval"
	// 手动规则名单（用户自定义域名，逗号分隔）：匹配优先级高于同步名单，
	// 与规则源一同由 /api/pac-config 管理。
	KeyPACCustomDirect = "pac_custom_direct"
	KeyPACCustomProxy  = "pac_custom_proxy"
)

// 智能分流默认规则源：Loyalsoldier/surge-rules（每日自动更新）。
// 每个 key 对应「主源 + 备用镜像」，按顺序尝试直到成功。
const (
	DefaultPACDirectURLs = "https://raw.githubusercontent.com/Loyalsoldier/surge-rules/release/direct.txt,https://cdn.jsdelivr.net/gh/Loyalsoldier/surge-rules@release/direct.txt"
	DefaultPACProxyURLs  = "https://raw.githubusercontent.com/Loyalsoldier/surge-rules/release/gfw.txt,https://cdn.jsdelivr.net/gh/Loyalsoldier/surge-rules@release/gfw.txt"
)

// SettingDef 描述一个可在前端配置的项。
type SettingDef struct {
	Key     string `json:"key"`
	Default string `json:"default"`
	Desc    string `json:"desc"`
	// Validate 校验用户输入，返回错误信息（nil 表示合法）。
	Validate func(v string) error `json:"-"`
}

// Settings 返回所有可在前端配置的项定义。
func Settings() []SettingDef {
	return []SettingDef{
		{Key: KeyProxyPort, Default: "7892", Desc: "代理监听端口（HTTP 与 SOCKS5 共用，仅本机，被占用自动顺延）", Validate: validatePort},
		{Key: KeyCheckTarget, Default: "https://www.apple.com/library/test/success.html", Desc: "节点检测目标 URL", Validate: validateURL},
		{Key: KeyCheckSafetyTgt, Default: "https://httpbin.org/anything", Desc: "连接安全检测回显端点（需返回 origin 与 headers 字段）", Validate: validateURL},
		{Key: KeyCheckTimeout, Default: "10s", Desc: "单节点检测超时（如 5s、500ms）", Validate: validateDuration},
		{Key: KeyCheckConcurr, Default: "32", Desc: "并发检测节点数", Validate: validatePositiveInt},
		{Key: KeyRefreshPeriod, Default: "15m", Desc: "代理池自动检测周期（如 30m、1h）", Validate: validateDuration},
		{Key: KeySubEnabled, Default: "1", Desc: "订阅导出开关（0 关闭 / 1 开启）", Validate: validateBool},
		{Key: KeySubListen, Default: "127.0.0.1:17891", Desc: "订阅服务监听地址（如需局域网设备订阅改为 0.0.0.0:17891）", Validate: validateHostPort},
		{Key: KeySubHost, Default: "", Desc: "对外展示的订阅 IP（监听为 0.0.0.0 时用于生成订阅 URL）", Validate: validateIPOrEmpty},
		{Key: KeyHistoryRetention, Default: "7", Desc: "检测历史保留天数（瘦身数据库时清理更早的记录）", Validate: validatePositiveInt},
		{Key: KeyChainCheckInterval, Default: "5m", Desc: "链路自动健康检测周期（如 5m、15m，最小 1 分钟，连续失败 2 次自动停用）", Validate: validateDuration},
		{Key: KeyPACEnabled, Default: "1", Desc: "智能分流开关（0 关闭全部走代理 / 1 开启按规则分流）", Validate: validateBool},
		{Key: KeyPACMode, Default: "whitelist", Desc: "智能分流模式（whitelist 默认走代理 / blacklist 默认直连）", Validate: validatePACMode},
		{Key: KeyPACDirectURLs, Default: DefaultPACDirectURLs, Desc: "直连规则列表 URL（逗号分隔，按序尝试）", Validate: validateURLList},
		{Key: KeyPACProxyURLs, Default: DefaultPACProxyURLs, Desc: "代理规则列表 URL（逗号分隔，按序尝试）", Validate: validateURLList},
		{Key: KeyPACRefresh, Default: "24h", Desc: "分流规则自动刷新周期（如 12h、24h，最小 1 小时）", Validate: validatePACRefresh},
		{Key: KeyPACCustomDirect, Default: "", Desc: "手动直连名单（域名，逗号分隔，优先级最高）", Validate: validateDomainList},
		{Key: KeyPACCustomProxy, Default: "", Desc: "手动代理名单（域名，逗号分隔，优先级最高）", Validate: validateDomainList},
	}
}

// validatePort 校验纯端口号（1-65535）。
func validatePort(v string) error {
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("端口必须是 1-65535 之间的整数")
	}
	return nil
}

func validateURL(v string) error {
	if !strings.HasPrefix(v, "http://") && !strings.HasPrefix(v, "https://") {
		return fmt.Errorf("必须是 http:// 或 https:// 开头的 URL")
	}
	return nil
}

func validateDuration(v string) error {
	if _, err := time.ParseDuration(v); err != nil {
		return fmt.Errorf("时长格式不合法，如 5s、10s、15m")
	}
	return nil
}

func validatePositiveInt(v string) error {
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fmt.Errorf("必须是正整数")
	}
	return nil
}

// validateBool 校验 0/1 布尔字符串。
func validateBool(v string) error {
	if v != "0" && v != "1" {
		return fmt.Errorf("必须是 0 或 1")
	}
	return nil
}

// validateHostPort 校验 host:port 监听地址。
func validateHostPort(v string) error {
	_, port, err := net.SplitHostPort(v)
	if err != nil {
		return fmt.Errorf("必须是 host:port 格式，如 127.0.0.1:17891")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("端口必须是 1-65535 之间的整数")
	}
	return nil
}

// validatePACMode 校验智能分流模式。
func validatePACMode(v string) error {
	if v != "whitelist" && v != "blacklist" {
		return fmt.Errorf("必须是 whitelist 或 blacklist")
	}
	return nil
}

// validateURLList 校验逗号分隔的 http(s) URL 列表（允许空，空表示不拉取该项规则）。
func validateURLList(v string) error {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	for _, u := range strings.Split(v, ",") {
		u = strings.TrimSpace(u)
		if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			return fmt.Errorf("每个 URL 必须是 http:// 或 https:// 开头，逗号分隔")
		}
	}
	return nil
}

// validatePACRefresh 校验分流规则刷新周期（最小 1 小时）。
func validatePACRefresh(v string) error {
	d, err := time.ParseDuration(v)
	if err != nil {
		return fmt.Errorf("周期格式不合法，如 12h、24h")
	}
	if d < time.Hour {
		return fmt.Errorf("周期不能小于 1 小时")
	}
	return nil
}

// validateDomainList 校验逗号分隔的手动规则域名名单（允许空）。
// 域名规则与 rule 包一致：小写字母/数字/-/.，≤255，不以点或连字符开头/结尾。
func validateDomainList(v string) error {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	for _, d := range strings.Split(v, ",") {
		d = strings.ToLower(strings.TrimSpace(d))
		if !validDomain(d) {
			return fmt.Errorf("含非法域名: %s（仅限小写字母/数字/-/.，≤255）", d)
		}
	}
	return nil
}

// validDomain 域名白名单校验（与 rule 包保持一致，避免循环依赖）。
func validDomain(d string) bool {
	if len(d) == 0 || len(d) > 255 {
		return false
	}
	if strings.HasPrefix(d, ".") || strings.HasSuffix(d, ".") {
		return false
	}
	if strings.HasPrefix(d, "-") || strings.HasSuffix(d, "-") {
		return false
	}
	for _, r := range d {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '.' {
			continue
		}
		return false
	}
	return true
}

// validateIPOrEmpty 校验 IP 地址（空串表示未选择，允许）。
func validateIPOrEmpty(v string) error {
	if v == "" {
		return nil
	}
	if net.ParseIP(v) == nil {
		return fmt.Errorf("必须是合法的 IP 地址")
	}
	return nil
}

type Config struct {
	mu sync.RWMutex // 保护运行时热更新的可变字段（PAC* / Chain*），见 PACFields / ChainParams

	APIBind              string
	DBPath               string
	ProxyHost            string // 代理监听 host，固定仅本机，不允许修改
	ProxyPort            int    // 代理监听端口（HTTP 与 SOCKS5 共用）
	SessionToken         string
	CheckTarget          string
	CheckSafetyTarget string
	CheckTimeout         time.Duration
	CheckConcurrency     int
	RefreshInterval      time.Duration
	SubEnabled           bool   // 订阅导出开关
	SubListen            string // 订阅服务监听地址（默认仅本机，对外暴露需显式配置 0.0.0.0）
	SubHost              string // 对外展示的订阅 IP（监听为通配地址时用于生成订阅 URL，空则回退 127.0.0.1）
	SubToken             string // 订阅密钥（独立于 SessionToken，随订阅 URL 提供给外部客户端）
	HistoryRetentionDays int    // 检测历史保留天数（瘦身数据库时清理更早的记录）
	PACEnabled           bool   // 智能分流开关（关闭时全部流量走代理）
	PACMode              string // 智能分流模式（whitelist / blacklist）
	PACDirectURLs        string // 直连规则列表 URL（逗号分隔，按序尝试）
	PACProxyURLs         string // 代理规则列表 URL（逗号分隔，按序尝试）
	PACRefreshInterval   time.Duration // 分流规则自动刷新周期
	PACCustomDirect      string // 手动直连名单（域名，逗号分隔，匹配优先级最高）
	PACCustomProxy       string // 手动代理名单（域名，逗号分隔，匹配优先级最高）
	// 自动链路（auto-chain）策略参数：层数与每层选择策略。
	ChainHops      int    // 自动链路层数（默认 2）
	ChainSelection string // 每层选择策略（weighted / random / best，默认 weighted）
	// 链路自动健康检测周期：对启用的链路定时探测，连续失败达阈值自动停用。
	ChainCheckInterval time.Duration
}

// PACFields 是智能分流（PAC）相关可变字段的快照。
// api 层通过 MutatePAC 在锁内整体读改写，避免并发配置更新时的数据竞争。
type PACFields struct {
	Enabled         bool
	Mode            string
	DirectURLs      string
	ProxyURLs       string
	RefreshInterval time.Duration
	CustomDirect    string
	CustomProxy     string
}

// PACSnapshot 返回当前 PAC 相关字段的副本（并发安全）。
func (c *Config) PACSnapshot() PACFields {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return PACFields{
		Enabled:         c.PACEnabled,
		Mode:            c.PACMode,
		DirectURLs:      c.PACDirectURLs,
		ProxyURLs:       c.PACProxyURLs,
		RefreshInterval: c.PACRefreshInterval,
		CustomDirect:    c.PACCustomDirect,
		CustomProxy:     c.PACCustomProxy,
	}
}

// MutatePAC 在锁内读改写 PAC 相关字段（fn 可修改快照字段，随后写回）。
// 用于 API 配置更新，保证并发读取方不会读到中间状态。
func (c *Config) MutatePAC(fn func(f *PACFields)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	f := PACFields{
		Enabled:         c.PACEnabled,
		Mode:            c.PACMode,
		DirectURLs:      c.PACDirectURLs,
		ProxyURLs:       c.PACProxyURLs,
		RefreshInterval: c.PACRefreshInterval,
		CustomDirect:    c.PACCustomDirect,
		CustomProxy:     c.PACCustomProxy,
	}
	fn(&f)
	c.PACEnabled = f.Enabled
	c.PACMode = f.Mode
	c.PACDirectURLs = f.DirectURLs
	c.PACProxyURLs = f.ProxyURLs
	c.PACRefreshInterval = f.RefreshInterval
	c.PACCustomDirect = f.CustomDirect
	c.PACCustomProxy = f.CustomProxy
}

// SubFields 是订阅导出相关可变字段的快照。
// api 层通过 MutateSub 在锁内整体读改写，避免订阅服务（独立路由）
// 与配置更新并发时读到中间状态。
type SubFields struct {
	Enabled bool
	Listen  string
	Host    string
	Token   string
}

// SubSnapshot 返回当前订阅导出相关字段的副本（并发安全）。
func (c *Config) SubSnapshot() SubFields {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return SubFields{
		Enabled: c.SubEnabled,
		Listen:  c.SubListen,
		Host:    c.SubHost,
		Token:   c.SubToken,
	}
}

// MutateSub 在锁内读改写订阅导出相关字段（fn 可修改快照字段，随后写回）。
func (c *Config) MutateSub(fn func(f *SubFields)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	f := SubFields{
		Enabled: c.SubEnabled,
		Listen:  c.SubListen,
		Host:    c.SubHost,
		Token:   c.SubToken,
	}
	fn(&f)
	c.SubEnabled = f.Enabled
	c.SubListen = f.Listen
	c.SubHost = f.Host
	c.SubToken = f.Token
}

// ChainParams 返回自动链路参数（并发安全）。
func (c *Config) ChainParams() (int, string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ChainHops, c.ChainSelection
}

// SetChainParams 更新自动链路参数（并发安全）。
func (c *Config) SetChainParams(hops int, selection string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ChainHops = hops
	c.ChainSelection = selection
}

// RuntimeFields 是运行期可热更新的通用设置字段快照（代理端口、检测参数、
// 历史保留天数、链路健康检测周期等）。与 PAC / Sub / Chain 字段一样，
// 统一通过锁保护，避免配置热更新与后台协程并发读取时的数据竞争。
type RuntimeFields struct {
	ProxyPort            int
	CheckTarget          string
	CheckSafetyTarget    string
	CheckTimeout         time.Duration
	CheckConcurrency     int
	RefreshInterval      time.Duration
	HistoryRetentionDays int
	ChainCheckInterval   time.Duration
}

// RuntimeSnapshot 返回运行期热更新字段的副本（并发安全）。
func (c *Config) RuntimeSnapshot() RuntimeFields {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return RuntimeFields{
		ProxyPort:            c.ProxyPort,
		CheckTarget:          c.CheckTarget,
		CheckSafetyTarget:    c.CheckSafetyTarget,
		CheckTimeout:         c.CheckTimeout,
		CheckConcurrency:     c.CheckConcurrency,
		RefreshInterval:      c.RefreshInterval,
		HistoryRetentionDays: c.HistoryRetentionDays,
		ChainCheckInterval:   c.ChainCheckInterval,
	}
}

// MutateRuntime 在锁内读改写运行期热更新字段（fn 可修改快照字段，随后写回）。
func (c *Config) MutateRuntime(fn func(f *RuntimeFields)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	f := RuntimeFields{
		ProxyPort:            c.ProxyPort,
		CheckTarget:          c.CheckTarget,
		CheckSafetyTarget:    c.CheckSafetyTarget,
		CheckTimeout:         c.CheckTimeout,
		CheckConcurrency:     c.CheckConcurrency,
		RefreshInterval:      c.RefreshInterval,
		HistoryRetentionDays: c.HistoryRetentionDays,
		ChainCheckInterval:   c.ChainCheckInterval,
	}
	fn(&f)
	c.ProxyPort = f.ProxyPort
	c.CheckTarget = f.CheckTarget
	c.CheckSafetyTarget = f.CheckSafetyTarget
	c.CheckTimeout = f.CheckTimeout
	c.CheckConcurrency = f.CheckConcurrency
	c.RefreshInterval = f.RefreshInterval
	c.HistoryRetentionDays = f.HistoryRetentionDays
	c.ChainCheckInterval = f.ChainCheckInterval
}

func New() *Config {
	c := &Config{
		APIBind:              "127.0.0.1:17890",
		DBPath:               "proxypilot.db",
		ProxyHost:            "127.0.0.1",
		ProxyPort:            7892,
		CheckTarget:          "https://www.apple.com/library/test/success.html",
		CheckSafetyTarget: "https://httpbin.org/anything",
		CheckTimeout:         10 * time.Second,
		CheckConcurrency:     32,
		RefreshInterval:      15 * time.Minute,
		SubEnabled:           true,
		SubListen:            "127.0.0.1:17891",
		SubToken:             generatedSessionToken(),
		HistoryRetentionDays: 7,
		PACEnabled:           true,
		PACMode:              "whitelist",
		PACDirectURLs:        DefaultPACDirectURLs,
		PACProxyURLs:         DefaultPACProxyURLs,
		PACRefreshInterval:   24 * time.Hour,
		ChainHops:            2,
		ChainSelection:       "weighted",
		ChainCheckInterval:   5 * time.Minute,
	}
	c.SessionToken = generatedSessionToken()
	c.ApplyEnv()
	return c
}

// SubscriptionURL 返回订阅 URL（供 UI 展示与复制）。
// 用户配置了具体监听 IP 时原样拼接；监听为 0.0.0.0/:: 等通配地址时，
// 拼接用户在前端下拉选中的局域网 IP（SubHost），未选择时回退 127.0.0.1。
func (c *Config) SubscriptionURL() string {
	f := c.SubSnapshot()
	host, port, err := net.SplitHostPort(f.Listen)
	if err != nil {
		host, port = "127.0.0.1", "17891"
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = f.Host
		if host == "" {
			host = "127.0.0.1"
		}
	}
	return "http://" + net.JoinHostPort(host, port) + "/sub/" + f.Token
}

// LANIPs 返回本机所有非回环的 IPv4 局域网地址（供前端下拉选择）。
func LANIPs() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var ips []string
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
			ips = append(ips, ipnet.IP.String())
		}
	}
	return ips
}

// ProxyAddr 返回代理网关的完整监听地址 host:port（并发安全）。
func (c *Config) ProxyAddr() string {
	f := c.RuntimeSnapshot()
	return net.JoinHostPort(c.ProxyHost, strconv.Itoa(f.ProxyPort))
}

// TargetHostPort 从检测目标（URL 或裸 host:port）解析出 host:port。
// URL 不带端口时按 scheme 推断默认端口（https=443，其他=80）。
// 供节点检测与链路健康检测共用。
func TargetHostPort(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("检测目标为空")
	}
	// 带 scheme 的 URL：按 URL 解析提取 host:port。
	// 注意不能先裸调 net.SplitHostPort：对 "https://example.com/path" 这种
	// 只有一个冒号的字符串，SplitHostPort 会把 https 当 host、//… 当 port 返回 nil error，
	// 导致整个 URL 被误当成 target 传入握手。
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" {
			return "", fmt.Errorf("无效的检测目标 %q", raw)
		}
		port := u.Port()
		if port == "" {
			if u.Scheme == "https" {
				port = "443"
			} else {
				port = "80"
			}
		}
		return net.JoinHostPort(u.Hostname(), port), nil
	}
	// 裸 host:port：校验端口为数字，避免非 host:port 形式被误判为合法目标。
	host, port, err := net.SplitHostPort(raw)
	if err != nil {
		return "", fmt.Errorf("无效的检测目标 %q", raw)
	}
	if _, err := strconv.Atoi(port); err != nil {
		return "", fmt.Errorf("无效的检测目标 %q：端口 %q 不是数字", raw, port)
	}
	return net.JoinHostPort(host, port), nil
}

func (c *Config) ApplyEnv() {
	if v := os.Getenv("PROXYPILOT_API_BIND"); v != "" {
		c.APIBind = v
	}
	if v := os.Getenv("PROXYPILOT_DB_PATH"); v != "" {
		c.DBPath = v
	}
	if v := os.Getenv("PROXYPILOT_PROXY_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 65535 {
			c.ProxyPort = n
		}
	}
	if v := os.Getenv("PROXYPILOT_TOKEN"); v != "" {
		c.SessionToken = v
	}
	if v := os.Getenv("PROXYPILOT_CHECK_TARGET"); v != "" {
		c.CheckTarget = v
	}
	if v := os.Getenv("PROXYPILOT_CHECK_SAFETY_TARGET"); v != "" {
		c.CheckSafetyTarget = v
	}
	if v := os.Getenv("PROXYPILOT_CHECK_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.CheckTimeout = d
		}
	}
	if v := os.Getenv("PROXYPILOT_CHECK_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.CheckConcurrency = n
		}
	}
	if v := os.Getenv("PROXYPILOT_REFRESH_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.RefreshInterval = d
		}
	}
	if v := os.Getenv("PROXYPILOT_SUB_ENABLED"); v != "" {
		c.SubEnabled = v == "1"
	}
	if v := os.Getenv("PROXYPILOT_SUB_LISTEN"); v != "" {
		c.SubListen = v
	}
	if v := os.Getenv("PROXYPILOT_SUB_HOST"); v != "" {
		c.SubHost = v
	}
	if v := os.Getenv("PROXYPILOT_SUB_TOKEN"); v != "" {
		c.SubToken = v
	}
	if v := os.Getenv("PROXYPILOT_HISTORY_RETENTION_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.HistoryRetentionDays = n
		}
	}
	if v := os.Getenv("PROXYPILOT_PAC_ENABLED"); v != "" {
		c.PACEnabled = v == "1"
	}
	if v := os.Getenv("PROXYPILOT_PAC_MODE"); v != "" {
		c.PACMode = v
	}
	if v := os.Getenv("PROXYPILOT_PAC_DIRECT_URLS"); v != "" {
		c.PACDirectURLs = v
	}
	if v := os.Getenv("PROXYPILOT_PAC_PROXY_URLS"); v != "" {
		c.PACProxyURLs = v
	}
	if v := os.Getenv("PROXYPILOT_PAC_REFRESH_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.PACRefreshInterval = d
		}
	}
	if v := os.Getenv("PROXYPILOT_CHAIN_CHECK_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.ChainCheckInterval = d
		}
	}
}

func generatedSessionToken() string {
	return NewToken()
}

// NewToken 生成 32 位 hex 随机密钥（用于 session token 与订阅密钥重置）。
func NewToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "fallback-" + strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b)
}

// settingKey 返回持久化配置项 key 对应的 Config 字段指针，
// 以及该 key 是否属于可配置项。
func (c *Config) settingKey(key string) (func(string), bool) {
	switch key {
	case KeyProxyPort:
		return func(v string) {
			c.MutateRuntime(func(f *RuntimeFields) {
				if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 65535 {
					f.ProxyPort = n
				}
			})
		}, true
	case KeyCheckTarget:
		return func(v string) {
			c.MutateRuntime(func(f *RuntimeFields) { f.CheckTarget = v })
		}, true
	case KeyCheckSafetyTgt:
		return func(v string) {
			c.MutateRuntime(func(f *RuntimeFields) { f.CheckSafetyTarget = v })
		}, true
	case KeyCheckTimeout:
		return func(v string) {
			c.MutateRuntime(func(f *RuntimeFields) {
				if d, err := time.ParseDuration(v); err == nil {
					f.CheckTimeout = d
				}
			})
		}, true
	case KeyCheckConcurr:
		return func(v string) {
			c.MutateRuntime(func(f *RuntimeFields) {
				if n, err := strconv.Atoi(v); err == nil && n > 0 {
					f.CheckConcurrency = n
				}
			})
		}, true
	case KeyRefreshPeriod:
		return func(v string) {
			c.MutateRuntime(func(f *RuntimeFields) {
				if d, err := time.ParseDuration(v); err == nil {
					f.RefreshInterval = d
				}
			})
		}, true
	case KeySubEnabled:
		return func(v string) {
			c.MutateSub(func(f *SubFields) { f.Enabled = v == "1" })
		}, true
	case KeySubListen:
		return func(v string) {
			c.MutateSub(func(f *SubFields) { f.Listen = v })
		}, true
	case KeySubHost:
		return func(v string) {
			c.MutateSub(func(f *SubFields) { f.Host = v })
		}, true
	case KeyHistoryRetention:
		return func(v string) {
			c.MutateRuntime(func(f *RuntimeFields) {
				if n, err := strconv.Atoi(v); err == nil && n > 0 {
					f.HistoryRetentionDays = n
				}
			})
		}, true
	case KeyPACEnabled:
		return func(v string) { c.MutatePAC(func(f *PACFields) { f.Enabled = v == "1" }) }, true
	case KeyPACMode:
		return func(v string) { c.MutatePAC(func(f *PACFields) { f.Mode = v }) }, true
	case KeyPACDirectURLs:
		return func(v string) { c.MutatePAC(func(f *PACFields) { f.DirectURLs = v }) }, true
	case KeyPACProxyURLs:
		return func(v string) { c.MutatePAC(func(f *PACFields) { f.ProxyURLs = v }) }, true
	case KeyPACRefresh:
		return func(v string) {
			c.MutatePAC(func(f *PACFields) {
				if d, err := time.ParseDuration(v); err == nil {
					f.RefreshInterval = d
				}
			})
		}, true
	case KeyPACCustomDirect:
		return func(v string) { c.MutatePAC(func(f *PACFields) { f.CustomDirect = v }) }, true
	case KeyPACCustomProxy:
		return func(v string) { c.MutatePAC(func(f *PACFields) { f.CustomProxy = v }) }, true
	case KeyChainHops:
		return func(v string) {
			if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 8 {
				_, sel := c.ChainParams()
				c.SetChainParams(n, sel)
			}
		}, true
	case KeyChainSelection:
		return func(v string) {
			switch v {
			case "weighted", "random", "best":
				hops, _ := c.ChainParams()
				c.SetChainParams(hops, v)
			}
		}, true
	case KeyChainCheckInterval:
		return func(v string) {
			c.MutateRuntime(func(f *RuntimeFields) {
				if d, err := time.ParseDuration(v); err == nil {
					if d < time.Minute {
						d = time.Minute
					}
					f.ChainCheckInterval = d
				}
			})
		}, true
	default:
		return nil, false
	}
}

// LoadOverrides 从持久化存储中加载用户在前端修改过的配置，覆盖当前值。
// 调用时机：main 启动时、构造各组件之前。
func (c *Config) LoadOverrides(st *storage.Store) {
	for _, def := range Settings() {
		v, err := st.GetSetting(def.Key)
		if err != nil || v == "" {
			continue
		}
		if def.Validate != nil {
			if err := def.Validate(v); err != nil {
				continue // 跳过持久化中的非法值
			}
		}
		if apply, ok := c.settingKey(def.Key); ok {
			apply(v)
		}
	}
	// 订阅密钥独立加载：首次启动生成并持久化，之后保持一致。
	if v, err := st.GetSetting(KeySubToken); err == nil && v != "" {
		c.SubToken = v
	} else {
		_ = st.SetSetting(KeySubToken, c.SubToken)
	}
	// 自动链路参数独立加载：不进 Settings() 通用表单（由出口路由管理），
	// 合法性由各 settingKey 应用逻辑保证（非法值自动忽略，保持默认）。
	for _, k := range []string{KeyChainHops, KeyChainSelection} {
		if v, err := st.GetSetting(k); err == nil && v != "" {
			if apply, ok := c.settingKey(k); ok {
				apply(v)
			}
		}
	}
	// 兼容旧版配置 key：check_anonymity_target → check_safety_target。
	// 老库若只存有旧 key，迁移到新 key 后删除旧记录。
	const legacySafetyKey = "check_anonymity_target"
	if v, err := st.GetSetting(legacySafetyKey); err == nil && v != "" {
		if cur, err := st.GetSetting(KeyCheckSafetyTgt); err != nil || cur == "" {
			_ = st.SetSetting(KeyCheckSafetyTgt, v)
			c.CheckSafetyTarget = v
		}
		_ = st.DeleteSetting(legacySafetyKey)
	}
}

// SettingValue 返回指定 key 的当前值（作为字符串）。
func (c *Config) SettingValue(key string) (string, bool) {
	switch key {
	case KeyProxyPort:
		return strconv.Itoa(c.RuntimeSnapshot().ProxyPort), true
	case KeyCheckTarget:
		return c.RuntimeSnapshot().CheckTarget, true
	case KeyCheckSafetyTgt:
		return c.RuntimeSnapshot().CheckSafetyTarget, true
	case KeyCheckTimeout:
		return c.RuntimeSnapshot().CheckTimeout.String(), true
	case KeyCheckConcurr:
		return strconv.Itoa(c.RuntimeSnapshot().CheckConcurrency), true
	case KeyRefreshPeriod:
		return c.RuntimeSnapshot().RefreshInterval.String(), true
	case KeySubEnabled:
		if c.SubSnapshot().Enabled {
			return "1", true
		}
		return "0", true
	case KeySubListen:
		return c.SubSnapshot().Listen, true
	case KeySubHost:
		return c.SubSnapshot().Host, true
	case KeyHistoryRetention:
		return strconv.Itoa(c.RuntimeSnapshot().HistoryRetentionDays), true
	case KeyPACEnabled:
		if c.PACSnapshot().Enabled {
			return "1", true
		}
		return "0", true
	case KeyPACMode:
		return c.PACSnapshot().Mode, true
	case KeyPACDirectURLs:
		return c.PACSnapshot().DirectURLs, true
	case KeyPACProxyURLs:
		return c.PACSnapshot().ProxyURLs, true
	case KeyPACRefresh:
		return c.PACSnapshot().RefreshInterval.String(), true
	case KeyChainHops:
		hops, _ := c.ChainParams()
		return strconv.Itoa(hops), true
	case KeyChainSelection:
		_, sel := c.ChainParams()
		return sel, true
	default:
		return "", false
	}
}

// ValidateSetting 只校验配置值是否合法，不修改 Config。
// 用于 API 批量更新前的整体校验（原子性：任一非法则全部不应用）。
func ValidateSetting(key, value string) error {
	def := settingDef(key)
	if def == nil {
		return fmt.Errorf("未知配置项: %s", key)
	}
	if def.Validate != nil {
		if err := def.Validate(value); err != nil {
			return err
		}
	}
	return nil
}

// ApplySetting 校验并应用一个配置值到 Config（不持久化）。
// 返回 (是否修改, 错误)。
func (c *Config) ApplySetting(key, value string) (bool, error) {
	if err := ValidateSetting(key, value); err != nil {
		return false, err
	}
	apply, _ := c.settingKey(key)
	if old, ok := c.SettingValue(key); ok && old == value {
		return false, nil
	}
	apply(value)
	return true, nil
}

func settingDef(key string) *SettingDef {
	for _, def := range Settings() {
		if def.Key == key {
			return &def
		}
	}
	return nil
}
