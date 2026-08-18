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

func TestEffectiveScore(t *testing.T) {
	cases := []struct {
		name     string
		score    int
		failures int
		want     float64
	}{
		{"base", 100, 0, 100},
		{"one failure", 100, 1, 50},
		{"two failures", 100, 2, 25},
		{"zero score", 0, 0, 0},
		{"negative score", -5, 0, 0},
	}
	for _, c := range cases {
		n := &model.ProxyNode{Score: c.score}
		if got := effectiveScore(n, c.failures); got != c.want {
			t.Errorf("%s: effectiveScore() = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestSelectBestPrefersScoreThenLatency(t *testing.T) {
	m := newTestPool(t)
	// 分数相同（80），延迟低的优先。
	slow := addAlive(t, m, "slow", 80, 300)
	fast := addAlive(t, m, "fast", 80, 100)

	s := NewSelector(m)
	got := s.Next()
	if got == nil || got.ID != fast.ID {
		t.Fatalf("expected fast node (id=%d) with same score, got %+v", fast.ID, got)
	}
	_ = slow
}

func TestSelectBestScoreBeatsLatency(t *testing.T) {
	m := newTestPool(t)
	// 分数优先于延迟：90 分高延迟节点应胜过 50 分低延迟节点。
	high := addAlive(t, m, "high-score", 90, 500)
	addAlive(t, m, "low-score", 50, 50)

	s := NewSelector(m)
	got := s.Next()
	if got == nil || got.ID != high.ID {
		t.Fatalf("expected high-score node (id=%d), got %+v", high.ID, got)
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

// ---------- NextStrict ----------

func TestNextStrictFiltersByProtocol(t *testing.T) {
	m := newTestPool(t)
	httpNode := addAlive(t, m, "http-a", 90, 100)
	socks := addAliveWithProtocol(t, m, "socks-a", 80, 100, model.ProtocolSOCKS5)

	s := NewSelector(m)
	got := s.NextStrict(model.ProtocolSOCKS5)
	if got == nil || got.ID != socks.ID {
		t.Fatalf("expected socks5 node %d, got %+v", socks.ID, got)
	}
	got = s.NextStrict(model.ProtocolHTTP)
	if got == nil || got.ID != httpNode.ID {
		t.Fatalf("expected http node %d, got %+v", httpNode.ID, got)
	}
}

func TestNextStrictNoCrossProtocolFallback(t *testing.T) {
	m := newTestPool(t)
	addAlive(t, m, "http-a", 90, 100) // 池中只有 HTTP 节点

	s := NewSelector(m)
	if got := s.NextStrict(model.ProtocolSOCKS5); got != nil {
		t.Fatalf("expected nil for socks5 with only http nodes, got %+v", got)
	}
	// 对比：NextForProtocol（软限制）此时会回退到 HTTP 节点。
	if got := s.NextForProtocol(model.ProtocolSOCKS5); got == nil {
		t.Fatal("expected cross-protocol fallback in NextForProtocol")
	}
}

func TestNextStrictEmptyPool(t *testing.T) {
	m := newTestPool(t)
	s := NewSelector(m)
	if got := s.NextStrict(model.ProtocolSOCKS5); got != nil {
		t.Fatalf("expected nil with empty pool, got %+v", got)
	}
	if got := s.NextStrict(model.ProtocolHTTP); got != nil {
		t.Fatalf("expected nil with empty pool, got %+v", got)
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

// ---------- 指定固定出口（pin） ----------

func TestPinForcesNode(t *testing.T) {
	m := newTestPool(t)
	a := addAlive(t, m, "a", 90, 100)
	b := addAlive(t, m, "b", 50, 200)

	s := NewSelector(m)
	// 未指定时选最优（a）
	if got := s.Next(); got == nil || got.ID != a.ID {
		t.Fatalf("expected node a before pin, got %+v", got)
	}

	s.Pin(b.ID)
	// 指定 b 后固定返回 b（即使 a 评分更高）
	for i := 0; i < 3; i++ {
		if got := s.Next(); got == nil || got.ID != b.ID {
			t.Fatalf("expected pinned node b, got %+v", got)
		}
	}
	if s.PinnedID() != b.ID {
		t.Errorf("PinnedID() = %d, want %d", s.PinnedID(), b.ID)
	}
	if p := s.Pinned(); p == nil || p.ID != b.ID {
		t.Errorf("Pinned() = %+v, want node b", p)
	}
}

func TestPinForcesNodeForHostAndProtocol(t *testing.T) {
	m := newTestPool(t)
	httpNode := addAlive(t, m, "http-a", 90, 100)
	addAlive(t, m, "b", 50, 100)

	s := NewSelector(m)
	s.Pin(httpNode.ID)

	if got := s.NextForHost(model.ProtocolHTTP, "example.com"); got == nil || got.ID != httpNode.ID {
		t.Fatalf("expected pinned http node for host, got %+v", got)
	}
	if got := s.NextForProtocol(model.ProtocolHTTP); got == nil || got.ID != httpNode.ID {
		t.Fatalf("expected pinned http node for protocol, got %+v", got)
	}
}

func TestUnpinRestoresAutoSelect(t *testing.T) {
	m := newTestPool(t)
	a := addAlive(t, m, "a", 90, 100)
	b := addAlive(t, m, "b", 50, 100)

	s := NewSelector(m)
	s.Pin(b.ID)
	s.Unpin()
	if s.PinnedID() != 0 {
		t.Errorf("PinnedID() = %d after unpin, want 0", s.PinnedID())
	}
	if p := s.Pinned(); p != nil {
		t.Errorf("Pinned() = %+v after unpin, want nil", p)
	}
	// 恢复按评分选择 a
	if got := s.Next(); got == nil || got.ID != a.ID {
		t.Fatalf("expected node a after unpin, got %+v", got)
	}
}

func TestPinIgnoresDeadNode(t *testing.T) {
	checker := &mockChecker{fail: map[int64]bool{}}
	m := newTestPoolWithChecker(t, checker)
	a := addAlive(t, m, "a", 90, 100)
	b := addAlive(t, m, "b", 50, 100)

	s := NewSelector(m)
	s.Pin(a.ID)

	// a 检测失败变 dead 后，指定节点不可用，应回退自动选择 b
	checker.fail[a.ID] = true
	m.CheckNode(a)

	if got := s.Next(); got == nil || got.ID != b.ID {
		t.Fatalf("expected fallback to b when pinned node dead, got %+v", got)
	}
	// 指定仍然保留（等节点复活后恢复），不自动清除
	if s.PinnedID() != a.ID {
		t.Errorf("PinnedID() = %d, want still %d", s.PinnedID(), a.ID)
	}

	// 节点复活后恢复固定使用 a
	delete(checker.fail, a.ID)
	m.CheckNode(a)
	if got := s.Next(); got == nil || got.ID != a.ID {
		t.Fatalf("expected pinned node a after revive, got %+v", got)
	}
}

func TestPinForcesNodeAcrossProtocols(t *testing.T) {
	m := newTestPool(t)
	httpNode := addAlive(t, m, "http-a", 50, 100)
	socks := addAliveWithProtocol(t, m, "socks-a", 90, 100, model.ProtocolSOCKS5)

	// 指定 HTTP 节点后，SOCKS5 流量也应固定走它（指定优先于协议族划分），
	// 因为 ConnectTCP 会按节点自身协议完成握手。
	s := NewSelector(m)
	s.Pin(httpNode.ID)
	if got := s.NextForProtocol(model.ProtocolSOCKS5); got == nil || got.ID != httpNode.ID {
		t.Fatalf("expected pinned http node for socks5 traffic, got %+v", got)
	}
	if got := s.Next(); got == nil || got.ID != httpNode.ID {
		t.Fatalf("expected pinned http node for generic Next, got %+v", got)
	}

	// 指定 SOCKS5 节点后，HTTP 流量也应固定走它。
	s2 := NewSelector(m)
	s2.Pin(socks.ID)
	if got := s2.NextForProtocol(model.ProtocolHTTP); got == nil || got.ID != socks.ID {
		t.Fatalf("expected pinned socks5 node for http traffic, got %+v", got)
	}
}

func TestPinOverridesSticky(t *testing.T) {
	m := newTestPool(t)
	a := addAlive(t, m, "a", 90, 100)
	b := addAlive(t, m, "b", 50, 100)

	s := NewSelector(m)
	// 先让 example.com 在自动模式下绑定到最高分的 a
	if got := s.NextForHost(model.ProtocolHTTP, "example.com"); got == nil || got.ID != a.ID {
		t.Fatalf("expected node a first, got %+v", got)
	}

	// 指定 b 后，即使 example.com 已有粘性绑定到 a，也固定返回 b（指定优先于粘性）
	s.Pin(b.ID)
	for i := 0; i < 2; i++ {
		if got := s.NextForHost(model.ProtocolHTTP, "example.com"); got == nil || got.ID != b.ID {
			t.Fatalf("expected pinned node b despite sticky, got %+v", got)
		}
	}

	// 取消指定后，恢复粘性/自动选择（原粘性绑定 a 仍然有效）
	s.Unpin()
	if got := s.NextForHost(model.ProtocolHTTP, "example.com"); got == nil || got.ID != a.ID {
		t.Fatalf("expected sticky node a after unpin, got %+v", got)
	}
}

func TestPinAutoClearedWhenNodeRemoved(t *testing.T) {
	m := newTestPool(t)
	a := addAlive(t, m, "a", 90, 100)
	b := addAlive(t, m, "b", 50, 100)

	s := NewSelector(m)
	s.Pin(a.ID)

	// 手动从池中删除指定的节点（模拟用户删除或自动淘汰）
	if err := m.Remove(a.ID); err != nil {
		t.Fatalf("remove node failed: %v", err)
	}

	// 指定节点已不存在，应自动取消并回退自动选择 b
	if got := s.Next(); got == nil || got.ID != b.ID {
		t.Fatalf("expected fallback to b after pinned node removed, got %+v", got)
	}
	// 内存中的指定已被自动清除，避免悬空
	if s.PinnedID() != 0 {
		t.Errorf("PinnedID() = %d, want 0 after node removed", s.PinnedID())
	}
	if p := s.Pinned(); p != nil {
		t.Errorf("Pinned() = %+v after node removed, want nil", p)
	}
}

func TestPinSocksMatchesSocks(t *testing.T) {
	m := newTestPool(t)
	httpNode := addAlive(t, m, "http-a", 95, 10)
	socks := addAliveWithProtocol(t, m, "socks-a", 50, 100, model.ProtocolSOCKS5)

	s := NewSelector(m)
	s.Pin(socks.ID)

	// SOCKS5 流量固定走指定 SOCKS5 节点
	if got := s.NextForProtocol(model.ProtocolSOCKS5); got == nil || got.ID != socks.ID {
		t.Fatalf("expected pinned socks5 node, got %+v", got)
	}
	// HTTP 流量同样固定走指定 SOCKS5 节点（指定优先于协议族划分）
	if got := s.NextForProtocol(model.ProtocolHTTP); got == nil || got.ID != socks.ID {
		t.Fatalf("expected pinned socks5 node for http traffic, got %+v", got)
	}
	_ = httpNode
}

func TestStrategyDefaultIsWeighted(t *testing.T) {
	m := newTestPool(t)
	s := NewSelector(m)
	if s.Strategy() != StrategyWeighted {
		t.Fatalf("default strategy = %s, want weighted", s.Strategy())
	}
}

func TestStrategyBestPicksHighestScore(t *testing.T) {
	m := newTestPool(t)
	high := addAlive(t, m, "high", 95, 200)
	low := addAlive(t, m, "low", 60, 10)
	s := NewSelector(m)
	s.SetStrategy(StrategyBest)
	got := s.Next()
	if got == nil || got.ID != high.ID {
		t.Fatalf("best strategy should pick highest-score node, got %+v", got)
	}
	_ = low
}

func TestStrategyRandomReturnsAliveAndSticky(t *testing.T) {
	m := newTestPool(t)
	addAlive(t, m, "a", 80, 100)
	addAlive(t, m, "b", 70, 100)
	s := NewSelector(m)
	s.SetStrategy(StrategyRandom)

	// 多次随机（无 host，不走粘性）应返回池中的存活节点，且覆盖多个节点
	seen := map[int64]bool{}
	for i := 0; i < 50; i++ {
		n := s.Next()
		if n == nil {
			t.Fatal("random strategy returned nil")
		}
		seen[n.ID] = true
	}
	if len(seen) < 2 {
		t.Fatalf("random strategy should eventually pick multiple nodes, got %d distinct", len(seen))
	}

	// 同一域名在粘性窗口内保持稳定（复用同一节点）
	first := s.NextForHost(model.ProtocolHTTP, "sticky.test")
	if first == nil {
		t.Fatal("random strategy returned nil for sticky host")
	}
	for i := 0; i < 10; i++ {
		n := s.NextForHost(model.ProtocolHTTP, "sticky.test")
		if n == nil || n.ID != first.ID {
			t.Fatalf("random + sticky should reuse same node, got %v want %d", n, first.ID)
		}
	}
}

func TestStrategyRoundRobinCycles(t *testing.T) {
	m := newTestPool(t)
	a := addAlive(t, m, "a", 80, 100)
	b := addAlive(t, m, "b", 70, 100)
	c := addAlive(t, m, "c", 60, 100)
	s := NewSelector(m)
	s.SetStrategy(StrategyRoundRobin)

	var order []int64
	for i := 0; i < 6; i++ {
		n := s.Next()
		if n == nil {
			t.Fatal("round-robin returned nil")
		}
		order = append(order, n.ID)
	}
	// 按 ID 顺序轮转：a, b, c, a, b, c
	want := []int64{a.ID, b.ID, c.ID, a.ID, b.ID, c.ID}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("round-robin order = %v, want %v", order, want)
		}
	}
}

func TestStrategyInvalidIgnored(t *testing.T) {
	m := newTestPool(t)
	s := NewSelector(m)
	s.SetStrategy("nonsense")
	if s.Strategy() != StrategyWeighted {
		t.Fatalf("invalid strategy changed selector to %s", s.Strategy())
	}
}

// TestPinnedOnlyInFixedStrategy 固定节点仅在策略为 fixed 时视为有效
// （Pinned 返回 nil），避免切换策略后界面仍显示"固定出口已指定"。
func TestPinnedOnlyInFixedStrategy(t *testing.T) {
	m := newTestPool(t)
	a := addAlive(t, m, "a", 80, 100)

	s := NewSelector(m)
	s.Pin(a.ID)
	// Pin 自动切到 fixed：Pinned 返回指定节点
	if s.Strategy() != StrategyFixed || s.Pinned() == nil || s.Pinned().ID != a.ID {
		t.Fatalf("fixed strategy should expose pinned node, got strategy=%s pinned=%+v", s.Strategy(), s.Pinned())
	}

	// 切到 best：固定节点不再视为有效，但指定仍保留（切回 fixed 可恢复）
	s.SetStrategy(StrategyBest)
	if s.Pinned() != nil {
		t.Fatalf("Pinned() should be nil when strategy is not fixed, got %+v", s.Pinned())
	}
	if s.PinnedID() != a.ID {
		t.Fatalf("PinnedID() = %d, want %d (pin should be retained)", s.PinnedID(), a.ID)
	}

	// 切回 fixed：恢复显示
	s.SetStrategy(StrategyFixed)
	if s.Pinned() == nil || s.Pinned().ID != a.ID {
		t.Fatalf("fixed strategy should restore pinned node, got %+v", s.Pinned())
	}
}
