package pool

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/axetroy/ProxyPilot/proxy-core/bus"
	"github.com/axetroy/ProxyPilot/proxy-core/model"
	"github.com/axetroy/ProxyPilot/proxy-core/storage"
	"github.com/axetroy/ProxyPilot/proxy-core/validator"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	st, err := storage.New(":memory:")
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return NewManager(st, validator.NewChecker("", 0), bus.New(), 4)
}

type fakeChecker struct {
	calls int32
}

func (f *fakeChecker) Check(node *model.ProxyNode) (model.CheckResult, error) {
	atomic.AddInt32(&f.calls, 1)
	return model.CheckResult{OK: true, Latency: 1}, nil
}

func TestCheckNowIgnoresCanceledContext(t *testing.T) {
	st, err := storage.New(":memory:")
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	checker := &fakeChecker{}
	m := NewManager(st, checker, bus.New(), 4)
	if m.AddNodes([]*model.ProxyNode{{Host: "ok", Port: 8080, Protocol: model.ProtocolHTTP, Status: model.StatusAlive}}) != 1 {
		t.Fatal("failed to add node")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.CheckNow(ctx); err != nil {
		t.Fatalf("expected check to continue with background context, got %v", err)
	}
	if got := atomic.LoadInt32(&checker.calls); got != 1 {
		t.Fatalf("expected 1 check, got %d", got)
	}
}

func TestEliminateRemovesHighFailCount(t *testing.T) {
	m := newTestManager(t)

	good := &model.ProxyNode{Host: "good", Port: 8080, Protocol: model.ProtocolHTTP, Status: model.StatusAlive, FailCount: 1}
	bad := &model.ProxyNode{Host: "bad", Port: 8080, Protocol: model.ProtocolHTTP, Status: model.StatusDead, FailCount: 5}
	if m.AddNodes([]*model.ProxyNode{good, bad}) != 2 {
		t.Fatal("failed to add nodes")
	}

	if got := m.Eliminate(3); got != 1 {
		t.Fatalf("expected 1 eliminated, got %d", got)
	}
	if m.Count() != 1 {
		t.Fatalf("expected 1 node left, got %d", m.Count())
	}
	if m.List()[0].Host != "good" {
		t.Fatalf("expected good node to survive, got %+v", m.List()[0])
	}
}

func TestEliminateKeepsHealthyNodes(t *testing.T) {
	m := newTestManager(t)
	n := &model.ProxyNode{Host: "ok", Port: 8080, Protocol: model.ProtocolHTTP, Status: model.StatusAlive, FailCount: 2}
	if m.AddNodes([]*model.ProxyNode{n}) != 1 {
		t.Fatal("failed to add node")
	}
	if got := m.Eliminate(3); got != 0 {
		t.Fatalf("expected 0 eliminated, got %d", got)
	}
	if m.Count() != 1 {
		t.Fatalf("expected node to remain, got %d", m.Count())
	}
}

// ---------- 以下为补充测试 ----------

// mockChecker 默认成功，可通过 fail map 指定失败节点。
type mockChecker struct {
	fail map[int64]bool
}

func (m *mockChecker) Check(node *model.ProxyNode) (model.CheckResult, error) {
	if m.fail != nil && m.fail[node.ID] {
		return model.CheckResult{OK: false, Latency: 0, Error: "mock failure"}, nil
	}
	return model.CheckResult{OK: true, Latency: 50}, nil
}

func newTestManagerWithChecker(t *testing.T, checker nodeChecker) (*Manager, *storage.Store) {
	t.Helper()
	st, err := storage.New(":memory:")
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return NewManager(st, checker, bus.New(), 4), st
}

func newNode(host string, port int, proto model.ProxyProtocol) *model.ProxyNode {
	return &model.ProxyNode{
		Host:     host,
		Port:     port,
		Protocol: proto,
		Status:   model.StatusNew,
	}
}

func TestNewManagerConcurrencyDefault(t *testing.T) {
	st, err := storage.New(":memory:")
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	defer func() { _ = st.Close() }()
	m := NewManager(st, &mockChecker{}, bus.New(), 0)
	if m.concur != 1 {
		t.Fatalf("concurrency = %d, want 1", m.concur)
	}
	m = NewManager(st, &mockChecker{}, bus.New(), -3)
	if m.concur != 1 {
		t.Fatalf("concurrency = %d, want 1", m.concur)
	}
}

func TestAddNodesReturnsNewCount(t *testing.T) {
	m, _ := newTestManagerWithChecker(t, &mockChecker{})
	n1 := newNode("1.1.1.1", 80, model.ProtocolHTTP)
	n2 := newNode("2.2.2.2", 80, model.ProtocolHTTP)

	if added := m.AddNodes([]*model.ProxyNode{n1, n2}); added != 2 {
		t.Fatalf("expected 2 added, got %d", added)
	}
	if m.Count() != 2 {
		t.Fatalf("count = %d, want 2", m.Count())
	}

	// 重复添加同一节点不应新增
	dup := newNode("1.1.1.1", 80, model.ProtocolHTTP)
	if added := m.AddNodes([]*model.ProxyNode{dup}); added != 0 {
		t.Fatalf("expected 0 added for duplicate, got %d", added)
	}
	if m.Count() != 2 {
		t.Fatalf("count = %d, want 2 after duplicate", m.Count())
	}
}

func TestAddNodesAssignsIDs(t *testing.T) {
	m, _ := newTestManagerWithChecker(t, &mockChecker{})
	n := newNode("1.1.1.1", 80, model.ProtocolHTTP)
	m.AddNodes([]*model.ProxyNode{n})
	if n.ID == 0 {
		t.Fatal("expected node ID assigned after AddNodes")
	}
}

func TestListReturnsSnapshot(t *testing.T) {
	m, _ := newTestManagerWithChecker(t, &mockChecker{})
	n := newNode("1.1.1.1", 80, model.ProtocolHTTP)
	m.AddNodes([]*model.ProxyNode{n})

	list := m.List()
	if len(list) != 1 {
		t.Fatalf("len = %d, want 1", len(list))
	}
	// 修改快照不应影响内部状态
	list[0].Score = 999
	list[0].Status = model.StatusDead
	if got := m.List()[0]; got.Score != 0 || got.Status != model.StatusNew {
		t.Fatalf("snapshot mutation leaked: %+v", got)
	}
}

func TestAliveFilter(t *testing.T) {
	m, _ := newTestManagerWithChecker(t, &mockChecker{})
	alive := newNode("1.1.1.1", 80, model.ProtocolHTTP)
	alive.Status = model.StatusAlive
	dead := newNode("2.2.2.2", 80, model.ProtocolHTTP)
	dead.Status = model.StatusDead
	m.AddNodes([]*model.ProxyNode{alive, dead})

	if m.Count() != 2 {
		t.Fatalf("count = %d, want 2", m.Count())
	}
	if m.CountAlive() != 1 {
		t.Fatalf("countAlive = %d, want 1", m.CountAlive())
	}
	aliveList := m.Alive()
	if len(aliveList) != 1 || aliveList[0].Host != "1.1.1.1" {
		t.Fatalf("alive list = %+v", aliveList)
	}
}

func TestLoadFromStorage(t *testing.T) {
	m, st := newTestManagerWithChecker(t, &mockChecker{})
	n := newNode("1.1.1.1", 80, model.ProtocolHTTP)
	m.AddNodes([]*model.ProxyNode{n})

	// 新建一个 manager 从同一 storage 加载
	m2 := NewManager(st, &mockChecker{}, bus.New(), 1)
	if err := m2.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if m2.Count() != 1 {
		t.Fatalf("count = %d, want 1", m2.Count())
	}
	if got := m2.List()[0]; got.Host != "1.1.1.1" {
		t.Fatalf("loaded node = %+v", got)
	}
}

func TestRemove(t *testing.T) {
	m, _ := newTestManagerWithChecker(t, &mockChecker{})
	n := newNode("1.1.1.1", 80, model.ProtocolHTTP)
	m.AddNodes([]*model.ProxyNode{n})

	if err := m.Remove(n.ID); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if m.Count() != 0 {
		t.Fatalf("count = %d, want 0", m.Count())
	}
}

func TestRemoveNodes(t *testing.T) {
	m, _ := newTestManagerWithChecker(t, &mockChecker{})
	n1 := newNode("1.1.1.1", 80, model.ProtocolHTTP)
	n2 := newNode("2.2.2.2", 80, model.ProtocolHTTP)
	m.AddNodes([]*model.ProxyNode{n1, n2})

	m.RemoveNodes([]int64{n1.ID, n2.ID})
	if m.Count() != 0 {
		t.Fatalf("count = %d, want 0", m.Count())
	}
}

func TestPickReturnsAliveNode(t *testing.T) {
	m, _ := newTestManagerWithChecker(t, &mockChecker{})
	n := newNode("1.1.1.1", 80, model.ProtocolHTTP)
	n.Status = model.StatusAlive
	n.Score = 90
	n.Latency = 100
	m.AddNodes([]*model.ProxyNode{n})

	picked := m.Pick()
	if picked == nil || picked.ID != n.ID {
		t.Fatalf("pick = %+v, want node %d", picked, n.ID)
	}
}

func TestPickEmptyPool(t *testing.T) {
	m, _ := newTestManagerWithChecker(t, &mockChecker{})
	if picked := m.Pick(); picked != nil {
		t.Fatalf("expected nil pick, got %+v", picked)
	}
	if picked := m.PickRandom(); picked != nil {
		t.Fatalf("expected nil pickRandom, got %+v", picked)
	}
}

func TestPickRandom(t *testing.T) {
	m, _ := newTestManagerWithChecker(t, &mockChecker{})
	for i := 0; i < 5; i++ {
		n := newNode("1.1.1.1", 80+i, model.ProtocolHTTP)
		n.Status = model.StatusAlive
		m.AddNodes([]*model.ProxyNode{n})
	}
	picked := m.PickRandom()
	if picked == nil {
		t.Fatal("expected a node")
	}
	if picked.Status != model.StatusAlive {
		t.Fatalf("picked node not alive: %+v", picked)
	}
}

func TestCheckNodeSuccess(t *testing.T) {
	m, _ := newTestManagerWithChecker(t, &mockChecker{})
	n := newNode("1.1.1.1", 80, model.ProtocolHTTP)
	m.AddNodes([]*model.ProxyNode{n})

	result := m.CheckNode(n)
	if !result.OK {
		t.Fatalf("expected ok, got %+v", result)
	}
	got := m.List()[0]
	if got.Status != model.StatusAlive {
		t.Fatalf("status = %q, want alive", got.Status)
	}
	if got.SuccessCount != 1 {
		t.Fatalf("successCount = %d, want 1", got.SuccessCount)
	}
	if got.Score <= 0 {
		t.Fatalf("score = %d, want > 0", got.Score)
	}
	if got.LastCheck.IsZero() {
		t.Fatal("expected LastCheck set")
	}
}

func TestCheckNodeFailure(t *testing.T) {
	checker := &mockChecker{fail: map[int64]bool{}}
	m, _ := newTestManagerWithChecker(t, checker)
	n := newNode("1.1.1.1", 80, model.ProtocolHTTP)
	m.AddNodes([]*model.ProxyNode{n})
	checker.fail[n.ID] = true

	result := m.CheckNode(n)
	if result.OK {
		t.Fatalf("expected failure, got %+v", result)
	}
	got := m.List()[0]
	if got.Status != model.StatusDead {
		t.Fatalf("status = %q, want dead", got.Status)
	}
	if got.FailCount != 1 {
		t.Fatalf("failCount = %d, want 1", got.FailCount)
	}
}

func TestCheckNowAllAlive(t *testing.T) {
	m, _ := newTestManagerWithChecker(t, &mockChecker{})
	for i := 0; i < 5; i++ {
		n := newNode("1.1.1.1", 8080+i, model.ProtocolHTTP)
		m.AddNodes([]*model.ProxyNode{n})
	}

	if err := m.CheckNow(context.Background()); err != nil {
		t.Fatalf("checkNow: %v", err)
	}
	if m.CountAlive() != 5 {
		t.Fatalf("countAlive = %d, want 5", m.CountAlive())
	}
}

func TestCheckNowEmptyPool(t *testing.T) {
	m, _ := newTestManagerWithChecker(t, &mockChecker{})
	if err := m.CheckNow(context.Background()); err != nil {
		t.Fatalf("checkNow on empty pool: %v", err)
	}
}

func TestCheckNowPersistsHistory(t *testing.T) {
	m, st := newTestManagerWithChecker(t, &mockChecker{})
	n := newNode("1.1.1.1", 80, model.ProtocolHTTP)
	m.AddNodes([]*model.ProxyNode{n})

	if err := m.CheckNow(context.Background()); err != nil {
		t.Fatalf("checkNow: %v", err)
	}
	hist, err := st.RecentHistory(n.ID, 10)
	if err != nil {
		t.Fatalf("recentHistory: %v", err)
	}
	if len(hist) != 1 {
		t.Fatalf("history len = %d, want 1", len(hist))
	}
	if !hist[0].Success {
		t.Fatalf("history success = %v, want true", hist[0].Success)
	}
}

func TestCheckNowConcurrentGuard(t *testing.T) {
	m, _ := newTestManagerWithChecker(t, &mockChecker{})
	n := newNode("1.1.1.1", 80, model.ProtocolHTTP)
	m.AddNodes([]*model.ProxyNode{n})

	// 并发调用 CheckNow 不应报错（checking 标志会跳过重复执行）
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			done <- m.CheckNow(context.Background())
		}()
	}
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatalf("checkNow: %v", err)
		}
	}
}

func TestRefreshLoopStopsOnCancel(t *testing.T) {
	m, _ := newTestManagerWithChecker(t, &mockChecker{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		m.RefreshLoop(ctx, time.Millisecond)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RefreshLoop did not stop after cancel")
	}
}
