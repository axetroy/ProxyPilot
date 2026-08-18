package api

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/axetroy/ProxyPilot/proxy-core/config"
	"github.com/axetroy/ProxyPilot/proxy-core/model"
	"github.com/axetroy/ProxyPilot/proxy-core/scheduler"
)

// egressPayload 设置出口策略的请求体。
type egressPayload struct {
	// Strategy 目标策略（fixed / best / random / weighted / round-robin）。
	Strategy string `json:"strategy" binding:"required"`
	// PinID 固定策略时可同时指定固定节点（>0 时生效）。
	PinID int64 `json:"pinId,omitempty"`
}

// egressConfig 出口路由的当前状态（供前端页面展示）。
type egressConfig struct {
	Strategy   string                   `json:"strategy"`
	PinnedNode *model.ProxyNode         `json:"pinnedNode,omitempty"`
	AliveCount int                      `json:"aliveCount"`
	Strategies []scheduler.StrategyMeta `json:"strategies"`
}

// getEgress 返回当前出口策略、固定节点与可选策略列表。
func (s *Services) getEgress(c *gin.Context) {
	cfg := egressConfig{
		Strategy:   string(s.Selector.Strategy()),
		AliveCount: s.Pool.CountAlive(),
		Strategies: scheduler.Strategies(),
	}
	cfg.PinnedNode = s.Selector.Pinned()
	c.JSON(http.StatusOK, ok(cfg))
}

// updateEgress 切换出口策略并持久化；固定策略可同时指定节点。
func (s *Services) updateEgress(c *gin.Context) {
	var payload egressPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, fail(400, "请求体格式错误"))
		return
	}
	if !scheduler.ValidStrategy(payload.Strategy) {
		c.JSON(http.StatusBadRequest, fail(400, "未知的出口策略"))
		return
	}
	strategy := scheduler.Strategy(payload.Strategy)
	s.Selector.SetStrategy(strategy)
	if err := s.Store.SetSetting(config.KeyEgressStrategy, payload.Strategy); err != nil {
		s.Bus.Error("persist egress strategy failed: " + err.Error())
	}

	// 固定策略可同时指定固定节点（复用 pin 的持久化键）。
	if strategy == scheduler.StrategyFixed && payload.PinID > 0 {
		node := s.Pool.Get(payload.PinID)
		if node == nil {
			c.JSON(http.StatusNotFound, fail(404, "节点不存在"))
			return
		}
		s.Selector.Pin(payload.PinID)
		if err := s.Store.SetSetting(config.KeyPinnedProxy, strconv.FormatInt(payload.PinID, 10)); err != nil {
			s.Bus.Error("persist pinned proxy failed: " + err.Error())
		}
		s.Bus.Info(fmt.Sprintf("egress strategy set to fixed with node %s", node.Key()))
	} else {
		s.Bus.Info(fmt.Sprintf("egress strategy set to %s", payload.Strategy))
	}
	c.JSON(http.StatusOK, ok(egressConfig{
		Strategy:   payload.Strategy,
		PinnedNode: s.Selector.Pinned(),
		AliveCount: s.Pool.CountAlive(),
		Strategies: scheduler.Strategies(),
	}))
}
