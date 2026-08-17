package storage

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/axetroy/ProxyPilot/proxy-core/model"
)

func TestDeleteSubscriptionRemovesNodesFromThatSubscription(t *testing.T) {
	st, err := New(":memory:")
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	defer func() { _ = st.Close() }()

	sub := &model.Subscription{Name: "sub-a", URL: "https://example.com", Enabled: true, CreatedAt: time.Now().UTC()}
	if err := st.UpsertSubscription(sub); err != nil {
		t.Fatalf("upsert subscription: %v", err)
	}

	node := &model.ProxyNode{Host: "1.2.3.4", Port: 1080, Protocol: model.ProtocolSOCKS5, Status: model.StatusAlive, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if added, err := st.SaveNode(node); err != nil {
		t.Fatalf("save node: %v", err)
	} else if !added {
		t.Fatal("expected node to be added")
	}
	if err := st.AttachNodeToSubscription(node.ID, sub.ID); err != nil {
		t.Fatalf("attach node: %v", err)
	}

	if _, err := st.DeleteSubscription(sub.ID); err != nil {
		t.Fatalf("delete subscription: %v", err)
	}

	count, err := st.CountNodes()
	if err != nil {
		t.Fatalf("count nodes: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 nodes after deleting subscription, got %d", count)
	}
}

// ---------- 以下为补充测试 ----------

func newTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := New(":memory:")
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func baseNode(host string, port int, proto model.ProxyProtocol) *model.ProxyNode {
	return &model.ProxyNode{
		Host:      host,
		Port:      port,
		Protocol:  proto,
		Status:    model.StatusNew,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
}

func TestSaveAndGetNode(t *testing.T) {
	st := newTestStore(t)
	n := baseNode("1.1.1.1", 8080, model.ProtocolHTTP)
	added, err := st.SaveNode(n)
	if err != nil {
		t.Fatalf("save node: %v", err)
	}
	if !added {
		t.Fatal("expected node added")
	}
	if n.ID == 0 {
		t.Fatal("expected node ID assigned")
	}

	got, err := st.GetNode(n.ID)
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if got.Host != "1.1.1.1" || got.Port != 8080 || got.Protocol != model.ProtocolHTTP {
		t.Fatalf("unexpected node: %+v", got)
	}
	if got.Status != model.StatusNew {
		t.Fatalf("status = %q, want new", got.Status)
	}
}

func TestSaveNodeDeduplicates(t *testing.T) {
	st := newTestStore(t)
	n1 := baseNode("1.1.1.1", 8080, model.ProtocolHTTP)
	if added, _ := st.SaveNode(n1); !added {
		t.Fatal("expected first insert added")
	}
	// 相同 host/port/protocol 重复插入应返回 false 且不新增
	n2 := baseNode("1.1.1.1", 8080, model.ProtocolHTTP)
	if added, err := st.SaveNode(n2); err != nil {
		t.Fatalf("save node: %v", err)
	} else if added {
		t.Fatal("expected duplicate not added")
	}
	count, err := st.CountNodes()
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
}

func TestUpsertNodeUpdatesExisting(t *testing.T) {
	st := newTestStore(t)
	n := baseNode("1.1.1.1", 8080, model.ProtocolHTTP)
	if added, _ := st.SaveNode(n); !added {
		t.Fatal("expected added")
	}

	// 用同一 key 更新节点
	n.Score = 80
	n.Latency = 123
	n.Status = model.StatusAlive
	if err := st.UpsertNode(n); err != nil {
		t.Fatalf("upsert node: %v", err)
	}
	count, _ := st.CountNodes()
	if count != 1 {
		t.Fatalf("count = %d, want 1 after upsert", count)
	}

	got, _ := st.GetNode(n.ID)
	if got.Score != 80 || got.Latency != 123 || got.Status != model.StatusAlive {
		t.Fatalf("node not updated: %+v", got)
	}
}

func TestUpdateNodeCheck(t *testing.T) {
	st := newTestStore(t)
	n := baseNode("1.1.1.1", 8080, model.ProtocolHTTP)
	_, _ = st.SaveNode(n)

	if err := st.UpdateNodeCheck(n.ID, model.StatusAlive, 100, 90, true, "中国", "广东省", "深圳市"); err != nil {
		t.Fatalf("update check: %v", err)
	}
	got, _ := st.GetNode(n.ID)
	if got.Status != model.StatusAlive || got.Latency != 100 || got.Score != 90 {
		t.Fatalf("node after success: %+v", got)
	}
	if got.SuccessCount != 1 || got.FailCount != 0 {
		t.Fatalf("counters after success: %+v", got)
	}
	if got.Country != "中国" || got.Province != "广东省" || got.City != "深圳市" {
		t.Fatalf("geo fields after success: %+v", got)
	}

	if err := st.UpdateNodeCheck(n.ID, model.StatusDead, 0, 10, false, "", "", ""); err != nil {
		t.Fatalf("update check: %v", err)
	}
	got, _ = st.GetNode(n.ID)
	if got.Status != model.StatusDead {
		t.Fatalf("status = %q, want dead", got.Status)
	}
	if got.SuccessCount != 1 || got.FailCount != 1 {
		t.Fatalf("counters after failure: %+v", got)
	}
	// 地区字段在检测失败且无解析结果时应被清空
	if got.Country != "" {
		t.Fatalf("geo fields should be reset after failure: %+v", got)
	}
}

func TestDeleteNode(t *testing.T) {
	st := newTestStore(t)
	n := baseNode("1.1.1.1", 8080, model.ProtocolHTTP)
	_, _ = st.SaveNode(n)

	if err := st.DeleteNode(n.ID); err != nil {
		t.Fatalf("delete node: %v", err)
	}
	count, _ := st.CountNodes()
	if count != 0 {
		t.Fatalf("count = %d, want 0", count)
	}
	if _, err := st.GetNode(n.ID); err == nil {
		t.Fatal("expected error getting deleted node")
	}
}

func TestListNodeOrdering(t *testing.T) {
	st := newTestStore(t)
	low := baseNode("1.1.1.1", 80, model.ProtocolHTTP)
	low.Score = 10
	high := baseNode("2.2.2.2", 80, model.ProtocolHTTP)
	high.Score = 90
	_, _ = st.SaveNode(low)
	_, _ = st.SaveNode(high)

	nodes, err := st.ListNode()
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("len = %d, want 2", len(nodes))
	}
	// 按 score DESC 排序
	if nodes[0].Host != "2.2.2.2" || nodes[1].Host != "1.1.1.1" {
		t.Fatalf("unexpected order: %+v, %+v", nodes[0], nodes[1])
	}
}

func TestListNodesByStatus(t *testing.T) {
	st := newTestStore(t)
	alive := baseNode("1.1.1.1", 80, model.ProtocolHTTP)
	alive.Status = model.StatusAlive
	dead := baseNode("2.2.2.2", 80, model.ProtocolHTTP)
	dead.Status = model.StatusDead
	_, _ = st.SaveNode(alive)
	_, _ = st.SaveNode(dead)

	aliveNodes, err := st.ListNodesByStatus(model.StatusAlive)
	if err != nil {
		t.Fatalf("list alive: %v", err)
	}
	if len(aliveNodes) != 1 || aliveNodes[0].Host != "1.1.1.1" {
		t.Fatalf("alive nodes = %+v", aliveNodes)
	}
	deadNodes, _ := st.ListNodesByStatus(model.StatusDead)
	if len(deadNodes) != 1 || deadNodes[0].Host != "2.2.2.2" {
		t.Fatalf("dead nodes = %+v", deadNodes)
	}
}

func TestCountNodesByStatus(t *testing.T) {
	st := newTestStore(t)
	alive := baseNode("1.1.1.1", 80, model.ProtocolHTTP)
	alive.Status = model.StatusAlive
	_, _ = st.SaveNode(alive)
	_, _ = st.SaveNode(baseNode("2.2.2.2", 80, model.ProtocolHTTP))

	if c, _ := st.CountNodesByStatus(model.StatusAlive); c != 1 {
		t.Fatalf("alive count = %d, want 1", c)
	}
	if c, _ := st.CountNodesByStatus(model.StatusNew); c != 1 {
		t.Fatalf("new count = %d, want 1", c)
	}
	if c, _ := st.CountNodes(); c != 2 {
		t.Fatalf("total count = %d, want 2", c)
	}
}

func TestUpsertSubscriptionInserts(t *testing.T) {
	st := newTestStore(t)
	sub := &model.Subscription{Name: "sub", URL: "https://example.com", Interval: 3600, Enabled: true}
	if err := st.UpsertSubscription(sub); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if sub.ID == 0 {
		t.Fatal("expected subscription ID assigned")
	}
	subs, err := st.ListSubscriptions()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("len = %d, want 1", len(subs))
	}
	if subs[0].Name != "sub" || !subs[0].Enabled {
		t.Fatalf("unexpected sub: %+v", subs[0])
	}
}

