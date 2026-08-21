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

// wsWriteTimeout 是 WebSocket 单次写入的超时：客户端慢读或网络卡顿时，
// WriteJSON/Ping 可能无限阻塞发送 goroutine；设置写 deadline 后超时即断开，
// 避免慢客户端拖垮事件推送通道。
const wsWriteTimeout = 10 * time.Second

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
			_ = conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
			if err := conn.WriteJSON(e); err != nil {
				return
			}
		case <-ticker.C:
			_ = conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}