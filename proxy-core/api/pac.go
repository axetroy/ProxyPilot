package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/axetroy/ProxyPilot/proxy-core/config"
)

// pacConfig 智能分流配置 + 规则同步状态（GET /api/pac-config 响应结构）。
type pacConfig struct {
	Enabled    bool   `json:"enabled"`    // 分流开关（关闭时全部走代理）
	Mode       string `json:"mode"`       // whitelist / blacklist
	DirectURLs string `json:"directUrls"` // 直连规则列表 URL（逗号分隔）
	ProxyURLs  string `json:"proxyUrls"`  // 代理规则列表 URL（逗号分隔）
	Refresh    string `json:"refresh"`    // 自动刷新周期

	// 手动规则名单（用户自定义域名，优先级高于同步名单）
	CustomDirect []string `json:"customDirect"`
	CustomProxy  []string `json:"customProxy"`

	// 规则同步状态（仅展示，由规则管理器回填）
	DirectCount int       `json:"directCount"` // 直连规则条数
	ProxyCount  int       `json:"proxyCount"`  // 代理规则条数
	SyncAt      time.Time `json:"syncAt,omitempty"`
	SyncError   string    `json:"syncError,omitempty"`
	Syncing     bool      `json:"syncing"`
}

func (s *Services) currentPAC() pacConfig {
	cfg := pacConfig{
		Enabled:    s.Cfg.PACEnabled,
		Mode:       s.Cfg.PACMode,
		DirectURLs: s.Cfg.PACDirectURLs,
		ProxyURLs:  s.Cfg.PACProxyURLs,
		Refresh:    s.Cfg.PACRefreshInterval.String(),
	}
	cfg.CustomDirect = splitDomains(s.Cfg.PACCustomDirect)
	cfg.CustomProxy = splitDomains(s.Cfg.PACCustomProxy)
	if s.Rule != nil {
		dc, pc, syncAt, syncErr, syncing := s.Rule.Stats()
		cfg.DirectCount = dc
		cfg.ProxyCount = pc
		cfg.SyncAt = syncAt
		cfg.SyncError = syncErr
		cfg.Syncing = syncing
	}
	return cfg
}

// splitDomains 把逗号分隔的域名名单拆成去空白后的切片。
// 始终返回非 nil 切片，保证 JSON 序列化为 [] 而非 null（前端类型为 string[]）。
func splitDomains(list string) []string {
	out := make([]string, 0)
	for _, d := range strings.Split(list, ",") {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		out = append(out, d)
	}
	return out
}

// getPACConfig 返回智能分流配置与规则同步状态。
func (s *Services) getPACConfig(c *gin.Context) {
	c.JSON(http.StatusOK, ok(s.currentPAC()))
}

// updatePACReq 更新智能分流配置的请求体（字段为 nil 表示不修改）。
type updatePACReq struct {
	Enabled      *bool     `json:"enabled"`
	Mode         *string   `json:"mode"`
	DirectURLs   *string   `json:"directUrls"`
	ProxyURLs    *string   `json:"proxyUrls"`
	Refresh      *string   `json:"refresh"`
	CustomDirect *[]string `json:"customDirect"` // 手动直连名单（整表覆盖；nil 表示不修改）
	CustomProxy  *[]string `json:"customProxy"`  // 手动代理名单（整表覆盖；nil 表示不修改）
}

