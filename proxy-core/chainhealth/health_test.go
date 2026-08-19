package chainhealth

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/axetroy/ProxyPilot/proxy-core/bus"
	"github.com/axetroy/ProxyPilot/proxy-core/config"
	"github.com/axetroy/ProxyPilot/proxy-core/model"
)

// mockChainStore 内存版 ChainStore：ListChains 返回的连续失败次数随健康更新变化，
// 贴近真实存储行为，便于测试跨轮次的阈值累积。
type mockChainStore struct {
	chains   []model.ProxyChain
	disabled map[int64]bool
	health   map[int64]healthCall
	mu       sync.Mutex
}

type healthCall struct {
	ok          bool
	latency     int64
	errMsg      string
	consecutive int
}

func newMockChainStore(chains []model.ProxyChain) *mockChainStore {
	return &mockChainStore{
		chains:   chains,
		disabled: map[int64]bool{},
		health:   map[int64]healthCall{},
	}
}

func (m *mockChainStore) ListChains() ([]model.ProxyChain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]model.ProxyChain, len(m.chains))
	copy(out, m.chains)
	return out, nil
}

func (m *mockChainStore) UpdateChainHealth(id int64, ok bool, latency int64, errMsg string, consecutive int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.health[id] = healthCall{ok: ok, latency: latency, errMsg: errMsg, consecutive: consecutive}
	for i := range m.chains {
		if m.chains[i].ID == id {
			m.chains[i].LastOK = ok
			m.chains[i].ConsecutiveFailures = consecutive
		}
	}
	return nil
}

func (m *mockChainStore) SetChainAutoDisabled(id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.disabled[id] = true
	for i := range m.chains {
		if m.chains[i].ID == id {
			m.chains[i].Enabled = false
			m.chains[i].AutoDisabled = true
		}
	}
	return nil
}

type mockNodes map[int64]*model.ProxyNode

func (m mockNodes) Get(id int64) *model.ProxyNode { return m[id] }

func newTestManager(store ChainStore, nodes NodeProvider, check CheckFunc) *Manager {
	cfg := config.New()
	cfg.ChainCheckInterval = time.Minute
	cfg.CheckTarget = "https://www.apple.com/library/test/success.html"
	cfg.CheckTimeout = time.Second
	return &Manager{store: store, nodes: nodes, bus: bus.New(), cfg: cfg, check: check}
}

func chainNode(id int64) *model.ProxyNode {
	return &model.ProxyNode{ID: id, Protocol: model.ProtocolSOCKS5, Host: "127.0.0.1", Port: 1080}
}

func okResult() model.ChainTestResult {
	return model.ChainTestResult{OK: true, TotalLatency: 42, Hops: []model.ChainHopResult{{Hop: 1, OK: true, Latency: 42}}}
}

func failResult() model.ChainTestResult {
	return model.ChainTestResult{OK: false, Hops: []model.ChainHopResult{{Hop: 1, Error: "dial timeout"}}}
}

// TestCheckAllHealthy 探测通过时记录健康状态且不触发停用。
func TestCheckAllHealthy(t *testing.T) {
	store := newMockChainStore([]model.ProxyChain{{ID: 1, Name: "c1", NodeIDs: []int64{10}, Enabled: true}})
	mgr := newTestManager(store, mockNodes{10: chainNode(10)}, func(_ []*model.ProxyNode, _ string, _ time.Duration) model.ChainTestResult {
		return okResult()
	})
	mgr.CheckAll()

	h := store.health[1]
	if !h.ok {
		t.Errorf("health = %+v, want ok", h)
	}
	if h.latency != 42 {
		t.Errorf("latency = %d, want 42", h.latency)
	}
	if h.consecutive != 0 {
		t.Errorf("consecutive = %d, want 0", h.consecutive)
	}
	if store.disabled[1] {
		t.Errorf("chain 1 should not be auto-disabled")
	}
}

