package metrics

import (
	"net/http"
	"runtime"
	"time"

	"github.com/axetroy/ProxyPilot/proxy-core/bus"
	"github.com/axetroy/ProxyPilot/proxy-core/chainhealth"
	"github.com/axetroy/ProxyPilot/proxy-core/collector"
	"github.com/axetroy/ProxyPilot/proxy-core/gateway"
	"github.com/axetroy/ProxyPilot/proxy-core/pool"
	"github.com/axetroy/ProxyPilot/proxy-core/scheduler"
	"github.com/axetroy/ProxyPilot/proxy-core/storage"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// Proxy pool metrics
	poolNodesTotal = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "proxypilot_pool_nodes_total",
		Help: "Total number of nodes in the pool by status",
	}, []string{"status"})

	poolNodeScore = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "proxypilot_pool_node_score",
		Help: "Node score",
	}, []string{"id", "host", "protocol"})

	poolNodeLatency = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "proxypilot_pool_node_latency_ms",
		Help: "Node latency in milliseconds",
	}, []string{"id", "host", "protocol"})

	poolNodeSafety = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "proxypilot_pool_node_safety_score",
		Help: "Node connection safety score (0-100)",
	}, []string{"id", "host"})

	// Check metrics
	checkDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "proxypilot_check_duration_seconds",
		Help:    "Node check duration in seconds",
		Buckets: prometheus.DefBuckets,
	}, []string{"protocol", "result"})

	checkTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "proxypilot_check_total",
		Help: "Total number of node checks",
	}, []string{"protocol", "result"})

	// Gateway traffic metrics
	gatewayTrafficUpload = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "proxypilot_gateway_traffic_upload_bytes_total",
		Help: "Total upload bytes through gateway",
	}, []string{"kind", "identifier"})

	gatewayTrafficDownload = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "proxypilot_gateway_traffic_download_bytes_total",
		Help: "Total download bytes through gateway",
	}, []string{"kind", "identifier"})

	gatewayConnections = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "proxypilot_gateway_connections_total",
		Help: "Total connections through gateway",
	}, []string{"kind", "identifier"})

	gatewayActiveConns = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "proxypilot_gateway_active_connections",
		Help: "Current active connections",
	})

	gatewayUDPActive = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "proxypilot_gateway_active_udp_associates",
		Help: "Current active UDP associates",
	})

	// Chain health metrics
	chainHealthTotal = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "proxypilot_chain_health_total",
		Help: "Chain health check results",
	}, []string{"chain_id", "chain_name", "result"})

	chainHealthLatency = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "proxypilot_chain_health_latency_ms",
		Help: "Chain health check latency in milliseconds",
	}, []string{"chain_id", "chain_name"})

	chainConsecutiveFailures = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "proxypilot_chain_consecutive_failures",
		Help: "Chain consecutive failures",
	}, []string{"chain_id", "chain_name"})

	// Subscription metrics
	subscriptionFetchTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "proxypilot_subscription_fetch_total",
		Help: "Total subscription fetch attempts",
	}, []string{"subscription_id", "subscription_name", "result"})

	subscriptionNodesAdded = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "proxypilot_subscription_nodes_added_total",
		Help: "Total nodes added from subscription",
	}, []string{"subscription_id"})

	subscriptionNodesRemoved = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "proxypilot_subscription_nodes_removed_total",
		Help: "Total nodes removed from subscription",
	}, []string{"subscription_id"})

	subscriptionFetchDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "proxypilot_subscription_fetch_duration_seconds",
		Help:    "Subscription fetch duration in seconds",
		Buckets: prometheus.DefBuckets,
	}, []string{"subscription_id"})

	// Selector metrics
	selectorStrategy = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "proxypilot_selector_strategy",
		Help: "Current selector strategy (1 = active)",
	}, []string{"strategy"})

	selectorFailures = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "proxypilot_selector_node_failures",
		Help: "Node consecutive failures in selector",
	}, []string{"node_id"})

	// API metrics
	apiRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "proxypilot_api_requests_total",
		Help: "Total API requests",
	}, []string{"method", "path", "status"})

	apiRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "proxypilot_api_request_duration_seconds",
		Help:    "API request duration in seconds",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path"})

	// System metrics
	systemGoroutines = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "proxypilot_system_goroutines",
		Help: "Number of goroutines",
	})

	systemMemoryAlloc = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "proxypilot_system_memory_alloc_bytes",
		Help: "Memory allocated in bytes",
	})

	systemMemorySys = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "proxypilot_system_memory_sys_bytes",
		Help: "Memory system in bytes",
	})

	systemUptime = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "proxypilot_system_uptime_seconds",
		Help: "System uptime in seconds",
	})

	startTime = time.Now()
)