// updatePACConfig 更新智能分流配置并持久化。
// 规则源（directUrls/proxyUrls）变化后自动触发一次规则同步。
func (s *Services) updatePACConfig(c *gin.Context) {
	var req updatePACReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, fail(http.StatusBadRequest, "请求体格式错误: "+err.Error()))
		return
	}
	if req.Enabled != nil {
		val := "0"
		if *req.Enabled {
			val = "1"
		}
		if err := config.ValidateSetting(config.KeyPACEnabled, val); err != nil {
			c.JSON(http.StatusBadRequest, fail(http.StatusBadRequest, err.Error()))
			return
		}
		s.Cfg.PACEnabled = *req.Enabled
		_ = s.Store.SetSetting(config.KeyPACEnabled, val)
	}
	if req.Mode != nil {
		if err := config.ValidateSetting(config.KeyPACMode, *req.Mode); err != nil {
			c.JSON(http.StatusBadRequest, fail(http.StatusBadRequest, err.Error()))
			return
		}
		s.Cfg.PACMode = *req.Mode
		_ = s.Store.SetSetting(config.KeyPACMode, *req.Mode)
	}
	if req.DirectURLs != nil {
		if err := config.ValidateSetting(config.KeyPACDirectURLs, *req.DirectURLs); err != nil {
			c.JSON(http.StatusBadRequest, fail(http.StatusBadRequest, err.Error()))
			return
		}
		s.Cfg.PACDirectURLs = *req.DirectURLs
		_ = s.Store.SetSetting(config.KeyPACDirectURLs, *req.DirectURLs)
	}
	if req.ProxyURLs != nil {
		if err := config.ValidateSetting(config.KeyPACProxyURLs, *req.ProxyURLs); err != nil {
			c.JSON(http.StatusBadRequest, fail(http.StatusBadRequest, err.Error()))
			return
		}
		s.Cfg.PACProxyURLs = *req.ProxyURLs
		_ = s.Store.SetSetting(config.KeyPACProxyURLs, *req.ProxyURLs)
	}
	if req.Refresh != nil {
		if err := config.ValidateSetting(config.KeyPACRefresh, *req.Refresh); err != nil {
			c.JSON(http.StatusBadRequest, fail(http.StatusBadRequest, err.Error()))
			return
		}
		if d, err := time.ParseDuration(*req.Refresh); err == nil {
			s.Cfg.PACRefreshInterval = d
		}
		_ = s.Store.SetSetting(config.KeyPACRefresh, *req.Refresh)
	}
	// 手动规则名单：整表覆盖（nil 表示不修改）
	for _, upd := range []struct {
		key   string
		items *[]string
		apply func(string)
	}{
		{config.KeyPACCustomDirect, req.CustomDirect, func(v string) { s.Cfg.PACCustomDirect = v }},
		{config.KeyPACCustomProxy, req.CustomProxy, func(v string) { s.Cfg.PACCustomProxy = v }},
	} {
		if upd.items == nil {
			continue
		}
		joined := joinDomains(*upd.items)
		if err := config.ValidateSetting(upd.key, joined); err != nil {
			c.JSON(http.StatusBadRequest, fail(http.StatusBadRequest, err.Error()))
			return
		}
		upd.apply(joined)
		_ = s.Store.SetSetting(upd.key, joined)
	}

	// 同步开关/模式/规则源/刷新周期到规则管理器（保护其内部状态的一致性）
	if s.Rule != nil {
		s.Rule.ApplyConfig()
	}

	// 规则源变化后立即同步一次（异步，不阻塞响应）；
	// 失败或进行中会被 rule 记录到 syncErr/Syncing，前端通过 GET /api/pac-config 可见。
	if s.Rule != nil && (req.DirectURLs != nil || req.ProxyURLs != nil) {
		go func() { _ = s.Rule.SyncNow(context.Background()) }()
	}
	c.JSON(http.StatusOK, ok(s.currentPAC()))
}

// syncPAC 手动触发规则同步。
func (s *Services) syncPAC(c *gin.Context) {
	if s.Rule == nil {
		c.JSON(http.StatusInternalServerError, fail(1, "规则管理器不可用"))
		return
	}
	if err := s.Rule.SyncNow(c.Request.Context()); err != nil {
		c.JSON(http.StatusInternalServerError, fail(1, err.Error()))
		return
	}
	c.JSON(http.StatusOK, ok(s.currentPAC()))
}

// joinDomains 把域名列表规范化（小写、去空白、去重）后以逗号连接。
func joinDomains(items []string) string {
	seen := make(map[string]struct{})
	var parts []string
	for _, d := range items {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == "" {
			continue
		}
		if _, ok := seen[d]; ok {
			continue
		}
		seen[d] = struct{}{}
		parts = append(parts, d)
	}
	return strings.Join(parts, ",")
}