package metrics

import (
	"testing"

	"github.com/axetroy/ProxyPilot/proxy-core/bus"
	"github.com/axetroy/ProxyPilot/proxy-core/gateway"
	"github.com/axetroy/ProxyPilot/proxy-core/model"
	"github.com/axetroy/ProxyPilot/proxy-core/pool"
	"github.com/axetroy/ProxyPilot/proxy-core/scheduler"
	"github.com/axetroy/ProxyPilot/proxy-core/storage"
	"github.com/prometheus/client_golang/prometheus"
)

// countSamples 统计默认 registry 中指定指标名的样本数。
func countSamples(t *testing.T, name string) int {
	t.Helper()
	fams, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, f := range fams {
		if f.GetName() == name {
			return len(f.Metric)
		}
	}
	return 0
}

// TestSnapshotPoolRemovesStaleSeries 锁定防泄漏语义：
// 节点删除后，下一轮快照必须清掉该节点的 per-node 序列，
// 否则长期运行时指标序列会随节点增删不断累积（内存泄漏）。
func TestSnapshotPoolRemovesStaleSeries(t *testing.T) {
	st, err := storage.New(":memory:")
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	defer func() { _ = st.Close() }()

	pm := pool.NewManager(st, nil, bus.New(), 1)
	pm.AddNodes([]*model.ProxyNode{
		{Host: "10.0.0.1", Port: 80, Protocol: model.ProtocolHTTP, Status: model.StatusAlive, Score: 10, Latency: 1},
		{Host: "10.0.0.2", Port: 80, Protocol: model.ProtocolHTTP, Status: model.StatusAlive, Score: 20, Latency: 2},
	})

	m := NewManager(pm, nil, nil, st, bus.New())
	m.snapshotPool()
	if got := countSamples(t, "proxypilot_pool_node_score"); got != 2 {
		t.Fatalf("after first snapshot: score samples = %d, want 2", got)
	}

	// 删除一个节点后再快照：旧序列必须消失（依赖 snapshot 内的 Reset）
	var idB int64
	for _, n := range pm.List() {
		if n.Host == "10.0.0.2" {
			idB = n.ID
		}
	}
	pm.RemoveNodes([]int64{idB})
	m.snapshotPool()

	if got := countSamples(t, "proxypilot_pool_node_score"); got != 1 {
		t.Fatalf("after removal + snapshot: score samples = %d, want 1 (stale series must be reset)", got)
	}
	if got := countSamples(t, "proxypilot_pool_node_latency_ms"); got != 1 {
		t.Fatalf("latency samples = %d, want 1", got)
	}
}

// TestSnapshotPoolStatusCounts 验证按状态计数随池变化正确重建。
func TestSnapshotPoolStatusCounts(t *testing.T) {
	st, err := storage.New(":memory:")
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	defer func() { _ = st.Close() }()

	pm := pool.NewManager(st, nil, bus.New(), 1)
	pm.AddNodes([]*model.ProxyNode{
		{Host: "10.1.0.1", Port: 80, Protocol: model.ProtocolHTTP, Status: model.StatusAlive, Score: 10, Latency: 1},
		{Host: "10.1.0.2", Port: 80, Protocol: model.ProtocolHTTP, Status: model.StatusDead, Score: 0, Latency: 0},
	})

	m := NewManager(pm, nil, nil, st, bus.New())
	m.snapshotPool()
	if got := countSamples(t, "proxypilot_pool_nodes_total"); got != 2 {
		t.Fatalf("status series = %d, want 2 (alive+dead)", got)
	}
}

// TestCoreMetricNamesPresent 指标名契约：核心指标必须在快照后出现。
// 前端（app/src/lib/prometheus-parser.ts 的 extractOverview）按名匹配，
// 后端改名会导致前端统计静默归零——改名时本测试与前端需同步修改。
func TestCoreMetricNamesPresent(t *testing.T) {
	st, err := storage.New(":memory:")
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	defer func() { _ = st.Close() }()

	pm := pool.NewManager(st, nil, bus.New(), 1)
	pm.AddNodes([]*model.ProxyNode{
		{Host: "10.2.0.1", Port: 80, Protocol: model.ProtocolHTTP, Status: model.StatusAlive, Score: 50, Latency: 100},
	})
	sel := scheduler.NewSelector(pm)
	gw := gateway.NewGateway(pm, sel, bus.New(), "127.0.0.1:0")

	m := NewManager(pm, sel, gw, st, bus.New())
	m.snapshot()

	fams, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	got := map[string]bool{}
	for _, f := range fams {
		if name := f.GetName(); len(name) > len("proxypilot_") && name[:len("proxypilot_")] == "proxypilot_" {
			got[name] = true
		}
	}

	coreNames := []string{
		"proxypilot_pool_nodes_total",
		"proxypilot_pool_node_score",
		"proxypilot_pool_node_latency_ms",
		"proxypilot_gateway_traffic_upload_bytes",
		"proxypilot_gateway_traffic_download_bytes",
		"proxypilot_gateway_connections",
		"proxypilot_gateway_active_connections",
		"proxypilot_gateway_active_udp_associates",
		"proxypilot_selector_strategy",
		"proxypilot_system_goroutines",
		"proxypilot_system_memory_alloc_bytes",
		"proxypilot_system_uptime_seconds",
	}
	for _, want := range coreNames {
		if !got[want] {
			t.Errorf("metric %q missing after snapshot (renamed? sync frontend parser)", want)
		}
	}
}
