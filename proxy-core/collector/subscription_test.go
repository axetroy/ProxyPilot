package collector

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/axetroy/ProxyPilot/proxy-core/bus"
	"github.com/axetroy/ProxyPilot/proxy-core/model"
	"github.com/axetroy/ProxyPilot/proxy-core/storage"
)

// mockSink 记录收集到的节点，并真实保存到存储以便验证清理逻辑。
type mockSink struct {
	store   *storage.Store
	added   int
	removed []int64
}

func (m *mockSink) AddNodes(nodes []*model.ProxyNode) int {
	added := 0
	for _, n := range nodes {
		isNew, err := m.store.SaveNode(n)
		if err == nil && isNew {
			added++
		}
	}
	m.added += added
	return added
}

func (m *mockSink) RemoveNodes(ids []int64) {
	m.removed = append(m.removed, ids...)
	for _, id := range ids {
		_ = m.store.DeleteNode(id)
	}
}

func newTestManager(t *testing.T) (*Manager, *storage.Store, *mockSink) {
	t.Helper()
	st, err := storage.New(":memory:")
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sink := &mockSink{store: st}
	m := NewManager(st, bus.New(), sink, 5*time.Second)
	t.Cleanup(m.Stop)
	return m, st, sink
}

func TestAddSubscription(t *testing.T) {
	m, st, _ := newTestManager(t)
	sub, err := m.AddSubscription("sub", "https://example.com", 3600)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if sub.ID == 0 {
		t.Fatal("expected subscription ID assigned")
	}
	if !sub.Enabled {
		t.Fatal("expected subscription enabled by default")
	}
	subs, err := st.ListSubscriptions()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("len = %d, want 1", len(subs))
	}
}

func TestUpdateSubscriptionNoNewRow(t *testing.T) {
	m, st, _ := newTestManager(t)
	sub, _ := m.AddSubscription("sub", "https://example.com", 3600)

	if err := m.UpdateSubscription(sub.ID, "renamed", "https://new.example.com", 1800, false); err != nil {
		t.Fatalf("update: %v", err)
	}
	subs, _ := st.ListSubscriptions()
	if len(subs) != 1 {
		t.Fatalf("expected 1 subscription after update, got %d", len(subs))
	}
	if subs[0].Name != "renamed" || subs[0].URL != "https://new.example.com" {
		t.Fatalf("subscription not updated: %+v", subs[0])
	}
	if subs[0].Interval != 1800 || subs[0].Enabled {
		t.Fatalf("fields not updated: %+v", subs[0])
	}
}

func TestDeleteSubscription(t *testing.T) {
	m, st, sink := newTestManager(t)
	sub, _ := m.AddSubscription("sub", "https://example.com", 3600)

	// 关联一个节点到订阅
	node := &model.ProxyNode{Host: "1.1.1.1", Port: 80, Protocol: model.ProtocolHTTP, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if added, _ := st.SaveNode(node); !added {
		t.Fatal("expected node added")
	}
	if err := st.AttachNodeToSubscription(node.ID, sub.ID); err != nil {
		t.Fatalf("attach: %v", err)
	}

	if err := m.Delete(sub.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(sink.removed) != 1 || sink.removed[0] != node.ID {
		t.Fatalf("removed = %v, want [%d]", sink.removed, node.ID)
	}
	subs, _ := st.ListSubscriptions()
	if len(subs) != 0 {
		t.Fatalf("expected 0 subscriptions, got %d", len(subs))
	}
}

func TestFetchNowParsesNodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("1.1.1.1:80\n2.2.2.2:443"))
	}))
	defer srv.Close()

	m, st, sink := newTestManager(t)
	sub, _ := m.AddSubscription("sub", srv.URL, 3600)

	if err := m.FetchNow(context.Background(), sub); err != nil {
		t.Fatalf("fetchNow: %v", err)
	}
	if sink.added != 2 {
		t.Fatalf("added = %d, want 2", sink.added)
	}
	// last_fetch 应被更新
	subs, _ := st.ListSubscriptions()
	if subs[0].LastFetch.IsZero() {
		t.Fatal("expected last_fetch set")
	}
}

func TestFetchNowError(t *testing.T) {
	m, _, _ := newTestManager(t)
	sub, _ := m.AddSubscription("sub", "http://127.0.0.1:1", 3600)
	if err := m.FetchNow(context.Background(), sub); err == nil {
		t.Fatal("expected error fetching unreachable subscription")
	}
}

func TestFetchNowRemovesStaleNodes(t *testing.T) {
	// 第一次抓取返回 3 个节点，第二次只返回 2 个（1 个被移除），
	// 验证旧节点会被清理，代理数不会只增不减。
	body := "1.1.1.1:80\n2.2.2.2:443\n3.3.3.3:8080"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	m, st, sink := newTestManager(t)
	sub, _ := m.AddSubscription("sub", srv.URL, 3600)

	if err := m.FetchNow(context.Background(), sub); err != nil {
		t.Fatalf("first fetchNow: %v", err)
	}
	if sink.added != 3 {
		t.Fatalf("added = %d, want 3", sink.added)
	}
	nodes, _ := st.ListNodesBySubscription(sub.ID)
	if len(nodes) != 3 {
		t.Fatalf("nodes after first fetch = %d, want 3", len(nodes))
	}

	// 第二次抓取：3.3.3.3 从订阅中消失
	body = "1.1.1.1:80\n2.2.2.2:443"
	if err := m.FetchNow(context.Background(), sub); err != nil {
		t.Fatalf("second fetchNow: %v", err)
	}
	if len(sink.removed) != 1 {
		t.Fatalf("removed = %v, want 1 stale node", sink.removed)
	}
	nodes, _ = st.ListNodesBySubscription(sub.ID)
	if len(nodes) != 2 {
		t.Fatalf("nodes after second fetch = %d, want 2", len(nodes))
	}
	// 被移除的节点应从存储中删除
	remaining, _ := st.ListNode()
	if len(remaining) != 2 {
		t.Fatalf("remaining nodes = %d, want 2", len(remaining))
	}
}

