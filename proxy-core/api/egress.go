package api

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/axetroy/ProxyPilot/proxy-core/config"
	"github.com/axetroy/ProxyPilot/proxy-core/model"
	"github.com/axetroy/ProxyPilot/proxy-core/scheduler"
	"github.com/axetroy/ProxyPilot/proxy-core/validator"
)

// egressPayload 设置出口策略的请求体。
type egressPayload struct {
	// Strategy 目标策略（fixed / best / random / weighted / round-robin / chain / auto-chain）。
	Strategy string `json:"strategy" binding:"required"`
	// PinID 固定策略时可同时指定固定节点（>0 时生效）。
	PinID int64 `json:"pinId,omitempty"`
	// ChainHops 自动链路层数（auto-chain 策略时生效，1-8）。
	ChainHops int `json:"chainHops,omitempty"`
	// ChainSelection 自动链路每层选择策略（weighted / random / best）。
	ChainSelection string `json:"chainSelection,omitempty"`
}

// egressConfig 出口路由的当前状态（供前端页面展示）。
type egressConfig struct {
	Strategy   string                   `json:"strategy"`
	PinnedNode *model.ProxyNode         `json:"pinnedNode,omitempty"`
	AliveCount int                      `json:"aliveCount"`
	Strategies []scheduler.StrategyMeta `json:"strategies"`
	// 自动链路（auto-chain）配置：层数与每层选择策略。
	ChainHops      int    `json:"chainHops"`
	ChainSelection string `json:"chainSelection"`
}

// getEgress 返回当前出口策略、固定节点与可选策略列表。
func (s *Services) getEgress(c *gin.Context) {
	hops, selection := s.Cfg.ChainParams()
	cfg := egressConfig{
		Strategy:       string(s.Selector.Strategy()),
		AliveCount:     s.Pool.CountAlive(),
		Strategies:     scheduler.Strategies(),
		ChainHops:      hops,
		ChainSelection: selection,
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

	// 先解析并校验全部参数，全部通过后才应用（避免校验失败时策略已被切换）。
	// 自动链路策略：层数与每层选择策略（带默认值）。
	var hops int
	var selection string
	if strategy == scheduler.StrategyAutoChain {
		curHops, curSelection := s.Cfg.ChainParams()
		hops = payload.ChainHops
		if hops <= 0 {
			hops = curHops
			if hops <= 0 {
				hops = 2
			}
		}
		if hops > 8 {
			c.JSON(http.StatusBadRequest, fail(400, "自动链路层数不能超过 8"))
			return
		}
		selection = payload.ChainSelection
		if selection == "" {
			selection = curSelection
			if selection == "" {
				selection = "weighted"
			}
		}
		if !scheduler.ValidChainSelection(selection) {
			c.JSON(http.StatusBadRequest, fail(400, "未知的自动链路选择策略"))
			return
		}
	}

	s.Selector.SetStrategy(strategy)
	if err := s.Store.SetSetting(config.KeyEgressStrategy, payload.Strategy); err != nil {
		s.Bus.Error("persist egress strategy failed: " + err.Error())
	}

	if strategy == scheduler.StrategyAutoChain {
		// 自动链路策略：保存层数与每层选择策略。
		s.Cfg.SetChainParams(hops, selection)
		if err := s.Store.SetSetting(config.KeyChainHops, strconv.Itoa(hops)); err != nil {
			s.Bus.Error("persist chain hops failed: " + err.Error())
		}
		if err := s.Store.SetSetting(config.KeyChainSelection, selection); err != nil {
			s.Bus.Error("persist chain selection failed: " + err.Error())
		}
		s.Bus.Info(fmt.Sprintf("auto-chain strategy set to %d hops, selection=%s", hops, selection))
	} else if strategy == scheduler.StrategyFixed && payload.PinID > 0 {
		// 固定策略可同时指定固定节点（复用 pin 的持久化键）。
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
	respHops, respSelection := s.Cfg.ChainParams()
	c.JSON(http.StatusOK, ok(egressConfig{
		Strategy:       payload.Strategy,
		PinnedNode:     s.Selector.Pinned(),
		AliveCount:     s.Pool.CountAlive(),
		Strategies:     scheduler.Strategies(),
		ChainHops:      respHops,
		ChainSelection: respSelection,
	}))
}

// testAutoChain 测试自动链路：按当前配置（层数 + 每层选择策略）从存活节点中
// 挑选节点，逐跳测量延迟与连通性，返回与手动链路测试相同的结果结构。
func (s *Services) testAutoChain(c *gin.Context) {
	hops, selection := s.Cfg.ChainParams()
	if hops <= 0 {
		hops = 2
	}
	if hops > 8 {
		c.JSON(http.StatusBadRequest, fail(400, "自动链路层数不能超过 8"))
		return
	}
	if selection == "" {
		selection = "weighted"
	}
	if !scheduler.ValidChainSelection(selection) {
		c.JSON(http.StatusBadRequest, fail(400, "未知的自动链路选择策略"))
		return
	}

	nodes := s.Selector.SelectChain(hops, scheduler.Strategy(selection))
	if len(nodes) == 0 {
		c.JSON(http.StatusBadRequest, fail(400, "没有存活节点可供测试"))
		return
	}

	target, err := config.TargetHostPort(s.Cfg.CheckTarget)
	if err != nil {
		c.JSON(http.StatusBadRequest, fail(400, err.Error()))
		return
	}
	timeout := s.Cfg.CheckTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	result := validator.TestChain(nodes, target, timeout)
	c.JSON(http.StatusOK, ok(result))
}
