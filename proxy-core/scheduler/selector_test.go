package scheduler

import (
	"testing"
	"time"

	"github.com/axetroy/ProxyPilot/proxy-core/bus"
	"github.com/axetroy/ProxyPilot/proxy-core/model"
	"github.com/axetroy/ProxyPilot/proxy-core/pool"
	"github.com/axetroy/ProxyPilot/proxy-core/storage"
)

// mockChecker 默认返回成功，可通过 fail map 指定某些节点失败。
type mockChecker struct {
	fail map[int64]bool
}

func (m *mockChecker) Check(node *model.ProxyNode) (model.CheckResult, error) {
	if m.fail != nil && m.fail[node.ID] {
		return model.CheckResult{OK: false, Latency: 0, Error: "mock failure"}, nil
	}
	return model.CheckResult{OK: true, Latency: 100}, nil
}

func newTestPool(t *testing.T) *pool.Manager {
	t.Helper()
	return newTestPoolWithChecker(t, &mockChecker{})
}

func newTestPoolWithChecker(t *testing.T, checker *mockChecker) *pool.Manager {
	t.Helper()
	st, err := storage.New(":memory:")
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return pool.NewManager(st, checker, bus.New(), 4)
}

func addAlive(t *testing.T, m *pool.Manager, host string, score int, latency int64) *model.ProxyNode {
	return addAliveWithProtocol(t, m, host, score, latency, model.ProtocolHTTP)
}

func addAliveWithProtocol(t *testing.T, m *pool.Manager, host string, score int, latency int64, protocol model.ProxyProtocol) *model.ProxyNode {
	t.Helper()
	n := &model.ProxyNode{
		Host:     host,
		Port:     8080,
		Protocol: protocol,
		Score:    score,
		Latency:  latency,
		Status:   model.StatusAlive,
	}
	if m.AddNodes([]*model.ProxyNode{n}) != 1 {
		t.Fatalf("failed to add node %s", host)
	}
	return n
}

func TestNextPicksHighestWeight(t *testing.T) {
	m := newTestPool(t)
	a := addAlive(t, m, "a", 90, 100)
	addAlive(t, m, "b", 50, 100)

	s := NewSelector(m)
	got := s.Next()
	if got == nil || got.ID != a.ID {
		t.Fatalf("expected node a (id=%d), got %+v", a.ID, got)
	}
}

func TestFailOnPenalizesNode(t *testing.T) {
	m := newTestPool(t)
	a := addAlive(t, m, "a", 90, 100)
	b := addAlive(t, m, "b", 50, 100)

	s := NewSelector(m)
	if got := s.Next(); got == nil || got.ID != a.ID {
		t.Fatalf("expected node a first, got %+v", got)
	}

	// a fails twice -> weight halves per failure, b becomes preferred.
	s.FailOn(a.ID)
	s.FailOn(a.ID)

	got := s.Next()
	if got == nil || got.ID != b.ID {
		t.Fatalf("expected node b after penalizing a, got %+v", got)
	}
}

func TestSuccessClearsFailure(t *testing.T) {
	m := newTestPool(t)
	a := addAlive(t, m, "a", 90, 100)
	addAlive(t, m, "b", 50, 100)

	s := NewSelector(m)
	s.FailOn(a.ID)
	if got := s.Next(); got == nil || got.ID != a.ID {
		// b must be preferred while a is penalized
		if got == nil || got.ID == a.ID {
			t.Fatalf("expected b while a penalized, got %+v", got)
		}
	}

	s.Success(a.ID)
	got := s.Next()
	if got == nil || got.ID != a.ID {
		t.Fatalf("expected node a after success, got %+v", got)
	}
}

func TestNextPrefersSocks5WhenSelectingForSocks5(t *testing.T) {
	m := newTestPool(t)
	addAliveWithProtocol(t, m, "http-node", 90, 100, model.ProtocolHTTP)
	socks := addAliveWithProtocol(t, m, "socks-node", 80, 100, model.ProtocolSOCKS5)

	s := NewSelector(m)
	got := s.NextForProtocol(model.ProtocolSOCKS5)
	if got == nil || got.ID != socks.ID {
		t.Fatalf("expected socks5 node %d, got %+v", socks.ID, got)
	}
}

