package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/axetroy/ProxyPilot/proxy-core/bus"
	"github.com/axetroy/ProxyPilot/proxy-core/collector"
	"github.com/axetroy/ProxyPilot/proxy-core/config"
	"github.com/axetroy/ProxyPilot/proxy-core/gateway"
	"github.com/axetroy/ProxyPilot/proxy-core/model"
	"github.com/axetroy/ProxyPilot/proxy-core/pool"
	"github.com/axetroy/ProxyPilot/proxy-core/rule"
	"github.com/axetroy/ProxyPilot/proxy-core/scheduler"
	"github.com/axetroy/ProxyPilot/proxy-core/storage"
)

// Services aggregates dependencies injected into the HTTP handlers.
type Services struct {
	Cfg       *config.Config
	Store     *storage.Store
	Pool      *pool.Manager
	Collector *collector.Manager
	Gateway   *gateway.Gateway
	Selector  *scheduler.Selector
	Bus       *bus.Bus
	Rule      *rule.Manager
}

type response struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data any    `json:"data,omitempty"`
}

func ok(data any) response               { return response{Code: 0, Msg: "ok", Data: data} }
func fail(code int, msg string) response { return response{Code: code, Msg: msg} }

func buildSystemStatus(running bool, total, alive int, currentNode *model.ProxyNode, httpBind, socksBind, version string) model.SystemStatus {
	var currentIP string
	if currentNode != nil {
		currentIP = currentNode.Host
	}
	return model.SystemStatus{
		Running:        running,
		ProxyCount:     total,
		AliveCount:     alive,
		CurrentIP:      currentIP,
		CurrentNode:    currentNode,
		HTTPProxyBind:  httpBind,
		SOCKSProxyBind: socksBind,
		Version:        version,
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
	r.GET("/api/settings", s.listSettings)
	r.PUT("/api/settings", s.updateSettings)
	r.GET("/api/subscriptions", s.listSubscriptions)
	r.POST("/api/subscription", s.createSubscription)
	r.DELETE("/api/subscription/:id", s.deleteSubscription)
	r.POST("/api/subscription/:id/refresh", s.refreshSubscription)
	r.PUT("/api/subscription/:id", s.updateSubscription)
	r.GET("/api/export", s.exportProxies)
	r.GET("/api/subscription", s.getSubscription)
	r.PUT("/api/subscription", s.updateSubscriptionConfig)
	r.GET("/api/db/status", s.dbStatus)
	r.POST("/api/db/compact", s.compactDb)
	r.GET("/api/proxies", s.listProxies)
	r.DELETE("/api/proxy/:id", s.deleteProxy)
	r.PUT("/api/proxy/pin", s.pinProxy)
	r.DELETE("/api/proxy/pin", s.unpinProxy)
	r.GET("/api/egress", s.getEgress)
	r.PUT("/api/egress", s.updateEgress)
	r.GET("/api/chains", s.listChains)
	r.POST("/api/chain", s.createChain)
	r.PUT("/api/chain/:id", s.updateChain)
	r.DELETE("/api/chain/:id", s.deleteChain)
	r.POST("/api/proxy/check", s.checkProxies)
	r.POST("/api/gateway/start", s.startGateway)
	r.POST("/api/gateway/stop", s.stopGateway)
	r.GET("/api/pac-config", s.getPACConfig)
	r.PUT("/api/pac-config", s.updatePACConfig)
	r.POST("/api/pac/sync", s.syncPAC)
	r.GET("/ws", s.websocket)
	return r
}

func (s *Services) status(c *gin.Context) {
	// 使用内存池计数：前端每秒轮询该接口，避免每次都查询 SQLite
	// （存储层为单连接串行化，读查询还会与检测/抓取的写查询互相等待）。
	total := s.Pool.Count()
	alive := s.Pool.CountAlive()
	currentNode := s.Gateway.CurrentNode()
	st := buildSystemStatus(
		s.Gateway.Running(),
		total,
		alive,
		currentNode,
		s.Gateway.HTTPAddr(),
		s.Gateway.SOCKSAddr(),
		config.Version,
	)
	// 附加用户指定的固定出口节点（未指定时为空）。
	if s.Selector != nil {
		st.PinnedNode = s.Selector.Pinned()
	}
	c.JSON(http.StatusOK, ok(st))
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
	// 为每个节点填充评分明细，供前端展示评分计算过程。
	for _, n := range nodes {
		n.ScoreBreakdown = pool.Breakdown(n)
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
	// 删除的正是固定出口节点时，自动取消指定，避免留下悬空引用。
	// 仅当当前策略为 fixed 时恢复加权（Unpin 会切换策略）；
	// 其他策略下只清除指定，不干扰用户当前选择的策略。
	if s.Selector != nil && s.Selector.PinnedID() == id {
		if s.Selector.Strategy() == scheduler.StrategyFixed {
			s.Selector.Unpin()
		} else {
			s.Selector.Pin(0)
		}
		_ = s.Store.SetSetting(config.KeyPinnedProxy, "")
		s.Bus.Info("deleted node was pinned, cleared exit pin")
	}
	c.JSON(http.StatusOK, ok(nil))
}

// pinPayload 指定固定出口节点的请求体。
type pinPayload struct {
	ID int64 `json:"id" binding:"required"`
}

// pinProxy 把某个节点指定为固定出口（之后不再按评分自动选择）。
func (s *Services) pinProxy(c *gin.Context) {
	var payload pinPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, fail(400, "请求体格式错误"))
		return
	}
	if payload.ID <= 0 {
		c.JSON(http.StatusBadRequest, fail(400, "无效的节点 ID"))
		return
	}
	if s.Selector == nil {
		c.JSON(http.StatusInternalServerError, fail(1, "selector unavailable"))
		return
	}
	node := s.Pool.Get(payload.ID)
	if node == nil {
		c.JSON(http.StatusNotFound, fail(404, "节点不存在"))
		return
	}
	s.Selector.Pin(payload.ID)
	// 指定固定出口后策略同步为 fixed（由 Pin 内部处理），此处持久化便于重启恢复。
	if err := s.Store.SetSetting(config.KeyPinnedProxy, strconv.FormatInt(payload.ID, 10)); err != nil {
		s.Bus.Error("persist pinned proxy failed: " + err.Error())
	}
	_ = s.Store.SetSetting(config.KeyEgressStrategy, string(scheduler.StrategyFixed))
	s.Bus.Info(fmt.Sprintf("pinned exit node: %s", node.Key()))
	c.JSON(http.StatusOK, ok(node))
}

// unpinProxy 取消固定出口指定，恢复智能加权策略。
func (s *Services) unpinProxy(c *gin.Context) {
	if s.Selector != nil {
		// Unpin 内部会恢复为智能加权策略。
		s.Selector.Unpin()
	}
	_ = s.Store.SetSetting(config.KeyPinnedProxy, "")
	_ = s.Store.SetSetting(config.KeyEgressStrategy, string(scheduler.StrategyWeighted))
	s.Bus.Info("unpinned exit node, back to weighted strategy")
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
		"http":   s.Gateway.HTTPAddr(),
		"socks5": s.Gateway.SOCKSAddr(),
	}))
}

func (s *Services) stopGateway(c *gin.Context) {
	s.Gateway.Stop()
	c.JSON(http.StatusOK, ok(nil))
}