// Collector 收集器接口，各模块实现以暴露指标
type Collector interface {
	Describe(ch chan<- *prometheus.Desc)
	Collect(ch chan<- prometheus.Metric)
}

// Manager 指标管理器，聚合各模块收集器
type Manager struct {
	poolMgr       *pool.Manager
	selector      *scheduler.Selector
	gateway       *gateway.Gateway
	chainHealth   *chainhealth.Manager
	chainStore    *storage.Store
	subCollector  *collector.Manager
	bus           *bus.Bus
	collectors    []Collector
	stopCh        chan struct{}
	updateTicker  *time.Ticker
}

// NewManager 创建指标管理器
func NewManager(
	poolMgr *pool.Manager,
	selector *scheduler.Selector,
	gateway *gateway.Gateway,
	chainHealth *chainhealth.Manager,
	chainStore *storage.Store,
	subCollector *collector.Manager,
	bus *bus.Bus,
) *Manager {
	m := &Manager{
		poolMgr:      poolMgr,
		selector:     selector,
		gateway:      gateway,
		chainHealth:  chainHealth,
		chainStore:   chainStore,
		subCollector: subCollector,
		bus:          bus,
		stopCh:       make(chan struct{}),
	}
	m.registerCollectors()
	return m
}

func (m *Manager) registerCollectors() {
	m.collectors = []Collector{
		&poolCollector{m: m},
		&gatewayCollector{m: m},
		&chainHealthCollector{m: m},
		&selectorCollector{m: m},
		&subscriptionCollector{m: m},
		&systemCollector{},
	}
}

// Start 启动定期更新（每 15 秒）
func (m *Manager) Start() {
	m.updateTicker = time.NewTicker(15 * time.Second)
	go m.run()
}

func (m *Manager) run() {
	m.update()
	for {
		select {
		case <-m.updateTicker.C:
			m.update()
		case <-m.stopCh:
			return
		}
	}
}

func (m *Manager) update() {
	for range m.collectors {
		// Each collector updates its own metrics
	}
}

// Stop 停止指标更新
func (m *Manager) Stop() {
	if m.updateTicker != nil {
		m.updateTicker.Stop()
	}
	close(m.stopCh)
}

// Handler 返回 Prometheus HTTP 处理器
func Handler() http.Handler {
	return promhttp.Handler()
}

// RecordAPIRequest 记录 API 请求（供中间件调用）
func RecordAPIRequest(method, path string, status int, duration time.Duration) {
	apiRequestsTotal.WithLabelValues(method, path, string(rune(status))).Inc()
	apiRequestDuration.WithLabelValues(method, path).Observe(duration.Seconds())
}

// RecordCheck 记录节点检测结果
func RecordCheck(protocol string, ok bool, duration time.Duration) {
	result := "success"
	if !ok {
		result = "failure"
	}
	checkTotal.WithLabelValues(protocol, result).Inc()
	checkDuration.WithLabelValues(protocol, result).Observe(duration.Seconds())
}