func TestNextPrefersHTTPFamilyWhenSelectingForHTTP(t *testing.T) {
	m := newTestPool(t)
	socks := addAliveWithProtocol(t, m, "socks-node", 90, 100, model.ProtocolSOCKS5)
	http := addAliveWithProtocol(t, m, "http-node", 80, 100, model.ProtocolHTTP)

	s := NewSelector(m)
	got := s.NextForProtocol(model.ProtocolHTTP)
	if got == nil || got.ID != http.ID {
		t.Fatalf("expected http node %d, got %+v", http.ID, got)
	}
	if got != nil && got.ID == socks.ID {
		t.Fatalf("expected http-family proxy, got socks5 node %d", socks.ID)
	}
}

func TestFailureWindowExpiry(t *testing.T) {
	m := newTestPool(t)
	a := addAlive(t, m, "a", 90, 100)
	addAlive(t, m, "b", 50, 100)

	s := NewSelector(m)
	s.FailOn(a.ID)
	if got := s.Next(); got == nil || got.ID == a.ID {
		t.Fatalf("expected a penalized, got %+v", got)
	}

	// Simulate window expiry by rewriting the failure timestamp.
	s.mu.Lock()
	s.failures[a.ID].at = time.Now().Add(-failureWindow - time.Second)
	s.mu.Unlock()

	got := s.Next()
	if got == nil || got.ID != a.ID {
		t.Fatalf("expected node a after window expiry, got %+v", got)
	}
}

func TestStickyReusesNodeForSameHost(t *testing.T) {
	m := newTestPool(t)
	a := addAlive(t, m, "a", 90, 100)
	addAlive(t, m, "b", 50, 100)

	s := NewSelector(m)
	first := s.NextForHost(model.ProtocolHTTP, "example.com")
	if first == nil || first.ID != a.ID {
		t.Fatalf("expected node a first, got %+v", first)
	}

	// 同一域名再次请求，应复用同一个节点（即使 b 权重更低也不换）。
	second := s.NextForHost(model.ProtocolHTTP, "example.com")
	if second == nil || second.ID != a.ID {
		t.Fatalf("expected sticky reuse of node a, got %+v", second)
	}
}

func TestStickyDifferentHostsMayDiffer(t *testing.T) {
	m := newTestPool(t)
	addAlive(t, m, "a", 90, 100)
	addAlive(t, m, "b", 50, 100)

	s := NewSelector(m)
	// 两个不同域名各自绑定，互不影响。
	first := s.NextForHost(model.ProtocolHTTP, "site-a.com")
	second := s.NextForHost(model.ProtocolHTTP, "site-b.com")
	if first == nil || second == nil {
		t.Fatalf("expected nodes for both hosts, got %+v / %+v", first, second)
	}
	// 再次请求各自域名，仍应复用各自绑定的节点。
	if got := s.NextForHost(model.ProtocolHTTP, "site-a.com"); got == nil || got.ID != first.ID {
		t.Fatalf("expected site-a.com to reuse node %d, got %+v", first.ID, got)
	}
	if got := s.NextForHost(model.ProtocolHTTP, "site-b.com"); got == nil || got.ID != second.ID {
		t.Fatalf("expected site-b.com to reuse node %d, got %+v", second.ID, got)
	}
}

func TestStickyClearedOnFail(t *testing.T) {
	m := newTestPool(t)
	a := addAlive(t, m, "a", 90, 100)
	b := addAlive(t, m, "b", 50, 100)

	s := NewSelector(m)
	if got := s.NextForHost(model.ProtocolHTTP, "example.com"); got == nil || got.ID != a.ID {
		t.Fatalf("expected node a first, got %+v", got)
	}

	// 节点 a 连续失败两次，权重被惩罚，且粘性绑定应被清除。
	s.FailOn(a.ID)
	s.FailOn(a.ID)

	got := s.NextForHost(model.ProtocolHTTP, "example.com")
	if got == nil || got.ID != b.ID {
		t.Fatalf("expected node b after a failed and sticky cleared, got %+v", got)
	}
}

