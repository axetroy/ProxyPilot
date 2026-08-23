// Package metrics 提供 proxy-core 的 Prometheus 指标端点（/metrics）。
//
// 采用「定时快照」模型：Manager 每 15 秒从各模块读取一次状态，
// 全量重建各指标序列；promhttp 直接暴露默认 registry。
package metrics

import (
	"net/http"
	"runtime"
	"strconv"
	"time"

	"github.com/axetroy/ProxyPilot/proxy-core/bus"
	"github.com/axetroy/ProxyPilot/proxy-core/gateway"
	"github.com/axetroy/ProxyPilot/proxy-core/pool"
	"github.com/axetroy/ProxyPilot/proxy-core/scheduler"
	"github.com/axetroy/ProxyPilot/proxy-core/storage"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	startTime = time.Now()

	// ---- 代理池 ----
	poolNodesTotal = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "proxypilot_pool_nodes_total",
		Help: "代理池节点数（按状态）",
	}, []string{"status"})

	poolNodeScore = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "proxypilot_pool_node_score",
		Help: "存活节点的质量评分（0-100）",
	}, []string{"id", "host", "protocol"})

	poolNodeLatency = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "proxypilot_pool_node_latency_ms",
		Help: "存活节点最近一次检测延迟（毫秒）",
	}, []string{"id", "host", "protocol"})

	poolNodeSafety = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "proxypilot_pool_node_safety_score",
		Help: "存活节点连接安全评分（0-100，探测成功时有效）",
	}, []string{"id", "host"})

	// ---- 网关 ----
	// 流量为「本次启动累计值」，用 Gauge 表达（单进程场景下 rate() 差值同样可用）；
	// 不使用 Counter.Add(累计值)，避免快照重建时重复累加。
	gatewayTrafficUpload = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "proxypilot_gateway_traffic_upload_bytes",
		Help: "网关上行流量累计（字节，本次启动至今，按出口分桶）",
	}, []string{"kind", "identifier"})

	gatewayTrafficDownload = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "proxypilot_gateway_traffic_download_bytes",
		Help: "网关下行流量累计（字节，本次启动至今，按出口分桶）",
	}, []string{"kind", "identifier"})

	gatewayConnections = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "proxypilot_gateway_connections",
		Help: "网关连接数累计（本次启动至今，按出口分桶）",
	}, []string{"kind", "identifier"})

	gatewayActiveConns = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "proxypilot_gateway_active_connections",
		Help: "当前处理的客户端连接数",
	})

	gatewayUDPActive = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "proxypilot_gateway_active_udp_associates",
		Help: "当前活跃的 SOCKS5 UDP ASSOCIATE 会话数",
	})

	// ---- 链路健康 ----
	chainHealthResult = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "proxypilot_chain_health_result",
		Help: "链路最近一次健康检测结果（success=1 / failure=0，未检测不输出）",
	}, []string{"chain_id", "chain_name"})

	chainHealthLatency = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "proxypilot_chain_health_latency_ms",
		Help: "链路最近一次健康检测整链耗时（毫秒）",
	}, []string{"chain_id", "chain_name"})

	chainConsecutiveFailures = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "proxypilot_chain_consecutive_failures",
		Help: "链路连续失败次数",
	}, []string{"chain_id", "chain_name"})

	// ---- 出口策略 ----
	selectorStrategy = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "proxypilot_selector_strategy",
		Help: "当前出口策略（仅当前策略输出 1）",
	}, []string{"strategy"})

	// ---- 系统 ----
	systemGoroutines = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "proxypilot_system_goroutines",
		Help: "当前 goroutine 数量",
	})

	systemMemoryAlloc = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "proxypilot_system_memory_alloc_bytes",
		Help: "堆内存分配量（字节）",
	})

	systemUptime = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "proxypilot_system_uptime_seconds",
		Help: "proxy-core 运行时长（秒）",
	})
)

// knownStrategies 用于策略指标的标签枚举（与 scheduler.Strategy 常量一致）。
var knownStrategies = []scheduler.Strategy{
	scheduler.StrategyFixed,
	scheduler.StrategyBest,
	scheduler.StrategyRandom,
	scheduler.StrategyWeighted,
	scheduler.StrategyRoundRobin,
	scheduler.StrategyChain,
	scheduler.StrategyAutoChain,
}

// Manager 定时从各模块采集快照并写入指标。
type Manager struct {
	pool  *pool.Manager
	sel   *scheduler.Selector
	gw    *gateway.Gateway
	store *storage.Store
	bus   *bus.Bus

	stopCh chan struct{}
}

// NewManager 创建指标管理器。Start 后每 15 秒采集一次。
func NewManager(poolMgr *pool.Manager, sel *scheduler.Selector, gw *gateway.Gateway, store *storage.Store, b *bus.Bus) *Manager {
	return &Manager{
		pool:   poolMgr,
		sel:    sel,
		gw:     gw,
		store:  store,
		bus:    b,
		stopCh: make(chan struct{}),
	}
}

