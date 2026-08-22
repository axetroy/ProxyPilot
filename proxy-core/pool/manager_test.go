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
	if got := m.concur.Load(); got != 1 {
		t.Fatalf("concurrency = %d, want 1", got)
	}
	m = NewManager(st, &mockChecker{}, bus.New(), -3)
	if got := m.concur.Load(); got != 1 {
		t.Fatalf("concurrency = %d, want 1", got)
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

func TestListSortedByScoreLatencyIDHost(t *testing.T) {
	m, _ := newTestManagerWithChecker(t, &mockChecker{})
	// 构造乱序节点：分数、延迟、ID、host 各不相同，验证完整排序链。
	nodes := []*model.ProxyNode{
		{Host: "z-host", Port: 80, Protocol: model.ProtocolHTTP, Score: 50, Latency: 100},
		{Host: "a-host", Port: 80, Protocol: model.ProtocolHTTP, Score: 90, Latency: 300},
		{Host: "b-host", Port: 80, Protocol: model.ProtocolHTTP, Score: 90, Latency: 100},
		{Host: "c-host", Port: 80, Protocol: model.ProtocolHTTP, Score: 90, Latency: 100},
	}
	if m.AddNodes(nodes) != 4 {
		t.Fatal("failed to add nodes")
	}

	list := m.List()
	if len(list) != 4 {
		t.Fatalf("len = %d, want 4", len(list))
	}
	// 期望顺序：b-host(90,100) → c-host(90,100) → a-host(90,300) → z-host(50,100)
	// b-host 与 c-host 分数延迟相同，按 ID 升序（先添加的 ID 小）。
	want := []string{"b-host", "c-host", "a-host", "z-host"}
	for i, w := range want {
		if list[i].Host != w {
			t.Fatalf("position %d: host = %s, want %s (full: %+v)", i, list[i].Host, w, list)
		}
	}
}

func TestListSortedAliveFirst(t *testing.T) {
	m, _ := newTestManagerWithChecker(t, &mockChecker{})
	// 存活节点分数低也应排在死亡/新节点之前。
	aliveLow := newNode("alive-low", 80, model.ProtocolHTTP)
	aliveLow.Status = model.StatusAlive
	aliveLow.Score = 10
	deadHigh := newNode("dead-high", 80, model.ProtocolHTTP)
	deadHigh.Status = model.StatusDead
	deadHigh.Score = 99
	newMid := newNode("new-mid", 80, model.ProtocolHTTP)
	newMid.Score = 50
	if m.AddNodes([]*model.ProxyNode{aliveLow, deadHigh, newMid}) != 3 {
		t.Fatal("failed to add nodes")
	}

	list := m.List()
	if len(list) != 3 {
		t.Fatalf("len = %d, want 3", len(list))
	}
	// 期望顺序：alive-low(存活) → new-mid(50) → dead-high(99)
	// 非存活状态按优先级 new < dead，dead 沉底。
	want := []string{"alive-low", "new-mid", "dead-high"}
	for i, w := range want {
		if list[i].Host != w {
			t.Fatalf("position %d: host = %s, want %s (full: %+v)", i, list[i].Host, w, list)
		}
	}
}

func TestListSortedStatusRankDeterministic(t *testing.T) {
	m, _ := newTestManagerWithChecker(t, &mockChecker{})
	// 非存活状态之间必须有明确顺序（checking < new < dead），
	// 否则 sort.Slice 不稳定排序会导致每次刷新顺序随机。
	dead := newNode("dead", 80, model.ProtocolHTTP)
	dead.Status = model.StatusDead
	dead.Score = 90
	newNode1 := newNode("new", 80, model.ProtocolHTTP)
	newNode1.Score = 90
	checking := newNode("checking", 80, model.ProtocolHTTP)
	checking.Status = model.StatusChecking
	checking.Score = 90
	if m.AddNodes([]*model.ProxyNode{dead, newNode1, checking}) != 3 {
		t.Fatal("failed to add nodes")
	}

	// 连续多次 List，顺序必须完全一致（确定性）。
	var prev []string
	for i := 0; i < 10; i++ {
		list := m.List()
		if len(list) != 3 {
			t.Fatalf("len = %d, want 3", len(list))
		}
		got := []string{list[0].Host, list[1].Host, list[2].Host}
		if prev == nil {
			prev = got
		} else {
			for j := range got {
				if got[j] != prev[j] {
					t.Fatalf("run %d: order changed: %v vs %v", i, got, prev)
				}
			}
		}
	}
	// 期望顺序：checking → new → dead（状态优先级，同分按 ID）。
	want := []string{"checking", "new", "dead"}
	for i, w := range want {
		if prev[i] != w {
			t.Fatalf("position %d: host = %s, want %s (full: %v)", i, prev[i], w, prev)
		}
	}
}

func TestAliveSortedByScoreLatencyIDHost(t *testing.T) {
	m, _ := newTestManagerWithChecker(t, &mockChecker{})
	aliveLow := newNode("low", 80, model.ProtocolHTTP)
	aliveLow.Status = model.StatusAlive
	aliveLow.Score = 30
	aliveLow.Latency = 50
	aliveHigh := newNode("high", 80, model.ProtocolHTTP)
	aliveHigh.Status = model.StatusAlive
	aliveHigh.Score = 90
	aliveHigh.Latency = 200
	dead := newNode("dead", 80, model.ProtocolHTTP)
	dead.Status = model.StatusDead
	m.AddNodes([]*model.ProxyNode{aliveLow, aliveHigh, dead})

	alive := m.Alive()
	if len(alive) != 2 {
		t.Fatalf("alive len = %d, want 2", len(alive))
	}
	if alive[0].Host != "high" || alive[1].Host != "low" {
		t.Fatalf("unexpected alive order: %+v", alive)
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

func TestCheckNodesSubset(t *testing.T) {
	m, _ := newTestManagerWithChecker(t, &mockChecker{})
	for i := 0; i < 5; i++ {
		n := newNode("1.1.1.1", 8080+i, model.ProtocolHTTP)
		m.AddNodes([]*model.ProxyNode{n})
	}
	nodes := m.List()
	targets := []int64{nodes[0].ID, nodes[1].ID, 99999}

	if err := m.CheckNodes(context.Background(), targets); err != nil {
		t.Fatalf("checkNodes: %v", err)
	}
	// 不存在的 id 被跳过，选中的节点应全部存活
	aliveCount := 0
	for _, n := range m.List() {
		if n.ID == nodes[0].ID || n.ID == nodes[1].ID {
			if n.Status != model.StatusAlive {
				t.Errorf("node %d status = %s, want alive", n.ID, n.Status)
			}
			aliveCount++
		}
	}
	if aliveCount != 2 {
		t.Errorf("checked nodes = %d, want 2", aliveCount)
	}
}

func TestCheckNodesEmptyIDs(t *testing.T) {
	m, _ := newTestManagerWithChecker(t, &mockChecker{})
	if err := m.CheckNodes(context.Background(), nil); err != nil {
		t.Fatalf("checkNodes with empty ids: %v", err)
	}
}

func TestRefreshLoopStopsOnCancel(t *testing.T) {
	m, _ := newTestManagerWithChecker(t, &mockChecker{})
	m.SetRefreshInterval(time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		m.RefreshLoop(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RefreshLoop did not stop after cancel")
	}
}

// SetChecker 热更新检测器：替换后 CheckNode 使用新检测器。
func TestSetChecker(t *testing.T) {
	m, st := newTestManagerWithChecker(t, &mockChecker{})
	m.AddNodes([]*model.ProxyNode{newNode("1.2.3.4", 8080, model.ProtocolHTTP)})
	nodes := m.List()
	id := nodes[0].ID

	// 默认 checker 返回 OK
	if r := m.CheckNode(nodes[0]); !r.OK {
		t.Errorf("default checker result OK = %v, want true", r.OK)
	}

	// 替换为总是失败的 checker
	m.SetChecker(&mockChecker{fail: map[int64]bool{id: true}})
	if r := m.CheckNode(m.List()[0]); r.OK {
		t.Errorf("after SetChecker result OK = %v, want false", r.OK)
	}
	_ = st
}

// SetConcurrency 热更新并发数：非法值回退为 1。
func TestSetConcurrency(t *testing.T) {
	m, _ := newTestManagerWithChecker(t, &mockChecker{})
	m.SetConcurrency(8)
	if got := m.concur.Load(); got != 8 {
		t.Errorf("concurrency = %d, want 8", got)
	}
	m.SetConcurrency(0)
	if got := m.concur.Load(); got != 1 {
		t.Errorf("concurrency after 0 = %d, want 1", got)
	}
	m.SetConcurrency(-3)
	if got := m.concur.Load(); got != 1 {
		t.Errorf("concurrency after negative = %d, want 1", got)
	}
}

// geoChecker 返回带出口 IP 的探测结果，用于验证地区解析。
type geoChecker struct{ proxiedIP string }

func (c *geoChecker) Check(node *model.ProxyNode) (model.CheckResult, error) {
	return model.CheckResult{
		OK: true, Latency: 50,
		Safety: &model.SafetyProbe{ProxiedIP: c.proxiedIP},
	}, nil
}

// 检测完成后，节点应填充离线 GeoIP 解析的地区（优先使用连接安全探测到的出口 IP）。
func TestCheckNodeFillsGeoLocation(t *testing.T) {
	m, _ := newTestManagerWithChecker(t, &geoChecker{proxiedIP: "8.8.8.8"})
	m.AddNodes([]*model.ProxyNode{newNode("dummy.example.com", 443, model.ProtocolHTTP)})
	ns := m.List()
	if len(ns) != 1 {
		t.Fatalf("nodes = %d, want 1", len(ns))
	}

	m.CheckNode(ns[0])

	got := m.Get(ns[0].ID)
	if got == nil {
		t.Fatal("node not found after check")
	}
	if got.Country != "United States" {
		t.Errorf("country = %q, want United States (from exit ip)", got.Country)
	}
	// host 是域名但出口 IP 命中，不应依赖 DNS。
	if got.Country == "" {
		t.Errorf("geo not resolved: %+v", got)
	}
}

// 无出口 IP 时，回退到节点 host 解析（host 为公网 IP 时纯本地查询）。
func TestCheckNodeFillsGeoFromHost(t *testing.T) {
	m, _ := newTestManagerWithChecker(t, &mockChecker{})
	m.AddNodes([]*model.ProxyNode{newNode("114.114.114.114", 80, model.ProtocolHTTP)})
	ns := m.List()

	m.CheckNode(ns[0])
	got := m.Get(ns[0].ID)
	if got.Country != "中国" {
		t.Errorf("country = %q, want 中国 (from host ip)", got.Country)
	}
	// 内网 host 解析为 Reserved，展示层可见但非空。
	m2, _ := newTestManagerWithChecker(t, &mockChecker{})
	m2.AddNodes([]*model.ProxyNode{newNode("127.0.0.1", 80, model.ProtocolHTTP)})
	ns = m2.List()
	m2.CheckNode(ns[0])
	if got := m2.Get(ns[0].ID); got.Country != "Reserved" {
		t.Errorf("country = %q, want Reserved for private host", got.Country)
	}
}

func TestPickBest(t *testing.T) {
	m, _ := newTestManagerWithChecker(t, &mockChecker{})
	// 添加三个节点：分数不同
	m.AddNodes([]*model.ProxyNode{
		{Host: "a.com", Port: 80, Protocol: model.ProtocolHTTP, Score: 50, Latency: 100, Status: model.StatusAlive},
		{Host: "b.com", Port: 80, Protocol: model.ProtocolHTTP, Score: 100, Latency: 200, Status: model.StatusAlive},
		{Host: "c.com", Port: 80, Protocol: model.ProtocolHTTP, Score: 100, Latency: 50, Status: model.StatusAlive}, // 最优：分数高、延迟低
		{Host: "d.com", Port: 80, Protocol: model.ProtocolHTTP, Score: 80, Latency: 10, Status: model.StatusDead},  // dead，应被忽略
	})

	best := m.PickBest()
	if best == nil {
		t.Fatal("PickBest returned nil")
	}
	if best.Host != "c.com" {
		t.Errorf("PickBest = %s, want c.com (highest score, lowest latency)", best.Host)
	}
	if best.Status != model.StatusAlive {
		t.Errorf("PickBest status = %v, want alive", best.Status)
	}

	// 无存活节点时返回 nil
	m2, _ := newTestManagerWithChecker(t, &mockChecker{})
	m2.AddNodes([]*model.ProxyNode{
		{Host: "x.com", Port: 80, Protocol: model.ProtocolHTTP, Score: 100, Latency: 100, Status: model.StatusDead},
	})
	if m2.PickBest() != nil {
		t.Error("PickBest should return nil when no alive nodes")
	}
}

func TestPickRandom(t *testing.T) {
	m, _ := newTestManagerWithChecker(t, &mockChecker{})
	m.AddNodes([]*model.ProxyNode{
		{Host: "a.com", Port: 80, Protocol: model.ProtocolHTTP, Score: 100, Latency: 100, Status: model.StatusAlive},
		{Host: "b.com", Port: 80, Protocol: model.ProtocolHTTP, Score: 100, Latency: 100, Status: model.StatusAlive},
		{Host: "c.com", Port: 80, Protocol: model.ProtocolHTTP, Score: 100, Latency: 100, Status: model.StatusDead}, // dead
	})

	// 多次调用，应该只返回 a.com 或 b.com（存活节点），且最终都能被选中
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		n := m.PickRandom()
		if n == nil {
			t.Fatal("PickRandom returned nil")
		}
		if n.Status != model.StatusAlive {
			t.Errorf("PickRandom returned dead node: %s", n.Host)
		}
		seen[n.Host] = true
	}
	if len(seen) != 2 {
		t.Errorf("PickRandom only saw hosts %v, want both a.com and b.com", seen)
	}

	// 无存活节点时返回 nil
	m2, _ := newTestManagerWithChecker(t, &mockChecker{})
	m2.AddNodes([]*model.ProxyNode{
		{Host: "x.com", Port: 80, Protocol: model.ProtocolHTTP, Score: 100, Latency: 100, Status: model.StatusDead},
	})
	if m2.PickRandom() != nil {
		t.Error("PickRandom should return nil when no alive nodes")
	}
}

func TestPickRoundRobin(t *testing.T) {
	m, _ := newTestManagerWithChecker(t, &mockChecker{})
	m.AddNodes([]*model.ProxyNode{
		{Host: "a.com", Port: 80, Protocol: model.ProtocolHTTP, Score: 100, Latency: 100, Status: model.StatusAlive},
		{Host: "b.com", Port: 80, Protocol: model.ProtocolHTTP, Score: 100, Latency: 100, Status: model.StatusAlive},
		{Host: "c.com", Port: 80, Protocol: model.ProtocolHTTP, Score: 100, Latency: 100, Status: model.StatusDead},
	})

	seen := make(map[string]bool)
	for i := 0; i < 30; i++ {
		n := m.PickRoundRobin()
		if n == nil {
			t.Fatal("PickRoundRobin returned nil")
		}
		if n.Status != model.StatusAlive {
			t.Errorf("PickRoundRobin returned dead node: %s", n.Host)
		}
		seen[n.Host] = true
	}
	if len(seen) != 2 {
		t.Errorf("PickRoundRobin only saw hosts %v, want both a.com and b.com", seen)
	}

	// 无存活节点时返回 nil
	m3, _ := newTestManagerWithChecker(t, &mockChecker{})
	m3.AddNodes([]*model.ProxyNode{
		{Host: "x.com", Port: 80, Protocol: model.ProtocolHTTP, Score: 100, Latency: 100, Status: model.StatusDead},
	})
	if m3.PickRoundRobin() != nil {
		t.Error("PickRoundRobin should return nil when no alive nodes")
	}
}

func TestPickByProtocol(t *testing.T) {
	m, _ := newTestManagerWithChecker(t, &mockChecker{})
	m.AddNodes([]*model.ProxyNode{
		{Host: "socks5-a", Port: 80, Protocol: model.ProtocolSOCKS5, Score: 50, Latency: 100, Status: model.StatusAlive},
		{Host: "socks5-b", Port: 80, Protocol: model.ProtocolSOCKS5, Score: 100, Latency: 50, Status: model.StatusAlive}, // 最优
		{Host: "http-a", Port: 80, Protocol: model.ProtocolHTTP, Score: 80, Latency: 100, Status: model.StatusAlive},
		{Host: "https-a", Port: 80, Protocol: model.ProtocolHTTPS, Score: 90, Latency: 200, Status: model.StatusAlive},
		{Host: "dead-socks5", Port: 80, Protocol: model.ProtocolSOCKS5, Score: 100, Latency: 10, Status: model.StatusDead},
	})

	// SOCKS5 只匹配 SOCKS5
	socks5Best := m.PickByProtocol(model.ProtocolSOCKS5)
	if socks5Best == nil {
		t.Fatal("PickByProtocol(SOCKS5) returned nil")
	}
	if socks5Best.Host != "socks5-b" {
		t.Errorf("PickByProtocol(SOCKS5) = %s, want socks5-b", socks5Best.Host)
	}

	// HTTP/HTTPS 匹配两者
	httpBest := m.PickByProtocol(model.ProtocolHTTP)
	if httpBest == nil {
		t.Fatal("PickByProtocol(HTTP) returned nil")
	}
	if httpBest.Host != "https-a" {
		t.Errorf("PickByProtocol(HTTP) = %s, want https-a (score 90 > 80)", httpBest.Host)
	}

	httpsBest := m.PickByProtocol(model.ProtocolHTTPS)
	if httpsBest == nil {
		t.Fatal("PickByProtocol(HTTPS) returned nil")
	}
	if httpsBest.Host != "https-a" {
		t.Errorf("PickByProtocol(HTTPS) = %s, want https-a", httpsBest.Host)
	}

	// 空协议 = 不筛选，返回全局最优
	anyBest := m.PickByProtocol("")
	if anyBest == nil {
		t.Fatal("PickByProtocol('') returned nil")
	}
	if anyBest.Host != "socks5-b" {
		t.Errorf("PickByProtocol('') = %s, want socks5-b (global best)", anyBest.Host)
	}

	// 无匹配协议时返回 nil
	m2, _ := newTestManagerWithChecker(t, &mockChecker{})
	m2.AddNodes([]*model.ProxyNode{
		{Host: "http-only", Port: 80, Protocol: model.ProtocolHTTP, Score: 100, Latency: 100, Status: model.StatusAlive},
	})
	if m2.PickByProtocol(model.ProtocolSOCKS5) != nil {
		t.Error("PickByProtocol(SOCKS5) should return nil when only HTTP nodes exist")
	}
}

func TestPickBestNotIn(t *testing.T) {
	m, _ := newTestManagerWithChecker(t, &mockChecker{})
	m.AddNodes([]*model.ProxyNode{
		{Host: "a.com", Port: 80, Protocol: model.ProtocolHTTP, Score: 100, Latency: 100, Status: model.StatusAlive},
		{Host: "b.com", Port: 80, Protocol: model.ProtocolHTTP, Score: 90, Latency: 100, Status: model.StatusAlive},
		{Host: "c.com", Port: 80, Protocol: model.ProtocolHTTP, Score: 80, Latency: 100, Status: model.StatusAlive},
	})

	picked := map[int64]bool{}
	// 第一次应选 a.com (score 100)
	n1 := m.PickBestNotIn(picked)
	if n1 == nil || n1.Host != "a.com" {
		t.Errorf("first PickBestNotIn = %v, want a.com", n1)
	}
	picked[n1.ID] = true

	// 第二次应选 b.com (score 90)
	n2 := m.PickBestNotIn(picked)
	if n2 == nil || n2.Host != "b.com" {
		t.Errorf("second PickBestNotIn = %v, want b.com", n2)
	}
	picked[n2.ID] = true

	// 第三次应选 c.com (score 80)
	n3 := m.PickBestNotIn(picked)
	if n3 == nil || n3.Host != "c.com" {
		t.Errorf("third PickBestNotIn = %v, want c.com", n3)
	}
	picked[n3.ID] = true

	// 第四次应返回 nil
	if m.PickBestNotIn(picked) != nil {
		t.Error("PickBestNotIn should return nil when all picked")
	}
}

func TestPickRandomNotIn(t *testing.T) {
	m, _ := newTestManagerWithChecker(t, &mockChecker{})
	m.AddNodes([]*model.ProxyNode{
		{Host: "a.com", Port: 80, Protocol: model.ProtocolHTTP, Score: 100, Latency: 100, Status: model.StatusAlive},
		{Host: "b.com", Port: 80, Protocol: model.ProtocolHTTP, Score: 100, Latency: 100, Status: model.StatusAlive},
		{Host: "c.com", Port: 80, Protocol: model.ProtocolHTTP, Score: 100, Latency: 100, Status: model.StatusDead},
	})

	picked := map[int64]bool{}
	seen := make(map[string]bool)
	for i := 0; i < 50; i++ {
		n := m.PickRandomNotIn(picked)
		if n == nil {
			t.Fatal("PickRandomNotIn returned nil unexpectedly")
		}
		if n.Status != model.StatusAlive {
			t.Errorf("PickRandomNotIn returned dead node: %s", n.Host)
		}
		seen[n.Host] = true
	}
	if len(seen) != 2 {
		t.Errorf("PickRandomNotIn only saw hosts %v, want both a.com and b.com", seen)
	}

	// 选过 a.com 后再排除
	var aID int64
	for _, n := range m.List() {
		if n.Host == "a.com" {
			aID = n.ID
			break
		}
	}
	if aID == 0 {
		t.Fatal("a.com not found")
	}
	picked[aID] = true
	seen = make(map[string]bool)
	for i := 0; i < 20; i++ {
		n := m.PickRandomNotIn(picked)
		if n == nil {
			t.Fatal("PickRandomNotIn returned nil after picking one")
		}
		seen[n.Host] = true
	}
	if len(seen) != 1 || !seen["b.com"] {
		t.Errorf("PickRandomNotIn after picking a.com = %v, want only b.com", seen)
	}
}

func TestPickWeightedNotIn(t *testing.T) {
	m, _ := newTestManagerWithChecker(t, &mockChecker{})
	m.AddNodes([]*model.ProxyNode{
		{Host: "high-score", Port: 80, Protocol: model.ProtocolHTTP, Score: 100, Latency: 100, Status: model.StatusAlive},
		{Host: "low-latency", Port: 80, Protocol: model.ProtocolHTTP, Score: 50, Latency: 10, Status: model.StatusAlive}, // weight = 5
		{Host: "dead", Port: 80, Protocol: model.ProtocolHTTP, Score: 100, Latency: 100, Status: model.StatusDead},
	})

	picked := map[int64]bool{}
	// 高分低延迟权重高，应更容易被选中
	counts := make(map[string]int)
	for i := 0; i < 200; i++ {
		n := m.PickWeightedNotIn(picked)
		if n == nil {
			t.Fatal("PickWeightedNotIn returned nil")
		}
		counts[n.Host]++
	}
	if counts["high-score"] == 0 || counts["low-latency"] == 0 {
		t.Errorf("PickWeightedNotIn missed nodes: %v", counts)
	}
	if counts["dead"] > 0 {
		t.Error("PickWeightedNotIn should not pick dead nodes")
	}

	// 全部 picked 后返回 nil
	var hsID, llID int64
	for _, n := range m.List() {
		switch n.Host {
		case "high-score":
			hsID = n.ID
		case "low-latency":
			llID = n.ID
		}
	}
	picked[hsID] = true
	picked[llID] = true
	if m.PickWeightedNotIn(picked) != nil {
		t.Error("PickWeightedNotIn should return nil when all picked")
	}
}

func TestAliveIDs(t *testing.T) {
	m, _ := newTestManagerWithChecker(t, &mockChecker{})
	m.AddNodes([]*model.ProxyNode{
		{Host: "a.com", Port: 80, Protocol: model.ProtocolHTTP, Score: 100, Latency: 100, Status: model.StatusAlive},
		{Host: "b.com", Port: 80, Protocol: model.ProtocolHTTP, Score: 100, Latency: 100, Status: model.StatusDead},
		{Host: "c.com", Port: 80, Protocol: model.ProtocolHTTP, Score: 100, Latency: 100, Status: model.StatusAlive},
	})

	ids := m.AliveIDs()
	if len(ids) != 2 {
		t.Errorf("AliveIDs = %d, want 2", len(ids))
	}
	found := make(map[int64]bool)
	for _, id := range ids {
		found[id] = true
	}
	// 验证只有存活节点的 ID
	all := m.List()
	for _, n := range all {
		if n.Status == model.StatusAlive && !found[n.ID] {
			t.Errorf("AliveIDs missing alive node %d", n.ID)
		}
		if n.Status != model.StatusAlive && found[n.ID] {
			t.Errorf("AliveIDs should not include dead node %d", n.ID)
		}
	}
}
