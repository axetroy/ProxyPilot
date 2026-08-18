package api

import (
	"context"
	"net/http"
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

// getPACConfig 返回智能分流配置与规则同步状态。
func (s *Services) getPACConfig(c *gin.Context) {
	c.JSON(http.StatusOK, ok(s.currentPAC()))
}

// updatePACReq 更新智能分流配置的请求体（字段为 nil 表示不修改）。
type updatePACReq struct {
	Enabled    *bool   `json:"enabled"`
	Mode       *string `json:"mode"`
	DirectURLs *string `json:"directUrls"`
	ProxyURLs  *string `json:"proxyUrls"`
	Refresh    *string `json:"refresh"`
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