func TestStickyExpiry(t *testing.T) {
	m := newTestPool(t)
	a := addAlive(t, m, "a", 90, 100)
	addAlive(t, m, "b", 50, 100)

	s := NewSelector(m)
	if got := s.NextForHost(model.ProtocolHTTP, "example.com"); got == nil || got.ID != a.ID {
		t.Fatalf("expected node a first, got %+v", got)
	}

	// 模拟粘性窗口过期：把绑定时间改到过去。
	s.mu.Lock()
	entry := s.sticky[stickyKey(model.ProtocolHTTP, "example.com")]
	entry.expiresAt = time.Now().Add(-time.Second)
	s.sticky[stickyKey(model.ProtocolHTTP, "example.com")] = entry
	s.mu.Unlock()

	// 过期后应重新选择（a 权重仍最高，所以还是 a，但绑定已刷新）。
	got := s.NextForHost(model.ProtocolHTTP, "example.com")
	if got == nil || got.ID != a.ID {
		t.Fatalf("expected node a after sticky expiry, got %+v", got)
	}
	// 绑定应被刷新为新的过期时间。
	s.mu.Lock()
	refreshed := s.sticky[stickyKey(model.ProtocolHTTP, "example.com")]
	s.mu.Unlock()
	if !refreshed.expiresAt.After(time.Now()) {
		t.Fatalf("expected sticky binding refreshed after expiry, got %+v", refreshed)
	}
}

func TestWeight(t *testing.T) {
	cases := []struct {
		name     string
		score    int
		latency  int64
		failures int
		want     float64
	}{
		{"base", 100, 100, 0, 1000},
		{"one failure", 100, 100, 1, 500},
		{"two failures", 100, 100, 2, 250},
		{"zero latency uses 1", 100, 0, 0, 100000},
		{"zero score", 0, 100, 0, 0},
		{"negative score", -5, 100, 0, 0},
	}
	for _, c := range cases {
		n := &model.ProxyNode{Score: c.score, Latency: c.latency}
		if got := weight(n, c.failures); got != c.want {
			t.Errorf("%s: weight() = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestNextNoNodes(t *testing.T) {
	m := newTestPool(t)
	s := NewSelector(m)
	if n := s.Next(); n != nil {
		t.Fatalf("expected nil with empty pool, got %+v", n)
	}
	if n := s.NextForProtocol(model.ProtocolHTTP); n != nil {
		t.Fatalf("expected nil with empty pool, got %+v", n)
	}
	if n := s.NextForHost(model.ProtocolHTTP, "example.com"); n != nil {
		t.Fatalf("expected nil with empty pool, got %+v", n)
	}
}

func TestStickyKeySeparatesProtocolAndHost(t *testing.T) {
	if stickyKey(model.ProtocolHTTP, "example.com") == stickyKey(model.ProtocolSOCKS5, "example.com") {
		t.Fatal("sticky keys for different protocols must differ")
	}
	if stickyKey(model.ProtocolHTTP, "a.com") == stickyKey(model.ProtocolHTTP, "b.com") {
		t.Fatal("sticky keys for different hosts must differ")
	}
	if stickyKey(model.ProtocolHTTP, "") == stickyKey(model.ProtocolSOCKS5, "") {
		t.Fatal("sticky keys for different protocols with empty host must differ")
	}
}

func TestStickyBindingDroppedWhenNodeDies(t *testing.T) {
	checker := &mockChecker{fail: map[int64]bool{}}
	m := newTestPoolWithChecker(t, checker)
	a := addAlive(t, m, "a", 90, 100)
	b := addAlive(t, m, "b", 50, 100)

	s := NewSelector(m)
	if got := s.NextForHost(model.ProtocolHTTP, "example.com"); got == nil || got.ID != a.ID {
		t.Fatalf("expected node a first, got %+v", got)
	}

	// 让节点 a 检测失败 -> 状态变 dead
	checker.fail[a.ID] = true
	m.CheckNode(a)

	// 节点死亡后粘性绑定应被清除，重新选择 b
	got := s.NextForHost(model.ProtocolHTTP, "example.com")
	if got == nil || got.ID != b.ID {
		t.Fatalf("expected node b after a died, got %+v", got)
	}
}
