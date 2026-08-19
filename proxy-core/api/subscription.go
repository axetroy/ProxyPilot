package api

import (
	"crypto/subtle"
	"net"
	"net/http"

	"github.com/axetroy/ProxyPilot/proxy-core/config"
	"github.com/axetroy/ProxyPilot/proxy-core/parser"
	"github.com/gin-gonic/gin"
)

// subscriptionConfig 订阅导出配置（GET /api/subscription 与 PUT /api/subscription 的响应结构）。
type subscriptionConfig struct {
	Enabled bool     `json:"enabled"`
	Listen  string   `json:"listen"`
	Host    string   `json:"host"`    // 对外展示的订阅 IP（通配监听时生效，空表示未选择）
	LANIPs  []string `json:"lanIPs"`  // 本机所有局域网 IP（供前端下拉选择）
	Token   string   `json:"token"`
	URL     string   `json:"url"`
}

func (s *Services) currentSubscription() subscriptionConfig {
	return subscriptionConfig{
		Enabled: s.Cfg.SubEnabled,
		Listen:  s.Cfg.SubListen,
		Host:    s.Cfg.SubHost,
		LANIPs:  config.LANIPs(),
		Token:   s.Cfg.SubToken,
		URL:     s.Cfg.SubscriptionURL(),
	}
}

// exportProxies 导出存活节点：
//   GET /api/export?format=json    → 默认，节点 JSON 列表
//   GET /api/export?format=base64  → 订阅文本（base64 编码，每行一个节点）
//   GET /api/export?format=plain   → 订阅文本（明文，每行一个节点）
func (s *Services) exportProxies(c *gin.Context) {
	nodes := s.Pool.Alive()
	switch c.DefaultQuery("format", "json") {
	case "json":
		c.JSON(http.StatusOK, ok(gin.H{"total": len(nodes), "nodes": nodes}))
	case "base64", "plain":
		content := parser.EncodeSubscription(nodes, c.Query("format") == "base64")
		c.Header("Content-Type", "text/plain; charset=utf-8")
		c.String(http.StatusOK, content)
	default:
		c.JSON(http.StatusBadRequest, fail(http.StatusBadRequest, "未知的导出格式，支持 json / base64 / plain"))
	}
}

// getSubscription 返回订阅导出配置（含订阅 URL）。
func (s *Services) getSubscription(c *gin.Context) {
	c.JSON(http.StatusOK, ok(s.currentSubscription()))
}

// updateSubscriptionReq 更新订阅导出配置的请求体。
type updateSubscriptionReq struct {
	Enabled    *bool   `json:"enabled"`    // 开关；nil 表示不修改
	Listen     *string `json:"listen"`     // 监听地址；nil 表示不修改（修改后需重启 proxy-core 生效）
	Host       *string `json:"host"`       // 对外展示的订阅 IP（通配监听时生效）；nil 表示不修改
	ResetToken bool    `json:"resetToken"` // 重置订阅密钥
}

// updateSubscriptionConfig 更新订阅导出配置（持久化到 settings 表）。
func (s *Services) updateSubscriptionConfig(c *gin.Context) {
	var req updateSubscriptionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, fail(http.StatusBadRequest, "请求体格式错误: "+err.Error()))
		return
	}
	if req.Enabled != nil {
		v := "0"
		if *req.Enabled {
			v = "1"
		}
		if err := config.ValidateSetting(config.KeySubEnabled, v); err != nil {
			c.JSON(http.StatusBadRequest, fail(http.StatusBadRequest, err.Error()))
			return
		}
		s.Cfg.SubEnabled = *req.Enabled
		_ = s.Store.SetSetting(config.KeySubEnabled, v)
	}
	if req.Listen != nil {
		if err := config.ValidateSetting(config.KeySubListen, *req.Listen); err != nil {
			c.JSON(http.StatusBadRequest, fail(http.StatusBadRequest, err.Error()))
			return
		}
		s.Cfg.SubListen = *req.Listen
		_ = s.Store.SetSetting(config.KeySubListen, *req.Listen)
		// 对外监听（0.0.0.0/::）会把代理节点订阅开放到其他设备，
		// 记录警示日志提示用户评估合规风险。
		host, _, err := net.SplitHostPort(*req.Listen)
		if err == nil && (host == "" || host == "0.0.0.0" || host == "::" || host == "[::]") {
			s.Bus.Warn("订阅服务已配置为对外监听，向其他设备提供代理节点订阅，请确认符合所在司法辖区的法律法规")
		}
	}
	if req.Host != nil {
		if err := config.ValidateSetting(config.KeySubHost, *req.Host); err != nil {
			c.JSON(http.StatusBadRequest, fail(http.StatusBadRequest, err.Error()))
			return
		}
		s.Cfg.SubHost = *req.Host
		_ = s.Store.SetSetting(config.KeySubHost, *req.Host)
	}
	if req.ResetToken {
		s.Cfg.SubToken = config.NewToken()
		_ = s.Store.SetSetting(config.KeySubToken, s.Cfg.SubToken)
	}
	c.JSON(http.StatusOK, ok(s.currentSubscription()))
}

// NewSubscriptionRouter 创建独立的订阅服务路由。
// 与主 API（17890）分离：仅暴露订阅端点，是否对外监听由用户显式配置
// （默认 127.0.0.1，避免把管理 API 一起暴露出去）。
func NewSubscriptionRouter(s *Services) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.GET("/sub/:token", s.serveSubscription)
	return r
}

// serveSubscription 处理 GET /sub/:token，返回存活节点的订阅文本。
// 支持 ?format=plain（明文）与 ?format=base64（默认）。
func (s *Services) serveSubscription(c *gin.Context) {
	if !s.Cfg.SubEnabled {
		c.JSON(http.StatusNotFound, fail(http.StatusNotFound, "订阅未开启"))
		return
	}
	token := c.Param("token")
	if token == "" {
		token = c.Query("token")
	}
	if !constantTimeEqual(token, s.Cfg.SubToken) {
		c.JSON(http.StatusUnauthorized, fail(http.StatusUnauthorized, "订阅密钥错误"))
		return
	}
	nodes := s.Pool.Alive()
	base64Encoded := c.DefaultQuery("format", "base64") != "plain"
	content := parser.EncodeSubscription(nodes, base64Encoded)
	c.Header("Content-Type", "text/plain; charset=utf-8")
	c.String(http.StatusOK, content)
}

// constantTimeEqual 常量时间比较，避免通过时序差异猜测订阅密钥。
func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
