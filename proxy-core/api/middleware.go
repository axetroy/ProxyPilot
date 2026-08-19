package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/axetroy/ProxyPilot/proxy-core/bus"
)

// allowedCORSOrigins 是允许跨域访问 API 的来源白名单。
// 仅放行本地开发（Vite）与 Electron 渲染进程，禁止回显任意 Origin，
// 防止恶意网页通过浏览器跨域调用本机 API。
var allowedCORSOrigins = map[string]bool{
	"http://localhost:5173":  true,
	"http://127.0.0.1:5173":  true,
	"https://localhost:5173": true,
	"file://":                true,
	"null":                   true, // Electron 渲染进程在 file:// 下的 Origin 可能是 null
}

func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		// 仅对白名单来源回显 Allow-Origin；非白名单来源不设置 CORS 头，
		// 浏览器会拦截跨域读取（服务端仍处理请求，但响应被浏览器拒绝）。
		if origin != "" && allowedCORSOrigins[origin] {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Credentials", "true")
		}
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Accept, X-Token, Authorization")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// tokenAuth rejects requests without the session token.
// Token is read from the X-Token header or the ?token= query param (WebSocket).
// token 由 config.New() 启动时随机生成，永不为空；token 为空视为配置异常，
// 同样拒绝，避免本机恶意程序无鉴权访问管理 API。
func tokenAuth(token string) gin.HandlerFunc {
	return func(c *gin.Context) {
		got := c.GetHeader("X-Token")
		if got == "" {
			got = c.Query("token")
		}
		if token == "" || got != token {
			c.AbortWithStatusJSON(http.StatusUnauthorized, fail(401, "invalid session token"))
			return
		}
		c.Next()
	}
}

func eventBusMiddleware(b *bus.Bus) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("bus", b)
		c.Next()
	}
}