// RecordSubscriptionFetch 记录订阅抓取
func RecordSubscriptionFetch(subID int64, subName string, ok bool, duration time.Duration, added, removed int) {
	result := "success"
	if !ok {
		result = "failure"
	}
	subscriptionFetchTotal.WithLabelValues(string(rune(subID)), subName, result).Inc()
	subscriptionFetchDuration.WithLabelValues(string(rune(subID))).Observe(duration.Seconds())
	if added > 0 {
		subscriptionNodesAdded.WithLabelValues(string(rune(subID))).Add(float64(added))
	}
	if removed > 0 {
		subscriptionNodesRemoved.WithLabelValues(string(rune(subID))).Add(float64(removed))
	}
}

// poolCollector 代理池指标收集
type poolCollector struct {
	m *Manager
}

func (c *poolCollector) Describe(ch chan<- *prometheus.Desc) {
	poolNodesTotal.Describe(ch)
	poolNodeScore.Describe(ch)
	poolNodeLatency.Describe(ch)
	poolNodeSafety.Describe(ch)
}

func (c *poolCollector) Collect(ch chan<- prometheus.Metric) {
	if c.m.poolMgr == nil {
		return
	}
	nodes := c.m.poolMgr.List()
	statusCount := map[string]int{}
	for _, n := range nodes {
		statusCount[string(n.Status)]++
		poolNodeScore.WithLabelValues(string(rune(n.ID)), n.Host, string(n.Protocol)).Set(float64(n.Score))
		poolNodeLatency.WithLabelValues(string(rune(n.ID)), n.Host, string(n.Protocol)).Set(float64(n.Latency))
		if n.ScoreBreakdown != nil {
			poolNodeSafety.WithLabelValues(string(rune(n.ID)), n.Host).Set(float64(n.ScoreBreakdown.Safety))
		} else if n.SafetyDetail != nil {
			poolNodeSafety.WithLabelValues(string(rune(n.ID)), n.Host).Set(float64(n.SafetyDetail.Score))
		}
	}
	for status, count := range statusCount {
		poolNodesTotal.WithLabelValues(status).Set(float64(count))
	}
	poolNodesTotal.Collect(ch)
	poolNodeScore.Collect(ch)
	poolNodeLatency.Collect(ch)
	poolNodeSafety.Collect(ch)
}

// gatewayCollector 网关指标收集
type gatewayCollector struct {
	m *Manager
}

func (c *gatewayCollector) Describe(ch chan<- *prometheus.Desc) {
	gatewayTrafficUpload.Describe(ch)
	gatewayTrafficDownload.Describe(ch)
	gatewayConnections.Describe(ch)
	gatewayActiveConns.Describe(ch)
	gatewayUDPActive.Describe(ch)
}

func (c *gatewayCollector) Collect(ch chan<- prometheus.Metric) {
	if c.m.gateway == nil {
		return
	}
	// Traffic snapshot
	snap := c.m.gateway.Traffic()
	for _, item := range snap.ByNode {
		gatewayTrafficUpload.WithLabelValues("node", string(rune(item.ID))).Add(float64(item.Upload))
		gatewayTrafficDownload.WithLabelValues("node", string(rune(item.ID))).Add(float64(item.Download))
		gatewayConnections.WithLabelValues("node", string(rune(item.ID))).Add(float64(item.Connections))
	}
	for _, item := range snap.ByChain {
		gatewayTrafficUpload.WithLabelValues("chain", item.Name).Add(float64(item.Upload))
		gatewayTrafficDownload.WithLabelValues("chain", item.Name).Add(float64(item.Download))
		gatewayConnections.WithLabelValues("chain", item.Name).Add(float64(item.Connections))
	}
	gatewayTrafficUpload.WithLabelValues("direct", "").Add(float64(snap.Direct.Upload))
	gatewayTrafficDownload.WithLabelValues("direct", "").Add(float64(snap.Direct.Download))
	gatewayConnections.WithLabelValues("direct", "").Add(float64(snap.Direct.Connections))

	// Active connections (approximate from traffic counter)
	// Note: gateway doesn't expose active count directly, using 0 as placeholder
	gatewayActiveConns.Set(0)
	gatewayUDPActive.Set(0)

	gatewayTrafficUpload.Collect(ch)
	gatewayTrafficDownload.Collect(ch)
	gatewayConnections.Collect(ch)
	gatewayActiveConns.Collect(ch)
	gatewayUDPActive.Collect(ch)
}

