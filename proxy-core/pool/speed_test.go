package pool

import (
	"testing"
	"time"

	"github.com/axetroy/ProxyPilot/proxy-core/model"
)

// 手动「测速」：SpeedTestNode 调用注入的测速函数，更新内存节点并持久化速率/测速时间。
func TestSpeedTestNodeUpdatesSpeed(t *testing.T) {
	m, st := newTestManagerWithChecker(t, &mockChecker{})
	m.SetSpeedTester(func(n *model.ProxyNode) (int64, error) {
		return 12_345_678, nil
	})

	n := newNode("6.6.6.6", 1080, model.ProtocolSOCKS5)
	if added := m.AddNodes([]*model.ProxyNode{n}); added != 1 {
		t.Fatalf("added = %d, want 1", added)
	}

	got, err := m.SpeedTestNode(n.ID)
	if err != nil {
		t.Fatalf("SpeedTestNode: %v", err)
	}
	if got != 12_345_678 {
		t.Fatalf("speed = %d, want 12345678", got)
	}

	stored, err := st.GetNode(n.ID)
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if stored.Speed != 12_345_678 {
		t.Fatalf("stored speed = %d, want 12345678", stored.Speed)
	}
	if stored.SpeedAt.IsZero() {
		t.Fatal("stored speed_at should be persisted")
	}
	if live := m.Get(n.ID); live == nil || live.Speed != 12_345_678 {
		t.Fatal("in-memory node should reflect speed")
	}
}

// 未注入测速函数时手动测速应返回错误，且不改变节点。
func TestSpeedTestNodeWithoutTester(t *testing.T) {
	m, st := newTestManagerWithChecker(t, &mockChecker{})
	n := newNode("7.7.7.7", 1080, model.ProtocolSOCKS5)
	_ = m.AddNodes([]*model.ProxyNode{n})

	if _, err := m.SpeedTestNode(n.ID); err == nil {
		t.Fatal("expected error when no speed tester configured")
	}
	stored, _ := st.GetNode(n.ID)
	if stored.Speed != 0 {
		t.Fatalf("stored speed = %d, want 0 (untouched)", stored.Speed)
	}
}

// 健康检查自动测速：按节点节流，窗口内不重复测速。
func TestProbeSpeedThrottledPerNode(t *testing.T) {
	m, _ := newTestManagerWithChecker(t, &mockChecker{})
	calls := 0
	m.SetSpeedTester(func(n *model.ProxyNode) (int64, error) {
		calls++
		return 1 << 20, nil
	})
	m.SetSpeedTestEnabled(true)
	m.SetSpeedTestInterval(10 * time.Minute)

	n := newNode("8.8.8.8", 1080, model.ProtocolSOCKS5)
	_ = m.AddNodes([]*model.ProxyNode{n})

	_ = m.CheckNode(n)
	if calls != 1 {
		t.Fatalf("speed tester calls = %d, want 1", calls)
	}
	// 窗口内再检测：应被节流，不再调用测速函数
	_ = m.CheckNode(m.Get(n.ID))
	if calls != 1 {
		t.Fatalf("speed tester calls = %d, want 1 (throttled)", calls)
	}

	// 超过节流窗口：应重新测速
	older := m.Get(n.ID)
	older.SpeedAt = time.Now().Add(-2 * time.Duration(m.speedTestInterval.Load()))
	_ = m.CheckNode(older)
	if calls != 2 {
		t.Fatalf("speed tester calls = %d, want 2 after recheck window", calls)
	}
}

// 自动测速开关关闭时，健康检查不触发测速。
func TestProbeSpeedDisabledByDefault(t *testing.T) {
	m, _ := newTestManagerWithChecker(t, &mockChecker{})
	calls := 0
	m.SetSpeedTester(func(n *model.ProxyNode) (int64, error) {
		calls++
		return 1 << 20, nil
	})
	m.SetSpeedTestEnabled(false) // 默认关闭

	n := newNode("9.9.9.9", 1080, model.ProtocolSOCKS5)
	_ = m.AddNodes([]*model.ProxyNode{n})
	_ = m.CheckNode(n)
	if calls != 0 {
		t.Fatalf("speed tester calls = %d, want 0 (disabled)", calls)
	}
}

// SpeedTestNodes 对多节点并发测速，返回成功/失败计数，且并发不破坏各节点落库。
func TestSpeedTestNodesConcurrent(t *testing.T) {
	m, st := newTestManagerWithChecker(t, &mockChecker{})
	var calls int
	m.SetSpeedTester(func(n *model.ProxyNode) (int64, error) {
		calls++
		return int64(calls) << 20, nil
	})

	nodes := []*model.ProxyNode{
		newNode("10.0.0.1", 1080, model.ProtocolSOCKS5),
		newNode("10.0.0.2", 1080, model.ProtocolSOCKS5),
		newNode("10.0.0.3", 1080, model.ProtocolSOCKS5),
	}
	if added := m.AddNodes(nodes); added != 3 {
		t.Fatalf("added = %d, want 3", added)
	}

	done, failed := m.SpeedTestNodes([]int64{nodes[0].ID, nodes[1].ID, nodes[2].ID})
	if done != 3 || failed != 0 {
		t.Fatalf("SpeedTestNodes = (%d, %d), want (3, 0)", done, failed)
	}
	for _, n := range nodes {
		stored, err := st.GetNode(n.ID)
		if err != nil {
			t.Fatalf("GetNode %d: %v", n.ID, err)
		}
		if stored.Speed == 0 {
			t.Fatalf("node %d speed not persisted", n.ID)
		}
		if live := m.Get(n.ID); live == nil || live.Speed == 0 {
			t.Fatalf("in-memory node %d speed not updated", n.ID)
		}
	}
}

// 空输入与无测速函数时，SpeedTestNodes 应安全返回 (0,0) / 全失败，不 panic、不死锁。
func TestSpeedTestNodesEdgeCases(t *testing.T) {
	m, _ := newTestManagerWithChecker(t, &mockChecker{})

	if done, failed := m.SpeedTestNodes(nil); done != 0 || failed != 0 {
		t.Fatalf("empty ids = (%d, %d), want (0, 0)", done, failed)
	}

	n := newNode("10.0.0.9", 1080, model.ProtocolSOCKS5)
	_ = m.AddNodes([]*model.ProxyNode{n})
	// 未注入测速函数：每个节点返回错误，计入 failed
	if done, failed := m.SpeedTestNodes([]int64{n.ID}); done != 0 || failed != 1 {
		t.Fatalf("no tester = (%d, %d), want (0, 1)", done, failed)
	}
}
