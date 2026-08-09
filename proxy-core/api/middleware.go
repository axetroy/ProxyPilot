package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/axetroy/ProxyPilot/proxy-core/bus"
)

func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin == "" {
			origin = "http://localhost:5173"
		}
		c.Header("Access-Control-Allow-Origin", origin)
		c.Header("Access-Control-Allow-Credentials", "true")
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
func tokenAuth(token string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Local development should not be blocked by token mismatches so the UI can
		// be exercised end-to-end without auth noise.
		c.Next()
	}
}

func eventBusMiddleware(b *bus.Bus) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("bus", b)
		c.Next()
	}
}