func TestUpsertSubscriptionUpdatesWithoutNewRow(t *testing.T) {
	// 回归测试：更新订阅不应新增记录
	st := newTestStore(t)
	sub := &model.Subscription{Name: "sub", URL: "https://example.com", Interval: 3600, Enabled: true}
	if err := st.UpsertSubscription(sub); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if sub.ID == 0 {
		t.Fatal("expected subscription ID assigned")
	}
	firstID := sub.ID

	// 用同一 ID 更新
	sub.Name = "sub-updated"
	sub.URL = "https://new.example.com"
	sub.Interval = 1800
	sub.Enabled = false
	if err := st.UpsertSubscription(sub); err != nil {
		t.Fatalf("upsert update: %v", err)
	}

	subs, err := st.ListSubscriptions()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("expected 1 subscription after update, got %d", len(subs))
	}
	if subs[0].ID != firstID {
		t.Fatalf("expected ID %d preserved, got %d", firstID, subs[0].ID)
	}
	if subs[0].Name != "sub-updated" || subs[0].URL != "https://new.example.com" {
		t.Fatalf("subscription not updated: %+v", subs[0])
	}
	if subs[0].Interval != 1800 || subs[0].Enabled {
		t.Fatalf("fields not updated: %+v", subs[0])
	}
}