// TestCheckAllAutoDisableAfterThreshold 连续失败达阈值（2 次）自动停用；
// 第一次失败只记录并累加，停用后连续计数归零。
func TestCheckAllAutoDisableAfterThreshold(t *testing.T) {
	store := newMockChainStore([]model.ProxyChain{{ID: 1, Name: "c1", NodeIDs: []int64{10}, Enabled: true}})
	mgr := newTestManager(store, mockNodes{10: chainNode(10)}, func(_ []*model.ProxyNode, _ string, _ time.Duration) model.ChainTestResult {
		return failResult()
	})

	mgr.CheckAll()
	if store.disabled[1] {
		t.Fatalf("chain 1 disabled after single failure, want threshold 2")
	}
	if h := store.health[1]; h.consecutive != 1 || h.ok {
		t.Errorf("health = %+v, want consecutive 1 and not ok", h)
	}
	if chains, _ := store.ListChains(); chains[0].Enabled == false {
		t.Errorf("chain still enabled after 1 failure")
	}

	mgr.CheckAll()
	if !store.disabled[1] {
		t.Fatalf("chain 1 not auto-disabled after 2 consecutive failures")
	}
	if h := store.health[1]; h.consecutive != 0 {
		t.Errorf("health = %+v, want consecutive reset to 0 after disable", h)
	}
}

// TestCheckAllSkipsDisabled 已停用（手动或自动）的链路不参与检测。
func TestCheckAllSkipsDisabled(t *testing.T) {
	store := newMockChainStore([]model.ProxyChain{
		{ID: 1, Name: "off", NodeIDs: []int64{10}, Enabled: false},
		{ID: 2, Name: "on", NodeIDs: []int64{11}, Enabled: true},
	})
	var checked []int64
	var mu sync.Mutex
	mgr := newTestManager(store, mockNodes{10: chainNode(10), 11: chainNode(11)}, func(nodes []*model.ProxyNode, _ string, _ time.Duration) model.ChainTestResult {
		mu.Lock()
		checked = append(checked, nodes[0].ID)
		mu.Unlock()
		return okResult()
	})
	mgr.CheckAll()

	if len(checked) != 1 || checked[0] != 11 {
		t.Errorf("checked node ids = %v, want only [11] (enabled chain)", checked)
	}
}

// TestCheckAllMissingNode 链路引用的节点不存在时按失败处理。
func TestCheckAllMissingNode(t *testing.T) {
	store := newMockChainStore([]model.ProxyChain{{ID: 1, Name: "c1", NodeIDs: []int64{99}, Enabled: true}})
	// 空节点表：节点 99 不存在，探测函数不会被执行。
	mgr := newTestManager(store, mockNodes{}, func(_ []*model.ProxyNode, _ string, _ time.Duration) model.ChainTestResult {
		return failResult()
	})
	mgr.CheckAll()

	if h := store.health[1]; h.ok {
		t.Errorf("health = %+v, want failure for missing node", h)
	}
}

// TestManagerStartContextCancel 定时循环可被 ctx 取消退出。
func TestManagerStartContextCancel(t *testing.T) {
	store := newMockChainStore([]model.ProxyChain{{ID: 1, Name: "c1", NodeIDs: []int64{10}, Enabled: true}})
	mgr := newTestManager(store, mockNodes{10: chainNode(10)}, func(_ []*model.ProxyNode, _ string, _ time.Duration) model.ChainTestResult {
		return okResult()
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		mgr.Start(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not exit after context cancel")
	}
}

// TestManagerIntervalHotReload 修改 ChainCheckInterval 后，
// 下一次等待周期应读取新值（支持前端热更新），而不是一直用 Start 时的旧周期。
func TestManagerIntervalHotReload(t *testing.T) {
	store := newMockChainStore([]model.ProxyChain{{ID: 1, Name: "c1", NodeIDs: []int64{10}, Enabled: true}})
	mgr := newTestManager(store, mockNodes{10: chainNode(10)}, func(_ []*model.ProxyNode, _ string, _ time.Duration) model.ChainTestResult {
		return okResult()
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 初始周期 1 分钟；改小后应在短时间触发下一轮（验证实时读取而非缓存旧值）。
	mgr.cfg.ChainCheckInterval = 60 * time.Millisecond
	go mgr.Start(ctx)

	deadline := time.Now().Add(3 * time.Second)
	for {
		store.mu.Lock()
		hasHealth := store.health[1].ok
		store.mu.Unlock()
		if hasHealth {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("health check did not run after interval hot reload")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
