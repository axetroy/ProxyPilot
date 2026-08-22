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
// metricsHandler: 可选的 /metrics 处理器（无需 token，供 Prometheus 抓取），为 nil 时不注册。
func NewRouter(s *Services, metricsHandler ...http.Handler) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(corsMiddleware())

	// /metrics 端点无需 token 鉴权，供 Prometheus 直接抓取
	if len(metricsHandler) > 0 && metricsHandler[0] != nil {
		r.GET("/metrics", gin.WrapH(metricsHandler[0]))
	}

	r.Use(tokenAuth(s.Cfg.SessionToken))
	r.Use(eventBusMiddleware(s.Bus))

	r.GET("/api/status", s.status)
	r.GET("/api/traffic", s.traffic)
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
	r.GET("/api/proxy/:id/history", s.proxyHistory)
	r.POST("/api/proxy/batch-delete", s.batchDeleteProxy)
	r.PUT("/api/proxy/pin", s.pinProxy)
	r.DELETE("/api/proxy/pin", s.unpinProxy)
	r.GET("/api/egress", s.getEgress)
	r.PUT("/api/egress", s.updateEgress)
	r.POST("/api/egress/auto-chain/test", s.testAutoChain)
	r.GET("/api/chains", s.listChains)
	r.POST("/api/chain", s.createChain)
	r.PUT("/api/chain/:id", s.updateChain)
	r.DELETE("/api/chain/:id", s.deleteChain)
	r.POST("/api/chain/:id/test", s.testChain)
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
	sub, err := s.Collector.Get(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, fail(1, err.Error()))
		return
	}
	if sub == nil {
		c.JSON(http.StatusNotFound, fail(404, "subscription not found"))
		return
	}
	if err := s.Collector.Delete(sub.ID); err != nil {
		c.JSON(http.StatusInternalServerError, fail(1, err.Error()))
		return
	}
	c.JSON(http.StatusOK, ok(nil))
}

func (s *Services) refreshSubscription(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, fail(400, "bad id"))
		return
	}
	sub, err := s.Collector.Get(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, fail(1, err.Error()))
		return
	}
	if sub == nil {
		c.JSON(http.StatusNotFound, fail(404, "subscription not found"))
		return
	}
	result, err := s.Collector.FetchNow(c.Request.Context(), sub)
	if err != nil {
		c.JSON(http.StatusInternalServerError, fail(1, err.Error()))
		return
	}
	// 返回本次抓取摘要：解析节点总数与新增节点数，供前端确认订阅内容是否有效。
	// result 为 nil 表示该订阅正在抓取中（防重入跳过），此时无摘要可返回。
	resp := gin.H{"id": sub.ID}
	if result != nil {
		resp["total"] = result.Total
		resp["added"] = result.Added
	}
	c.JSON(http.StatusOK, ok(resp))
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

// proxyHistory 返回单个节点的检测历史（按时间正序，旧→新），供前端绘制延迟趋势曲线。
// limit 默认 60，上限 500，防止一次拉取过多。
func (s *Services) proxyHistory(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, fail(400, "bad id"))
		return
	}
	limit := 60
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	hist, err := s.Store.RecentHistory(id, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, fail(1, err.Error()))
		return
	}
	// RecentHistory 按 id 倒序（新→旧），翻转为正序便于前端按时间顺序绘制。
	for i, j := 0, len(hist)-1; i < j; i, j = i+1, j-1 {
		hist[i], hist[j] = hist[j], hist[i]
	}
	c.JSON(http.StatusOK, ok(hist))
}

// batchDeletePayload 批量删除节点的请求体。
type batchDeletePayload struct {
	IDs []int64 `json:"ids" binding:"required"`
}

// batchDeleteProxy 批量删除节点（订阅导入大量节点时的清理场景）。
// 若删除的包含固定出口节点，自动取消指定，避免留下悬空引用（策略处理与单个删除一致）。
func (s *Services) batchDeleteProxy(c *gin.Context) {
	var payload batchDeletePayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, fail(400, err.Error()))
		return
	}
	if len(payload.IDs) == 0 {
		c.JSON(http.StatusBadRequest, fail(400, "ids 不能为空"))
		return
	}
	s.Pool.RemoveNodes(payload.IDs)
	if s.Selector != nil {
		for _, id := range payload.IDs {
			if s.Selector.PinnedID() == id {
				if s.Selector.Strategy() == scheduler.StrategyFixed {
					s.Selector.Unpin()
				} else {
					s.Selector.Pin(0)
				}
				_ = s.Store.SetSetting(config.KeyPinnedProxy, "")
				s.Bus.Info("deleted node was pinned, cleared exit pin")
				break
			}
		}
	}
	s.Bus.Info(fmt.Sprintf("batch deleted %d proxies", len(payload.IDs)))
	c.JSON(http.StatusOK, ok(gin.H{"deleted": len(payload.IDs)}))
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
	ID  *int64  `json:"id,omitempty"`
	IDs []int64 `json:"ids,omitempty"`
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
	if len(payload.IDs) > 0 {
		// 批量检测选中的节点：并发执行（sem 限流）+ 进度广播，异步避免阻塞请求。
		// 使用 Background 而非请求 context：handler 返回后请求 context 会被取消，导致检测中断。
		go func() {
			_ = s.Pool.CheckNodes(context.Background(), payload.IDs)
		}()
		c.JSON(http.StatusAccepted, ok(gin.H{"started": true, "total": len(payload.IDs)}))
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