func TestUpsertSubscriptionPreservesLastFetchOnUpdate(t *testing.T) {
	st := newTestStore(t)
	sub := &model.Subscription{Name: "sub", URL: "https://example.com", Enabled: true}
	if err := st.UpsertSubscription(sub); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	fetchTime := time.Now().UTC().Add(-time.Hour)
	if err := st.UpdateSubscriptionFetch(sub.ID, fetchTime); err != nil {
		t.Fatalf("update fetch: %v", err)
	}

	// 更新订阅（LastFetch 为零值）不应清掉已记录的 last_fetch
	sub.Name = "renamed"
	sub.URL = "https://other.example.com"
	sub.Interval = 60
	sub.Enabled = false
	if err := st.UpsertSubscription(sub); err != nil {
		t.Fatalf("upsert update: %v", err)
	}

	subs, _ := st.ListSubscriptions()
	if len(subs) != 1 {
		t.Fatalf("expected 1 subscription, got %d", len(subs))
	}
	if !subs[0].LastFetch.Equal(fetchTime.Truncate(time.Second)) &&
		!subs[0].LastFetch.Equal(fetchTime) {
		t.Fatalf("expected last_fetch preserved, got %v", subs[0].LastFetch)
	}
}

func TestListSubscriptionsProxyCount(t *testing.T) {
	st := newTestStore(t)
	sub := &model.Subscription{Name: "sub", URL: "https://example.com", Enabled: true}
	if err := st.UpsertSubscription(sub); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// 关联 2 个节点到订阅
	for i := 0; i < 2; i++ {
		n := baseNode("1.1.1.1", 8080+i, model.ProtocolHTTP)
		_, _ = st.SaveNode(n)
		if err := st.AttachNodeToSubscription(n.ID, sub.ID); err != nil {
			t.Fatalf("attach: %v", err)
		}
	}

	subs, err := st.ListSubscriptions()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(subs) != 1 || subs[0].ProxyCount != 2 {
		t.Fatalf("proxyCount = %d, want 2 (subs=%+v)", subs[0].ProxyCount, subs)
	}
}

