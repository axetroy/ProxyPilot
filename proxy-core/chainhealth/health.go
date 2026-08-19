// Package chainhealth 链路自动健康管理：对启用的代理链路定时做整链探测，
// 连续失败达到阈值自动停用，避免失效链路长期占用 chain 策略出口。
package chainhealth

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/axetroy/ProxyPilot/proxy-core/bus"
	"github.com/axetroy/ProxyPilot/proxy-core/config"
	"github.com/axetroy/ProxyPilot/proxy-core/model"
	"github.com/axetroy/ProxyPilot/proxy-core/validator"
)

// NodeProvider 提供节点查询（*pool.Manager 实现）。
type NodeProvider interface {
	Get(id int64) *model.ProxyNode
}

// ChainStore 健康管理需要的最小存储接口（*storage.Store 实现）。
type ChainStore interface {
	ListChains() ([]model.ProxyChain, error)
	UpdateChainHealth(id int64, ok bool, latency int64, errMsg string, consecutive int) error
	SetChainAutoDisabled(id int64) error
}

// CheckFunc 一次链路的探测函数（生产默认 validator.TestChain，测试可注入避免真实网络）。
type CheckFunc func(nodes []*model.ProxyNode, target string, timeout time.Duration) model.ChainTestResult

// failThreshold 连续失败达到该次数自动停用链路，避免单次网络抖动误伤。
const failThreshold = 2

// Manager 链路自动健康管理。检测周期、检测目标与超时每次轮询实时读取
// Config，支持前端热更新；检测结果持久化在 proxy_chains 表并推送 bus 日志。
type Manager struct {
	store ChainStore
	nodes NodeProvider
	bus   *bus.Bus
	cfg   *config.Config
	check CheckFunc
}

// New 创建链路健康管理器。checkFunc 缺省使用真实整链探测。
func New(store ChainStore, nodes NodeProvider, b *bus.Bus, cfg *config.Config) *Manager {
	return &Manager{
		store: store,
		nodes: nodes,
		bus:   b,
		cfg:   cfg,
		check: validator.TestChain,
	}
}

// Start 启动周期检测循环，直到 ctx 取消。启动后立即执行一轮，尽早暴露失效链路。
// 检测周期每次循环实时读取 Config，支持前端热更新。
func (m *Manager) Start(ctx context.Context) {
	m.checkAll()
	for {
		interval := m.cfg.ChainCheckInterval
		if interval < time.Minute {
			interval = time.Minute
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			m.checkAll()
		}
	}
}

// CheckAll 对全部启用的链路执行一轮健康检测。
// 供测试与一次性手动触发使用；生产由 Start 的定时循环驱动。
func (m *Manager) CheckAll() { m.checkAll() }

func (m *Manager) checkAll() {
	chains, err := m.store.ListChains()
	if err != nil {
		m.bus.Debug(fmt.Sprintf("chain health: list chains: %v", err))
		return
	}
	// 链路数量少，并行探测避免多链串行时被单链超时拖慢整个周期。
	var wg sync.WaitGroup
	for i := range chains {
		if !chains[i].Enabled {
			continue
		}
		wg.Add(1)
		go func(c *model.ProxyChain) {
			defer wg.Done()
			m.checkOne(c)
		}(&chains[i])
	}
	wg.Wait()
}

func (m *Manager) checkOne(ch *model.ProxyChain) {
	nodes := make([]*model.ProxyNode, 0, len(ch.NodeIDs))
	for _, nid := range ch.NodeIDs {
		n := m.nodes.Get(nid)
		if n == nil {
			// 节点已被删除：链路校验本不允许删除被引用的节点，防御性处理。
			m.recordFailure(ch, fmt.Sprintf("链路节点 %d 不存在", nid))
			return
		}
		nodes = append(nodes, n)
	}
	target, err := config.TargetHostPort(m.cfg.CheckTarget)
	if err != nil {
		m.recordFailure(ch, err.Error())
		return
	}
	timeout := m.cfg.CheckTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	result := m.check(nodes, target, timeout)
	if result.OK {
		_ = m.store.UpdateChainHealth(ch.ID, true, result.TotalLatency, "", 0)
		return
	}
	m.recordFailure(ch, validator.ChainError(result))
}

// recordFailure 记录一次失败；连续失败达到阈值时自动停用链路。
func (m *Manager) recordFailure(ch *model.ProxyChain, msg string) {
	consecutive := ch.ConsecutiveFailures + 1
	if consecutive >= failThreshold {
		if err := m.store.SetChainAutoDisabled(ch.ID); err != nil {
			m.bus.Error(fmt.Sprintf("chain health: auto disable chain %q: %v", ch.Name, err))
			return
		}
		// 停用后连续失败计数归零，由用户手动启用时重新积累。
		_ = m.store.UpdateChainHealth(ch.ID, false, 0, msg, 0)
		m.bus.Error(fmt.Sprintf("链路 %q 连续 %d 次检测失败已自动停用：%s", ch.Name, consecutive, msg))
		return
	}
	_ = m.store.UpdateChainHealth(ch.ID, false, 0, msg, consecutive)
	m.bus.Warn(fmt.Sprintf("链路 %q 检测失败（第 %d 次）：%s", ch.Name, consecutive, msg))
}