func TestFetchNowKeepsSharedNodes(t *testing.T) {
	// 同一节点被两个订阅共享：一个订阅移除该节点后，
	// 节点不应被删除（仍被另一个订阅引用），只解除关联。
	bodyA := "1.1.1.1:80\n2.2.2.2:443"
	bodyB := "2.2.2.2:443\n3.3.3.3:8080"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/a" {
			_, _ = w.Write([]byte(bodyA))
		} else {
			_, _ = w.Write([]byte(bodyB))
		}
	}))
	defer srv.Close()

	m, st, sink := newTestManager(t)
	subA, _ := m.AddSubscription("a", srv.URL+"/a", 3600)
	subB, _ := m.AddSubscription("b", srv.URL+"/b", 3600)

	if err := m.FetchNow(context.Background(), subA); err != nil {
		t.Fatalf("fetch A: %v", err)
	}
	if err := m.FetchNow(context.Background(), subB); err != nil {
		t.Fatalf("fetch B: %v", err)
	}

	// 订阅 A 移除 2.2.2.2
	bodyA = "1.1.1.1:80"
	if err := m.FetchNow(context.Background(), subA); err != nil {
		t.Fatalf("fetch A again: %v", err)
	}
	// 2.2.2.2 仍被订阅 B 引用，不应被删除
	remaining, _ := st.ListNode()
	if len(remaining) != 3 {
		t.Fatalf("remaining nodes = %d, want 3 (2.2.2.2 kept by sub B)", len(remaining))
	}
	if len(sink.removed) != 0 {
		t.Fatalf("removed = %v, want none (node still shared)", sink.removed)
	}
	// 订阅 A 的代理数应减为 1
	nodesA, _ := st.ListNodesBySubscription(subA.ID)
	if len(nodesA) != 1 {
		t.Fatalf("sub A nodes = %d, want 1", len(nodesA))
	}
}

func TestRunSchedulesEnabledSubscriptions(t *testing.T) {
	m, st, _ := newTestManager(t)
	if _, err := m.AddSubscription("enabled", "https://example.com", 3600); err != nil {
		t.Fatalf("add: %v", err)
	}
	disabled := &model.Subscription{Name: "disabled", URL: "https://example.com", Interval: 3600, Enabled: false}
	if err := st.UpsertSubscription(disabled); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	if err := m.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	m.mu.Lock()
	timerCount := len(m.timers)
	m.mu.Unlock()
	// 只有 enabled 的订阅会被调度
	if timerCount != 1 {
		t.Fatalf("timer count = %d, want 1", timerCount)
	}
}

func TestRunStops(t *testing.T) {
	m, _, _ := newTestManager(t)
	if _, err := m.AddSubscription("sub", "https://example.com", 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	// interval=1s 的订阅会有定时器
	m.mu.Lock()
	timerCount := len(m.timers)
	m.mu.Unlock()
	if timerCount != 1 {
		t.Fatalf("timer count = %d, want 1", timerCount)
	}
	m.Stop()
	m.mu.Lock()
	timerCount = len(m.timers)
	m.mu.Unlock()
	// Stop 会 stop 但保留 timer 引用；再次 Stop 不应 panic
	m.Stop()
	_ = timerCount
}

func TestList(t *testing.T) {
	m, _, _ := newTestManager(t)
	if _, err := m.AddSubscription("sub-a", "https://example.com/a", 3600); err != nil {
		t.Fatalf("add a: %v", err)
	}
	if _, err := m.AddSubscription("sub-b", "https://example.com/b", 3600); err != nil {
		t.Fatalf("add b: %v", err)
	}
	subs, err := m.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(subs) != 2 {
		t.Errorf("subs = %d, want 2", len(subs))
	}
}

// scheduleInContext 定时刷新：短 interval 触发 refresh 并重新调度；取消后停止。
func TestScheduleInContext(t *testing.T) {
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("1.2.3.4:8080"))
	}))
	defer src.Close()

	m, st, sink := newTestManager(t)
	sub := &model.Subscription{ID: 1, Name: "sub", URL: src.URL, Interval: 1, Enabled: true}
	if err := st.UpsertSubscription(sub); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.scheduleInContext(ctx, sub)

	// 等待定时刷新触发（interval=1s，最多等 3s）
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if sink.added > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if sink.added == 0 {
		t.Fatal("scheduled refresh did not add nodes")
	}

	// 取消后：当前排定的 timer 可能再触发一次，但之后必须停止
	cancel()
	time.Sleep(2500 * time.Millisecond)
	afterCancel := sink.added
	time.Sleep(1500 * time.Millisecond)
	if sink.added != afterCancel {
		t.Errorf("refresh continued after cancel: added %d -> %d", afterCancel, sink.added)
	}
}
