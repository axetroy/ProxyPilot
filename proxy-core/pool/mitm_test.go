package pool

import (
	"testing"
	"time"

	"github.com/axetroy/ProxyPilot/proxy-core/model"
)

// 检出中间人时：节点被标记并持久化，错误日志可见，连接安全子评分清零。
func TestProbeMitmMarksNodeAndZeroesSafety(t *testing.T) {
	m, st := newTestManagerWithChecker(t, &mockChecker{})
	calls := 0
	m.SetMITMDetector(func(n *model.ProxyNode) (bool, string) {
		calls++
		return true, "证书主机名不匹配"
	})

	n := newNode("6.6.6.6", 1080, model.ProtocolSOCKS5)
	if added := m.AddNodes([]*model.ProxyNode{n}); added != 1 {
		t.Fatalf("added = %d, want 1", added)
	}
	res := m.CheckNode(n)
	if !res.OK {
		t.Fatalf("check should pass, err=%v", res.Error)
	}

	got, err := st.GetNode(n.ID)
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if !got.MitmDetected {
		t.Fatal("node should be marked as MITM")
	}
	if got.MitmAt.IsZero() {
		t.Fatal("mitm check time should be persisted")
	}
	if live := m.Get(n.ID); live == nil || !live.MitmDetected {
		t.Fatal("in-memory node should be marked as MITM")
	}
	if calls != 1 {
		t.Fatalf("detector calls = %d, want 1", calls)
	}

	// 连接安全子维度清零：Breakdown 口径下 safety=0
	bd := Breakdown(m.Get(n.ID))
	if bd.Safety != 0 {
		t.Fatalf("safety score = %d, want 0 (MITM)", bd.Safety)
	}
}

// 中间人检测按节点节流：窗口内重复检测不再调用探测函数。
func TestProbeMitmThrottledPerNode(t *testing.T) {
	m, _ := newTestManagerWithChecker(t, &mockChecker{})
	calls := 0
	m.SetMITMDetector(func(n *model.ProxyNode) (bool, string) {
		calls++
		return false, ""
	})

	n := newNode("7.7.7.7", 1080, model.ProtocolSOCKS5)
	_ = m.AddNodes([]*model.ProxyNode{n})
	_ = m.CheckNode(n)

	// 窗口内再检测：探测函数不应再次执行
	fresh := m.Get(n.ID)
	fresh.LastCheck = time.Now() // 刚检测过
	_ = m.CheckNode(fresh)
	if calls != 1 {
		t.Fatalf("detector calls = %d, want 1 (throttled)", calls)
	}

	// 超过节流窗口：应重新探测
	older := m.Get(n.ID)
	older.MitmAt = time.Now().Add(-2 * mitmRecheckInterval)
	_ = m.CheckNode(older)
	if calls != 2 {
		t.Fatalf("detector calls = %d, want 2 after recheck window", calls)
	}
}

// 安全路由：排除过滤器命中节点不被自动选路；全部被排除时回退不过滤。
func TestExcludeFilterWithFallback(t *testing.T) {
	m, _ := newTestManagerWithChecker(t, &mockChecker{})
	clean := newNode("5.5.5.5", 1080, model.ProtocolSOCKS5)
	dirty := newNode("9.9.9.9", 1080, model.ProtocolSOCKS5)
	dirty.MitmDetected = true
	dirty.Status = model.StatusAlive
	_ = m.AddNodes([]*model.ProxyNode{clean, dirty})
	clean.Status = model.StatusAlive

	// 模拟 main.go 的装配：avoid_mitm 开启时规避中间人节点
	m.SetExcludeFilter(func(n *model.ProxyNode) bool {
		return n.MitmDetected
	})

	got := m.PickBest()
	if got == nil || got.ID != clean.ID {
		t.Fatalf("PickBest should skip MITM node, got %+v", got)
	}
	for _, n := range m.Alive() {
		if n.ID == dirty.ID {
			t.Fatal("Alive() should not include excluded node when others exist")
		}
	}

	// 全部存活节点都被排除：回退为不过滤（可用性优先）
	clean.Status = model.StatusDead
	got = m.PickBest()
	if got == nil || got.ID != dirty.ID {
		t.Fatalf("fallback should return MITM node when no alternative, got %+v", got)
	}
	if len(m.Alive()) != 1 {
		t.Fatalf("Alive() fallback = %d nodes, want 1", len(m.Alive()))
	}

	// 过滤器为空时行为不变
	m.SetExcludeFilter(nil)
	got = m.PickBest()
	if got == nil || got.ID != dirty.ID {
		t.Fatalf("PickBest without filter should return alive node, got %+v", got)
	}
}

// Excluded 供外部（调度器粘性校验）判断节点是否被过滤器命中。
func TestExcludedReportsFilterResult(t *testing.T) {
	m, _ := newTestManagerWithChecker(t, &mockChecker{})
	n := newNode("4.4.4.4", 1080, model.ProtocolSOCKS5)
	n.MitmDetected = true

	if m.Excluded(n) {
		t.Fatal("no filter installed yet, should not be excluded")
	}
	m.SetExcludeFilter(func(x *model.ProxyNode) bool { return x.MitmDetected })
	if !m.Excluded(n) {
		t.Fatal("MITM node should be excluded when filter is set")
	}
	m.SetExcludeFilter(nil)
	if m.Excluded(n) {
		t.Fatal("exclusion should be disabled after clearing filter")
	}
}

// 未注入探测函数时 evalOne 不应 panic，也不应产生标记。
func TestEvalOneWithoutMitmDetector(t *testing.T) {
	m, st := newTestManagerWithChecker(t, &mockChecker{})

	n := newNode("3.3.3.3", 1080, model.ProtocolSOCKS5)
	_ = m.AddNodes([]*model.ProxyNode{n})
	res := m.CheckNode(n)
	if !res.OK {
		t.Fatalf("check should pass: %v", res.Error)
	}
	got, err := st.GetNode(n.ID)
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if got.MitmDetected || !got.MitmAt.IsZero() {
		t.Fatalf("no detector installed, node must stay unmarked: %+v", got)
	}
}
