package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/axetroy/ProxyPilot/proxy-core/bus"
	"github.com/axetroy/ProxyPilot/proxy-core/collector"
	"github.com/axetroy/ProxyPilot/proxy-core/config"
	"github.com/axetroy/ProxyPilot/proxy-core/gateway"
	"github.com/axetroy/ProxyPilot/proxy-core/model"
	"github.com/axetroy/ProxyPilot/proxy-core/pool"
	"github.com/axetroy/ProxyPilot/proxy-core/storage"
)

// Services aggregates dependencies injected into the HTTP handlers.
type Services struct {
	Cfg       *config.Config
	Store     *storage.Store
	Pool      *pool.Manager
	Collector *collector.Manager
	Gateway   *gateway.Gateway
	Bus       *bus.Bus
}

type response struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data any    `json:"data,omitempty"`
}

func ok(data any) response               { return response{Code: 0, Msg: "ok", Data: data} }
func fail(code int, msg string) response { return response{Code: code, Msg: msg} }

func buildSystemStatus(running bool, total, alive int, currentNode, currentHTTPNode, currentSOCKS5Node *model.ProxyNode, httpBind, socksBind, version string) model.SystemStatus {
	var currentIP string
	if currentHTTPNode != nil {
		currentIP = currentHTTPNode.Host
	} else if currentSOCKS5Node != nil {
		currentIP = currentSOCKS5Node.Host
	} else if currentNode != nil {
		currentIP = currentNode.Host
	}
	return model.SystemStatus{
		Running:           running,
		ProxyCount:        total,
		AliveCount:        alive,
		CurrentIP:         currentIP,
		CurrentNode:       currentNode,
		CurrentHTTPNode:   currentHTTPNode,
		CurrentSOCKS5Node: currentSOCKS5Node,
		HTTPProxyBind:     httpBind,
		SOCKSProxyBind:    socksBind,
		Version:           version,
	}
}

// NewRouter builds the Gin engine with token protection.
func NewRouter(s *Services) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(corsMiddleware())
	r.Use(tokenAuth(s.Cfg.SessionToken))
	r.Use(eventBusMiddleware(s.Bus))

	r.GET("/api/status", s.status)
	r.GET("/api/subscriptions", s.listSubscriptions)
	r.POST("/api/subscription", s.createSubscription)
	r.DELETE("/api/subscription/:id", s.deleteSubscription)
	r.POST("/api/subscription/:id/refresh", s.refreshSubscription)
r.PUT("/api/subscription/:id", s.updateSubscription)
	r.GET("/api/proxies", s.listProxies)
	r.DELETE("/api/proxy/:id", s.deleteProxy)
	r.POST("/api/proxy/check", s.checkProxies)
	r.POST("/api/gateway/start", s.startGateway)
	r.POST("/api/gateway/stop", s.stopGateway)
	r.GET("/ws", s.websocket)
	return r
}

func (s *Services) status(c *gin.Context) {
	total, err := s.Store.CountNodes()
	if err != nil {
		c.JSON(http.StatusInternalServerError, fail(1, err.Error()))
		return
	}
	alive, err := s.Store.CountNodesByStatus(model.StatusAlive)
	if err != nil {
		c.JSON(http.StatusInternalServerError, fail(1, err.Error()))
		return
	}
	currentNode := s.Gateway.CurrentNode()
	currentHTTPNode := s.Gateway.CurrentHTTPNode()
	currentSOCKS5Node := s.Gateway.CurrentSOCKS5Node()
	c.JSON(http.StatusOK, ok(buildSystemStatus(
		s.Gateway.Running(),
		total,
		alive,
		currentNode,
		currentHTTPNode,
		currentSOCKS5Node,
		s.Cfg.HTTPProxyBind,
		s.Cfg.SOCKSProxyBind,
		config.Version,
	)))
}

func (s *Services) listSubscriptions(c *gin.Context) {
	subs, err := s.Store.ListSubscriptions()
	if err != nil {
		c.JSON(http.StatusInternalServerError, fail(1, err.Error()))
		return
	}
	c.JSON(http.StatusOK, ok(subs))
}

type subscriptionPayload struct {
	Name     string `json:"name" binding:"required"`
	URL      string `json:"url" binding:"required"`
	Interval int64  `json:"interval"` // seconds
	Enabled  bool   `json:"enabled"`
}