// chainHealthCollector 链路健康指标收集
type chainHealthCollector struct {
	m *Manager
}

func (c *chainHealthCollector) Describe(ch chan<- *prometheus.Desc) {
	chainHealthTotal.Describe(ch)
	chainHealthLatency.Describe(ch)
	chainConsecutiveFailures.Describe(ch)
}

func (c *chainHealthCollector) Collect(ch chan<- prometheus.Metric) {
	chains, err := c.m.chainStore.ListChains()
	if err != nil {
		return
	}
	for _, ch := range chains {
		result := "unknown"
		if ch.LastCheckedAt != nil {
			if ch.LastOK {
				result = "success"
			} else {
				result = "failure"
			}
		}
		chainHealthTotal.WithLabelValues(string(rune(ch.ID)), ch.Name, result).Set(1)
		chainHealthLatency.WithLabelValues(string(rune(ch.ID)), ch.Name).Set(float64(ch.LastLatency))
		chainConsecutiveFailures.WithLabelValues(string(rune(ch.ID)), ch.Name).Set(float64(ch.ConsecutiveFailures))
	}
	chainHealthTotal.Collect(ch)
	chainHealthLatency.Collect(ch)
	chainConsecutiveFailures.Collect(ch)
}

// selectorCollector 选择器指标收集
type selectorCollector struct {
	m *Manager
}

func (c *selectorCollector) Describe(ch chan<- *prometheus.Desc) {
	selectorStrategy.Describe(ch)
	selectorFailures.Describe(ch)
}

func (c *selectorCollector) Collect(ch chan<- prometheus.Metric) {
	if c.m.selector == nil {
		return
	}
	strategy := c.m.selector.Strategy()
	for _, s := range []scheduler.Strategy{
		scheduler.StrategyFixed, scheduler.StrategyBest, scheduler.StrategyRandom,
		scheduler.StrategyWeighted, scheduler.StrategyRoundRobin, scheduler.StrategyChain,
		scheduler.StrategyAutoChain,
	} {
		if s == strategy {
			selectorStrategy.WithLabelValues(string(s)).Set(1)
		} else {
			selectorStrategy.WithLabelValues(string(s)).Set(0)
		}
	}
	selectorStrategy.Collect(ch)
	selectorFailures.Collect(ch)
}

// subscriptionCollector 订阅指标收集
type subscriptionCollector struct {
	m *Manager
}

func (c *subscriptionCollector) Describe(ch chan<- *prometheus.Desc) {
	subscriptionFetchTotal.Describe(ch)
	subscriptionNodesAdded.Describe(ch)
	subscriptionNodesRemoved.Describe(ch)
	subscriptionFetchDuration.Describe(ch)
}

func (*subscriptionCollector) Collect(ch chan<- prometheus.Metric) {
	// Metrics are updated via RecordSubscriptionFetch calls
	subscriptionFetchTotal.Collect(ch)
	subscriptionNodesAdded.Collect(ch)
	subscriptionNodesRemoved.Collect(ch)
	subscriptionFetchDuration.Collect(ch)
}

// systemCollector 系统指标收集
type systemCollector struct{}

func (c *systemCollector) Describe(ch chan<- *prometheus.Desc) {
	systemGoroutines.Describe(ch)
	systemMemoryAlloc.Describe(ch)
	systemMemorySys.Describe(ch)
	systemUptime.Describe(ch)
}

func (c *systemCollector) Collect(ch chan<- prometheus.Metric) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	systemGoroutines.Set(float64(runtime.NumGoroutine()))
	systemMemoryAlloc.Set(float64(m.Alloc))
	systemMemorySys.Set(float64(m.Sys))
	systemUptime.Set(time.Since(startTime).Seconds())

	systemGoroutines.Collect(ch)
	systemMemoryAlloc.Collect(ch)
	systemMemorySys.Collect(ch)
	systemUptime.Collect(ch)
}