// Start 启动定时采集循环；立即执行一次以尽早产出数据。
func (m *Manager) Start() {
	go func() {
		m.snapshot()
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				m.snapshot()
			case <-m.stopCh:
				return
			}
		}
	}()
}

// Stop 停止采集循环。
func (m *Manager) Stop() {
	select {
	case <-m.stopCh:
	default:
		close(m.stopCh)
	}
}

// snapshot 采集一轮全量状态。per-node/per-chain/per-strategy 序列先 Reset 再重建，
// 保证已删除对象的旧序列不会残留。
func (m *Manager) snapshot() {
	defer func() {
		// 单次采集失败不应影响后续轮次与 API 服务。
		if r := recover(); r != nil {
			if m.bus != nil {
				m.bus.Debug("metrics snapshot panic recovered")
			}
		}
	}()

	m.snapshotPool()
	m.snapshotGateway()
	m.snapshotChains()
	m.snapshotSelector()
	m.snapshotSystem()
}

func (m *Manager) snapshotPool() {
	if m.pool == nil {
		return
	}
	nodes := m.pool.List()

	counts := make(map[string]int)
	poolNodeScore.Reset()
	poolNodeLatency.Reset()
	poolNodeSafety.Reset()
	for _, n := range nodes {
		counts[string(n.Status)]++
		if n.Status != "alive" {
			continue
		}
		id, host, proto := itoa(n.ID), n.Host, string(n.Protocol)
		poolNodeScore.WithLabelValues(id, host, proto).Set(float64(n.Score))
		poolNodeLatency.WithLabelValues(id, host, proto).Set(float64(n.Latency))
		if n.SafetyDetail != nil {
			poolNodeSafety.WithLabelValues(id, host).Set(float64(n.SafetyDetail.Score))
		}
	}
	poolNodesTotal.Reset()
	for status, count := range counts {
		poolNodesTotal.WithLabelValues(status).Set(float64(count))
	}
}

func (m *Manager) snapshotGateway() {
	if m.gw == nil {
		return
	}
	snap := m.gw.Traffic()

	gatewayTrafficUpload.Reset()
	gatewayTrafficDownload.Reset()
	gatewayConnections.Reset()
	setEntry := func(kind, id string, upload, download, connections int64) {
		gatewayTrafficUpload.WithLabelValues(kind, id).Set(float64(upload))
		gatewayTrafficDownload.WithLabelValues(kind, id).Set(float64(download))
		gatewayConnections.WithLabelValues(kind, id).Set(float64(connections))
	}
	for _, item := range snap.ByNode {
		setEntry("node", itoa(item.ID), item.Upload, item.Download, item.Connections)
	}
	for _, item := range snap.ByChain {
		setEntry("chain", item.Name, item.Upload, item.Download, item.Connections)
	}
	setEntry("direct", "", snap.Direct.Upload, snap.Direct.Download, snap.Direct.Connections)

	gatewayActiveConns.Set(float64(m.gw.ActiveConnCount()))
	gatewayUDPActive.Set(float64(m.gw.UDPActiveCount()))
}

func (m *Manager) snapshotChains() {
	if m.store == nil {
		return
	}
	chains, err := m.store.ListChains()
	if err != nil {
		return
	}
	chainHealthResult.Reset()
	chainHealthLatency.Reset()
	chainConsecutiveFailures.Reset()
	for _, c := range chains {
		id, name := itoa(c.ID), c.Name
		chainConsecutiveFailures.WithLabelValues(id, name).Set(float64(c.ConsecutiveFailures))
		if c.LastCheckedAt == nil {
			continue // 未检测过：结果/延迟不输出
		}
		v := 0.0
		if c.LastOK {
			v = 1
		}
		chainHealthResult.WithLabelValues(id, name).Set(v)
		chainHealthLatency.WithLabelValues(id, name).Set(float64(c.LastLatency))
	}
}

func (m *Manager) snapshotSelector() {
	if m.sel == nil {
		return
	}
	cur := m.sel.Strategy()
	selectorStrategy.Reset()
	for _, s := range knownStrategies {
		if s == cur {
			selectorStrategy.WithLabelValues(string(s)).Set(1)
		}
	}
}

func (m *Manager) snapshotSystem() {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	systemGoroutines.Set(float64(runtime.NumGoroutine()))
	systemMemoryAlloc.Set(float64(ms.Alloc))
	systemUptime.Set(time.Since(startTime).Seconds())
}

// Handler 返回 Prometheus HTTP 处理器（暴露默认 registry）。
func Handler() http.Handler {
	return promhttp.Handler()
}

// itoa 避免各处 strconv.FormatInt 样板。
func itoa(v int64) string {
	return strconv.FormatInt(v, 10)
}