func (s *Services) createSubscription(c *gin.Context) {
	var payload subscriptionPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, fail(400, err.Error()))
		return
	}
	if payload.Interval <= 0 {
		payload.Interval = 3600
	}
	sub, err := s.Collector.AddSubscription(payload.Name, payload.URL, payload.Interval)
	if err != nil {
		c.JSON(http.StatusInternalServerError, fail(1, err.Error()))
		return
	}
	c.JSON(http.StatusOK, ok(sub))
}

func (s *Services) deleteSubscription(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, fail(400, "bad id"))
		return
	}
	subs, err := s.Collector.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, fail(1, err.Error()))
		return
	}
	for _, sub := range subs {
		if sub.ID == id {
			if err := s.Collector.Delete(sub.ID); err != nil {
				c.JSON(http.StatusInternalServerError, fail(1, err.Error()))
				return
			}
			c.JSON(http.StatusOK, ok(nil))
			return
		}
	}
	c.JSON(http.StatusNotFound, fail(404, "subscription not found"))
}

func (s *Services) refreshSubscription(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, fail(400, "bad id"))
		return
	}
	subs, err := s.Collector.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, fail(1, err.Error()))
		return
	}
	for _, sub := range subs {
		if sub.ID == id {
			if err := s.Collector.FetchNow(c.Request.Context(), sub); err != nil {
				c.JSON(http.StatusInternalServerError, fail(1, err.Error()))
				return
			}
			c.JSON(http.StatusOK, ok(sub))
			return
		}
	}
	c.JSON(http.StatusNotFound, fail(404, "subscription not found"))
}

func (s *Services) updateSubscription(c *gin.Context) {
    id, err := strconv.ParseInt(c.Param("id"), 10, 64)
    if err != nil {
        c.JSON(http.StatusBadRequest, fail(400, "bad id"))
        return
    }
    var payload subscriptionPayload
    if err := c.ShouldBindJSON(&payload); err != nil {
        c.JSON(http.StatusBadRequest, fail(400, err.Error()))
        return
    }
    if payload.Interval <= 0 {
        payload.Interval = 3600
    }
    if err := s.Collector.UpdateSubscription(id, payload.Name, payload.URL, payload.Interval, payload.Enabled); err != nil {
        c.JSON(http.StatusInternalServerError, fail(1, err.Error()))
        return
    }
    c.JSON(http.StatusOK, ok(nil))
}

func (s *Services) listProxies(c *gin.Context) {
	status := c.Query("status")
	var nodes []*model.ProxyNode
	var err error
	switch status {
	case "":
		nodes = s.Pool.List()
	default:
		nodes, err = s.Store.ListNodesByStatus(model.ProxyStatus(status))
		if err != nil {
			c.JSON(http.StatusInternalServerError, fail(1, err.Error()))
			return
		}
	}
	c.JSON(http.StatusOK, ok(nodes))
}

func (s *Services) deleteProxy(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, fail(400, "bad id"))
		return
	}
	if err := s.Pool.Remove(id); err != nil {
		c.JSON(http.StatusInternalServerError, fail(1, err.Error()))
		return
	}
	c.JSON(http.StatusOK, ok(nil))
}

type checkPayload struct {
	ID *int64 `json:"id,omitempty"`
}

func (s *Services) checkProxies(c *gin.Context) {
	var payload checkPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, fail(400, err.Error()))
		return
	}
	if payload.ID != nil {
		node, err := s.Store.GetNode(*payload.ID)
		if err != nil {
			c.JSON(http.StatusNotFound, fail(404, "node not found"))
			return
		}
		result := s.Pool.CheckNode(node)
		c.JSON(http.StatusOK, ok(result))
		return
	}
	go func() {
		_ = s.Pool.CheckNow(context.Background())
	}()
	c.JSON(http.StatusAccepted, ok(gin.H{"started": true, "total": s.Pool.Count()}))
}

func (s *Services) startGateway(c *gin.Context) {
	if !s.Gateway.Running() {
		if err := s.Gateway.Start(); err != nil {
			c.JSON(http.StatusInternalServerError, fail(1, err.Error()))
			return
		}
	}
	c.JSON(http.StatusOK, ok(gin.H{
		"http":   s.Cfg.HTTPProxyBind,
		"socks5": s.Cfg.SOCKSProxyBind,
	}))
}

func (s *Services) stopGateway(c *gin.Context) {
	s.Gateway.Stop()
	c.JSON(http.StatusOK, ok(nil))
}