func TestUpdateSubscriptionFetch(t *testing.T) {
	st := newTestStore(t)
	sub := &model.Subscription{Name: "sub", URL: "https://example.com", Enabled: true}
	_ = st.UpsertSubscription(sub)

	fetchTime := time.Now().UTC()
	if err := st.UpdateSubscriptionFetch(sub.ID, fetchTime); err != nil {
		t.Fatalf("update fetch: %v", err)
	}
	subs, _ := st.ListSubscriptions()
	if subs[0].LastFetch.IsZero() {
		t.Fatal("expected last_fetch set")
	}
}

func TestDeleteSubscriptionKeepsOtherNodes(t *testing.T) {
	st := newTestStore(t)
	sub := &model.Subscription{Name: "sub", URL: "https://example.com", Enabled: true}
	_ = st.UpsertSubscription(sub)

	attached := baseNode("1.1.1.1", 80, model.ProtocolHTTP)
	_, _ = st.SaveNode(attached)
	_ = st.AttachNodeToSubscription(attached.ID, sub.ID)

	// 未关联订阅的节点应保留
	unattached := baseNode("2.2.2.2", 80, model.ProtocolHTTP)
	_, _ = st.SaveNode(unattached)

	if _, err := st.DeleteSubscription(sub.ID); err != nil {
		t.Fatalf("delete subscription: %v", err)
	}
	count, _ := st.CountNodes()
	if count != 1 {
		t.Fatalf("count = %d, want 1 (unattached node kept)", count)
	}
	nodes, _ := st.ListNode()
	if nodes[0].Host != "2.2.2.2" {
		t.Fatalf("unexpected remaining node: %+v", nodes[0])
	}
}

func TestAddCheckHistoryAndRecent(t *testing.T) {
	st := newTestStore(t)
	n := baseNode("1.1.1.1", 80, model.ProtocolHTTP)
	_, _ = st.SaveNode(n)

	for i := 0; i < 3; i++ {
		if err := st.AddCheckHistory(model.CheckHistory{ProxyID: n.ID, Success: true, Latency: 50}); err != nil {
			t.Fatalf("add history: %v", err)
		}
	}
	if err := st.AddCheckHistory(model.CheckHistory{ProxyID: n.ID, Success: false, Latency: 0, Error: "timeout"}); err != nil {
		t.Fatalf("add history: %v", err)
	}

	hist, err := st.RecentHistory(n.ID, 10)
	if err != nil {
		t.Fatalf("recent history: %v", err)
	}
	if len(hist) != 4 {
		t.Fatalf("len = %d, want 4", len(hist))
	}
	// 按 id DESC：最新一条在前
	if hist[0].Success {
		t.Fatalf("expected newest entry failed, got %+v", hist[0])
	}
	if !hist[1].Success || hist[1].Latency != 50 {
		t.Fatalf("unexpected entry: %+v", hist[1])
	}
}

func TestRecentHistoryLimit(t *testing.T) {
	st := newTestStore(t)
	n := baseNode("1.1.1.1", 80, model.ProtocolHTTP)
	_, _ = st.SaveNode(n)

	for i := 0; i < 10; i++ {
		_ = st.AddCheckHistory(model.CheckHistory{ProxyID: n.ID, Success: true})
	}
	hist, err := st.RecentHistory(n.ID, 3)
	if err != nil {
		t.Fatalf("recent history: %v", err)
	}
	if len(hist) != 3 {
		t.Fatalf("len = %d, want 3", len(hist))
	}
}

func TestDeleteNodeRemovesHistory(t *testing.T) {
	st := newTestStore(t)
	n := baseNode("1.1.1.1", 80, model.ProtocolHTTP)
	_, _ = st.SaveNode(n)
	_ = st.AddCheckHistory(model.CheckHistory{ProxyID: n.ID, Success: true})

	if err := st.DeleteNode(n.ID); err != nil {
		t.Fatalf("delete node: %v", err)
	}
	hist, _ := st.RecentHistory(n.ID, 10)
	if len(hist) != 0 {
		t.Fatalf("expected history removed, got %d entries", len(hist))
	}
}

