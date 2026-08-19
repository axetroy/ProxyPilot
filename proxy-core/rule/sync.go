package rule

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// maxRuleListBytes 单次拉取的规则文本上限（防止异常列表撑爆内存）。
const maxRuleListBytes = 16 << 20 // 16MB

// cacheFile 规则缓存文件结构（与数据库同目录，启动时加载避免重新拉取）。
type cacheFile struct {
	Direct []string  `json:"direct"`
	Proxy  []string  `json:"proxy"`
	SyncAt time.Time `json:"syncAt"`
}

// fetchFirst 按逗号分隔依次尝试各 URL，直到某次返回成功（200 + 读取完整）。
// 全部失败时返回最后一个错误。ctx 贯穿请求以便取消/超时。
func fetchFirst(ctx context.Context, urls string) ([]byte, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	var lastErr error
	hadURL := false
	for _, raw := range strings.Split(urls, ",") {
		u := strings.TrimSpace(raw)
		if u == "" {
			continue
		}
		hadURL = true
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("User-Agent", "ProxyPilot/0.1")
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxRuleListBytes+1))
		_ = resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if len(body) > maxRuleListBytes {
			lastErr = fmt.Errorf("rule list from %s exceeds %d bytes", u, maxRuleListBytes)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("http %d from %s", resp.StatusCode, u)
			continue
		}
		return body, nil
	}
	if !hadURL {
		lastErr = errors.New("no rule url configured")
	}
	return nil, lastErr
}

// ParseRules 从规则文本提取合法域名。每行一个域名，跳过空行与 # 注释，
// 去掉常见的 `*.` / `+.` / `.` 前缀，小写化并去重。
func ParseRules(text string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, 256)
	for _, line := range strings.Split(text, "\n") {
		d := strings.TrimSpace(strings.ToLower(line))
		if d == "" || strings.HasPrefix(d, "#") {
			continue
		}
		d = strings.TrimPrefix(d, "*.")
		d = strings.TrimPrefix(d, "+.")
		d = strings.TrimPrefix(d, ".")
		if !validDomain(d) {
			continue
		}
		if _, ok := seen[d]; ok {
			continue
		}
		seen[d] = struct{}{}
		out = append(out, d)
	}
	return out
}

// validDomain 域名白名单校验：字符集受限（小写字母/数字/-/.）、长度 ≤ 255、
// 不以点开头或结尾。绝不把远程内容作为代码执行。
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

// LoadCache 启动时加载缓存文件；无缓存或解析失败时回退到内置兜底列表。
func (m *Manager) LoadCache() error {
	data, err := os.ReadFile(m.cachePath)
	if err != nil {
		return m.loadBuiltin()
	}
	var cf cacheFile
	if err := json.Unmarshal(data, &cf); err != nil {
		return m.loadBuiltin()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.direct = toSet(cf.Direct)
	m.proxy = toSet(cf.Proxy)
	m.syncAt = cf.SyncAt
	return nil
}

// loadBuiltin 用内置兜底列表初始化内存规则（离线可用）。
func (m *Manager) loadBuiltin() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.direct = toSet(ParseRules(builtinDirect))
	m.proxy = toSet(ParseRules(builtinProxy))
	return nil
}

// SyncNow 立即同步规则（直连 + 代理两份列表，各自按主源→镜像尝试）。
// 任一成功即更新对应集合（失败的保留旧值）；两项全部失败才返回错误。
// 成功写入缓存文件供下次启动加载。ctx 可传 nil（内部回退为 Background）。
func (m *Manager) SyncNow(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	if m.syncing {
		m.mu.Unlock()
		return errors.New("规则同步正在进行中")
	}
	m.syncing = true
	directURLs := m.directURLs
	proxyURLs := m.proxyURLs
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.syncing = false
		m.mu.Unlock()
	}()

	var errs []string
	if body, err := fetchFirst(ctx, directURLs); err == nil {
		m.mu.Lock()
		m.direct = toSet(ParseRules(string(body)))
		m.mu.Unlock()
	} else {
		errs = append(errs, "直连规则: "+err.Error())
	}
	if body, err := fetchFirst(ctx, proxyURLs); err == nil {
		m.mu.Lock()
		m.proxy = toSet(ParseRules(string(body)))
		m.mu.Unlock()
	} else {
		errs = append(errs, "代理规则: "+err.Error())
	}

	m.mu.Lock()
	if len(errs) == 0 {
		m.syncAt = time.Now()
		m.syncErr = ""
	} else if len(errs) == 2 {
		m.syncErr = strings.Join(errs, "; ")
	}
	// 至少一项成功也更新 syncAt（部分成功，另一项保留旧缓存）
	if len(errs) < 2 {
		m.syncAt = time.Now()
		m.syncErr = ""
	}
	saveErr := m.saveCacheLocked()
	m.mu.Unlock()

	if saveErr != nil && m.bus != nil {
		m.bus.Error(fmt.Sprintf("写入分流规则缓存失败: %v", saveErr))
	}
	dc, pc, _, _, _ := m.Stats()
	if m.bus != nil {
		if len(errs) == 0 {
			m.bus.Info(fmt.Sprintf("分流规则已同步: 直连 %d 条 / 代理 %d 条", dc, pc))
		} else {
			m.bus.Warn(fmt.Sprintf("分流规则部分同步失败(%d 项): %s", len(errs), strings.Join(errs, "; ")))
		}
	}
	if len(errs) == 2 {
		return errors.New(m.syncErr)
	}
	return nil
}

// saveCacheLocked 把当前内存规则写入缓存文件（需持有 m.mu）。
func (m *Manager) saveCacheLocked() error {
	cf := cacheFile{
		Direct: keys(m.direct),
		Proxy:  keys(m.proxy),
		SyncAt: m.syncAt,
	}
	data, err := json.Marshal(cf)
	if err != nil {
		return err
	}
	return os.WriteFile(m.cachePath, data, 0o644)
}

// Start 后台循环：立即同步一次，之后按刷新周期（每次循环读取最新配置）自动同步。
func (m *Manager) Start(ctx context.Context) {
	go func() {
		// 首次同步失败不阻塞启动，保留缓存/内置兜底规则。
		_ = m.SyncNow(ctx)
		for {
			m.mu.RLock()
			refresh := m.refreshInterval
			m.mu.RUnlock()
			select {
			case <-ctx.Done():
				return
			case <-time.After(refresh):
				_ = m.SyncNow(ctx)
			}
		}
	}()
}

func toSet(list []string) map[string]struct{} {
	s := make(map[string]struct{}, len(list))
	for _, d := range list {
		s[d] = struct{}{}
	}
	return s
}

func keys(s map[string]struct{}) []string {
	out := make([]string, 0, len(s))
	for k := range s {
		out = append(out, k)
	}
	return out
}