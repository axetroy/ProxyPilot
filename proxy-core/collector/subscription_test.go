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

// mockSink 记录收集到的节点。
type mockSink struct {
	added   int
	removed []int64
}

func (m *mockSink) AddNodes(nodes []*model.ProxyNode) int {
	m.added += len(nodes)
	return len(nodes)
}

func (m *mockSink) RemoveNodes(ids []int64) {
	m.removed = append(m.removed, ids...)
}

func newTestManager(t *testing.T) (*Manager, *storage.Store, *mockSink) {
	t.Helper()
	st, err := storage.New(":memory:")
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sink := &mockSink{}
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