func TestSaveNodeTimestampsDefaulted(t *testing.T) {
	st := newTestStore(t)
	// 不设置 CreatedAt/UpdatedAt
	n := &model.ProxyNode{Host: "1.1.1.1", Port: 8080, Protocol: model.ProtocolHTTP}
	if added, err := st.SaveNode(n); err != nil {
		t.Fatalf("save: %v", err)
	} else if !added {
		t.Fatal("expected added")
	}
	if n.CreatedAt.IsZero() || n.UpdatedAt.IsZero() {
		t.Fatal("expected timestamps defaulted")
	}
}

func TestSettingsGetSet(t *testing.T) {
	st := newTestStore(t)
	// 未设置时返回空
	v, err := st.GetSetting("some_setting")
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if v != "" {
		t.Errorf("GetSetting unset = %q, want empty", v)
	}

	if err := st.SetSetting("some_setting", "127.0.0.1:7999"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	v, err = st.GetSetting("some_setting")
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if v != "127.0.0.1:7999" {
		t.Errorf("GetSetting = %q, want 127.0.0.1:7999", v)
	}

	// 覆盖更新
	if err := st.SetSetting("some_setting", "127.0.0.1:8000"); err != nil {
		t.Fatalf("SetSetting update: %v", err)
	}
	v, _ = st.GetSetting("some_setting")
	if v != "127.0.0.1:8000" {
		t.Errorf("GetSetting after update = %q, want 127.0.0.1:8000", v)
	}
}

// ---------- 订阅-节点关联 ----------

// saveSub 创建订阅并返回。
func saveSub(t *testing.T, st *Store, name string) *model.Subscription {
	t.Helper()
	sub := &model.Subscription{Name: name, URL: "https://example.com/" + name, Enabled: true, CreatedAt: time.Now().UTC()}
	if err := st.UpsertSubscription(sub); err != nil {
		t.Fatalf("upsert subscription: %v", err)
	}
	return sub
}

// saveNode 创建节点并返回。
func saveNode(t *testing.T, st *Store, host string, port int) *model.ProxyNode {
	t.Helper()
	n := baseNode(host, port, model.ProtocolHTTP)
	if added, err := st.SaveNode(n); err != nil {
		t.Fatalf("save node: %v", err)
	} else if !added {
		t.Fatal("expected node to be added")
	}
	return n
}

func TestListNodesBySubscription(t *testing.T) {
	st := newTestStore(t)
	sub := saveSub(t, st, "sub-a")
	other := saveSub(t, st, "sub-b")

	n1 := saveNode(t, st, "1.1.1.1", 8080)
	n2 := saveNode(t, st, "2.2.2.2", 8081)
	n3 := saveNode(t, st, "3.3.3.3", 8082)

	if err := st.AttachNodeToSubscription(n1.ID, sub.ID); err != nil {
		t.Fatalf("attach n1: %v", err)
	}
	if err := st.AttachNodeToSubscription(n2.ID, sub.ID); err != nil {
		t.Fatalf("attach n2: %v", err)
	}
	if err := st.AttachNodeToSubscription(n3.ID, other.ID); err != nil {
		t.Fatalf("attach n3: %v", err)
	}

	nodes, err := st.ListNodesBySubscription(sub.ID)
	if err != nil {
		t.Fatalf("ListNodesBySubscription: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(nodes))
	}
	hosts := map[string]bool{}
	for _, n := range nodes {
		hosts[n.Host] = true
	}
	if !hosts["1.1.1.1"] || !hosts["2.2.2.2"] {
		t.Errorf("hosts = %v, want 1.1.1.1 and 2.2.2.2", hosts)
	}

	// 无关联的订阅返回空
	empty, err := st.ListNodesBySubscription(99999)
	if err != nil {
		t.Fatalf("ListNodesBySubscription empty: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("empty nodes = %d, want 0", len(empty))
	}
}

func TestDetachNodeFromSubscription(t *testing.T) {
	st := newTestStore(t)
	sub := saveSub(t, st, "sub-a")
	n1 := saveNode(t, st, "1.1.1.1", 8080)

	if err := st.AttachNodeToSubscription(n1.ID, sub.ID); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := st.DetachNodeFromSubscription(n1.ID, sub.ID); err != nil {
		t.Fatalf("detach: %v", err)
	}

	nodes, err := st.ListNodesBySubscription(sub.ID)
	if err != nil {
		t.Fatalf("ListNodesBySubscription: %v", err)
	}
	if len(nodes) != 0 {
		t.Errorf("nodes after detach = %d, want 0", len(nodes))
	}
	// 节点本身仍存在
	count, err := st.CountNodes()
	if err != nil {
		t.Fatalf("CountNodes: %v", err)
	}
	if count != 1 {
		t.Errorf("CountNodes = %d, want 1 (node kept)", count)
	}
}

func TestCountSubscriptionRefs(t *testing.T) {
	st := newTestStore(t)
	subA := saveSub(t, st, "sub-a")
	subB := saveSub(t, st, "sub-b")
	n1 := saveNode(t, st, "1.1.1.1", 8080)

	// 未关联时 0
	c, err := st.CountSubscriptionRefs(n1.ID)
	if err != nil {
		t.Fatalf("CountSubscriptionRefs: %v", err)
	}
	if c != 0 {
		t.Errorf("refs = %d, want 0", c)
	}

	// 关联两个订阅后 2
	if err := st.AttachNodeToSubscription(n1.ID, subA.ID); err != nil {
		t.Fatalf("attach A: %v", err)
	}
	if err := st.AttachNodeToSubscription(n1.ID, subB.ID); err != nil {
		t.Fatalf("attach B: %v", err)
	}
	c, err = st.CountSubscriptionRefs(n1.ID)
	if err != nil {
		t.Fatalf("CountSubscriptionRefs: %v", err)
	}
	if c != 2 {
		t.Errorf("refs = %d, want 2", c)
	}

	// 解除一个后 1
	if err := st.DetachNodeFromSubscription(n1.ID, subA.ID); err != nil {
		t.Fatalf("detach A: %v", err)
	}
	c, _ = st.CountSubscriptionRefs(n1.ID)
	if c != 1 {
		t.Errorf("refs after detach = %d, want 1", c)
	}
}

// 旧版数据库（没有地区列）在 storage.New 打开时应自动 ALTER 补列，且能正常读写地区字段。
func TestMigrateAddsGeoColumnsToLegacyDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")

	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	_, err = legacy.Exec(`CREATE TABLE proxy_nodes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		host TEXT NOT NULL,
		port INTEGER NOT NULL,
		protocol TEXT NOT NULL,
		username TEXT NOT NULL DEFAULT '',
		password TEXT NOT NULL DEFAULT '',
		latency INTEGER NOT NULL DEFAULT 0,
		score INTEGER NOT NULL DEFAULT 0,
		status TEXT NOT NULL DEFAULT 'new',
		success_count INTEGER NOT NULL DEFAULT 0,
		fail_count INTEGER NOT NULL DEFAULT 0,
		last_check DATETIME,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL,
		UNIQUE(host, port, protocol)
	)`)
	if err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	_, err = legacy.Exec(`INSERT INTO proxy_nodes
		(host, port, protocol, latency, score, status, created_at, updated_at)
		VALUES ('9.9.9.9', 443, 'http', 10, 99, 'alive', datetime('now'), datetime('now'))`)
	if err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	st, err := New(path)
	if err != nil {
		t.Fatalf("open legacy db with migrate: %v", err)
	}
	defer func() { _ = st.Close() }()

	nodes, err := st.ListNode()
	if err != nil {
		t.Fatalf("ListNode after migrate: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(nodes))
	}

	// 补列后可正常写入并读回地区字段
	node := nodes[0]
	if err := st.UpdateNodeCheck(node.ID, model.StatusAlive, 50, 80, true, "中国", "香港", "香港"); err != nil {
		t.Fatalf("UpdateNodeCheck after migrate: %v", err)
	}
	got, err := st.GetNode(node.ID)
	if err != nil {
		t.Fatalf("GetNode after migrate: %v", err)
	}
	if got.Country != "中国" || got.Province != "香港" {
		t.Fatalf("geo fields after migrate: %+v", got)
	}
}
