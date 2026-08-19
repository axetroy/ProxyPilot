package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		// 无 Origin（非浏览器客户端，如 curl）或命中本地白名单才允许升级。
		if origin == "" {
			return true
		}
		return allowedCORSOrigins[origin]
	},
}

// websocket streams bus events (logs, progress) to the Electron UI.
// Token is verified via the X-Token header (middleware) or ?token= query param.
func (s *Services) websocket(c *gin.Context) {
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()

	ctx := c.Request.Context()
	sub := s.Bus.Subscribe()
	defer s.Bus.Unsubscribe(sub)

	// replay history + live events
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-sub:
			if !ok {
				return
			}
			if err := conn.WriteJSON(e); err != nil {
				return
			}
		case <-ticker.C:
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}