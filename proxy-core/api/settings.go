package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/axetroy/ProxyPilot/proxy-core/config"
	"github.com/axetroy/ProxyPilot/proxy-core/validator"
)

// settingItem 返回给前端的单个配置项。
type settingItem struct {
	Key     string `json:"key"`
	Value   string `json:"value"`
	Default string `json:"default"`
	Desc    string `json:"desc"`
}

// buildChecker 基于当前配置构建节点检测器（target/timeout 变化时热替换）。
func (s *Services) buildChecker() *validator.Checker {
	return validator.NewChecker(s.Cfg.CheckTarget, s.Cfg.CheckTimeout)
}

// listSettings 返回所有可配置项及其当前值。
func (s *Services) listSettings(c *gin.Context) {
	items := make([]settingItem, 0, len(config.Settings()))
	for _, def := range config.Settings() {
		value, _ := s.Cfg.SettingValue(def.Key)
		items = append(items, settingItem{Key: def.Key, Value: value, Default: def.Default, Desc: def.Desc})
	}
	c.JSON(http.StatusOK, ok(items))
}

// updateSettings 接受 {"key": "value"}，校验后应用并持久化。
// 支持热更新的配置立即生效：
//   - check_target / check_timeout：重建检测器
//   - check_concurrency：更新并发数
//   - refresh_interval：更新自动检测周期
//   - http_proxy_bind / socks5_proxy_bind：网关运行中时自动重启网关切换端口
func (s *Services) updateSettings(c *gin.Context) {
	var updates map[string]string
	if err := c.ShouldBindJSON(&updates); err != nil {
		c.JSON(http.StatusBadRequest, fail(400, "请求体格式错误"))
		return
	}
	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, fail(400, "没有需要更新的配置"))
		return
	}

	// 先整体校验（不修改状态），任一非法则全部不应用（原子性）
	for key, value := range updates {
		if err := config.ValidateSetting(key, value); err != nil {
			c.JSON(http.StatusBadRequest, fail(400, err.Error()))
			return
		}
	}

	// 校验通过，逐项应用并持久化
	changed := false
	for key, value := range updates {
		applied, err := s.Cfg.ApplySetting(key, value)
		if err != nil {
			continue // 已在上面校验过，不会走到这里
		}
		if !applied {
			continue
		}
		changed = true
		if err := s.Store.SetSetting(key, value); err != nil {
			s.Bus.Error("persist setting failed: " + err.Error())
		}
		switch key {
		case config.KeyCheckTarget, config.KeyCheckTimeout:
			s.Pool.SetChecker(s.buildChecker())
			s.Bus.Info("checker updated (target/timeout)")
		case config.KeyCheckConcurr:
			n := s.Cfg.CheckConcurrency
			s.Pool.SetConcurrency(n)
			s.Bus.Info("check concurrency updated")
		case config.KeyRefreshPeriod:
			s.Pool.SetRefreshInterval(s.Cfg.RefreshInterval)
			s.Bus.Info("refresh interval updated")
		case config.KeyHTTPBind, config.KeySOCKSBind:
			if err := s.restartGatewayIfRunning(); err != nil {
				s.Bus.Error("gateway restart failed: " + err.Error())
			}
		}
	}

	items := make([]settingItem, 0, len(config.Settings()))
	for _, def := range config.Settings() {
		value, _ := s.Cfg.SettingValue(def.Key)
		items = append(items, settingItem{Key: def.Key, Value: value, Default: def.Default, Desc: def.Desc})
	}
	c.JSON(http.StatusOK, ok(gin.H{"changed": changed, "settings": items}))
}

// restartGatewayIfRunning 网关运行中且端口配置变化时，Stop 后以新端口重新 Start。
func (s *Services) restartGatewayIfRunning() error {
	if !s.Gateway.Running() {
		return nil
	}
	s.Gateway.SetAddrs(s.Cfg.HTTPProxyBind, s.Cfg.SOCKSProxyBind)
	s.Gateway.Stop()
	if err := s.Gateway.Start(); err != nil {
		return err
	}
	s.Bus.Info("gateway restarted with new ports")
	return nil
}
