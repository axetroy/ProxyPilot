package model

import (
	"strconv"
	"time"
)

type ProxyStatus string

const (
	StatusNew      ProxyStatus = "new"
	StatusChecking ProxyStatus = "checking"
	StatusAlive    ProxyStatus = "alive"
	StatusDead     ProxyStatus = "dead"
)

type ProxyProtocol string

const (
	ProtocolHTTP   ProxyProtocol = "http"
	ProtocolHTTPS  ProxyProtocol = "https"
	ProtocolSOCKS5 ProxyProtocol = "socks5"
)

type ProxyNode struct {
	ID             int64         `json:"id"`
	Host           string        `json:"host"`
	Port           int           `json:"port"`
	Protocol       ProxyProtocol `json:"protocol"`
	Username       string        `json:"username,omitempty"`
	Password       string        `json:"password,omitempty"`
	Latency        int64         `json:"latency"`
	Score          int           `json:"score"`
	Status         ProxyStatus   `json:"status"`
	Country        string        `json:"country,omitempty"`  // 节点出口地区：国家（离线 GeoIP 解析，检测时填充）
	Province       string        `json:"province,omitempty"` // 省份/州
	City           string        `json:"city,omitempty"`     // 城市
	SuccessCount   int           `json:"successCount"`
	FailCount      int           `json:"failCount"`
	LastCheck      time.Time     `json:"lastCheck"`
	CreatedAt      time.Time     `json:"createdAt"`
	UpdatedAt      time.Time     `json:"updatedAt"`
	SubscriptionID int64         `json:"subscriptionId,omitempty"`
	// ScoreBreakdown 评分明细，仅 API 输出时填充（不持久化），用于前端展示评分计算过程。
	ScoreBreakdown *ScoreBreakdown `json:"scoreBreakdown,omitempty"`
	// SafetyDetail 连接安全检测明细（探测成功时填充，不持久化），
	// 供评分明细展示与 Breakdown 还原使用。
	SafetyDetail *SafetyDetail `json:"safetyDetail,omitempty"`
	// MitmDetected 检测到该节点存在 HTTPS 中间人行为（向客户端返回伪造的目标站点证书）。
	// 持久化字段；被标记的节点在「安全路由」开启时不会被自动选用。
	MitmDetected bool `json:"mitmDetected"`
	// MitmAt 最近一次中间人检测时间（无论结果，用于检测节流与前端展示）。
	MitmAt time.Time `json:"mitmAt"`
	// Speed 经节点下载测得的带宽速率（字节/秒），未测速时为 0。
	// 由带宽测速探测填充（手动「测速」或开启自动测速时的健康检查偶发探测）。
	Speed int64 `json:"speed"`
	// SpeedAt 最近一次带宽测速时间（用于测速节流与前端展示）。
	SpeedAt time.Time `json:"speedAt"`
}

// ScoreBreakdown 表示一次评分的各维度明细，与 CalculateScore 的权重口径一致。
type ScoreBreakdown struct {
	SuccessRate     int     `json:"successRate"`     // 成功率（0-100）
	LatencyScore    int     `json:"latencyScore"`    // 延迟分（0-100）
	Stability       int     `json:"stability"`       // 稳定性（0-100）
	Safety          int     `json:"safety"`          // 连接安全（0-100）
	WeightSuccess   float64 `json:"weightSuccess"`   // 成功率权重
	WeightLatency   float64 `json:"weightLatency"`   // 延迟权重
	WeightStability float64 `json:"weightStability"` // 稳定性权重
	WeightSafety    float64 `json:"weightSafety"`    // 连接安全权重
	Score           int     `json:"score"`           // 加权总分（0-100）
}

func (n *ProxyNode) Key() string {
	return n.Host + ":" + strconv.Itoa(n.Port) + ":" + string(n.Protocol)
}

type Subscription struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	URL        string    `json:"url"`
	Interval   int64     `json:"interval"` // seconds
	Enabled    bool      `json:"enabled"`
	LastFetch  time.Time `json:"lastFetch"`
	CreatedAt  time.Time `json:"createdAt"`
	ProxyCount int       `json:"proxyCount,omitempty"`
}

// ProxyChain 代理链路：客户端 → n0 → n1 → … → 目标服务器。
// NodeIDs 是有序节点 ID 列表（按链路顺序，前端按选择顺序写入）。
type ProxyChain struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	NodeIDs   []int64   `json:"nodeIds"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	// LastCheckedAt 最近一次自动健康检测时间（从未检测过为空）。
	LastCheckedAt *time.Time `json:"lastCheckedAt,omitempty"`
	// LastOK 最近一次健康检测是否通过。
	LastOK bool `json:"lastOk"`
	// LastLatency 最近一次健康检测整链建链总耗时（毫秒），失败时为 0。
	LastLatency int64 `json:"lastLatency"`
	// LastError 最近一次健康检测失败原因（通过时为空）。
	LastError string `json:"lastError,omitempty"`
	// ConsecutiveFailures 连续失败次数；达到阈值自动停用后重置为 0。
	ConsecutiveFailures int `json:"consecutiveFailures"`
	// AutoDisabled 是否被链路健康管理自动停用（区别于用户手动停用）。
	AutoDisabled bool `json:"autoDisabled"`
}

// ChainHopResult 链路测试中某一跳（节点）的测试结果。
type ChainHopResult struct {
	// Hop 第几跳（从 1 开始）。
	Hop      int    `json:"hop"`
	NodeID   int64  `json:"nodeId"`
	Key      string `json:"key"` // host:port
	Protocol string `json:"protocol"`
	OK       bool   `json:"ok"`
	// Latency 该跳建立隧道（连接 + 握手）耗时（毫秒）。
	Latency int64 `json:"latency"`
	// Error 该跳失败原因（OK 为 true 时为空）。
	Error string `json:"error,omitempty"`
}

// ChainTestResult 一次链路测试的整体结果。
type ChainTestResult struct {
	// OK 全部跳均成功。
	OK bool `json:"ok"`
	// TotalLatency 整条链路建链总耗时（毫秒），失败时为空。
	TotalLatency int64            `json:"totalLatency"`
	Hops         []ChainHopResult `json:"hops"`
}

type CheckHistory struct {
	ID        int64     `json:"id"`
	ProxyID   int64     `json:"proxyId"`
	Success   bool      `json:"success"`
	Latency   int64     `json:"latency"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

type SystemStatus struct {
	Running     bool       `json:"running"`
	ProxyCount  int        `json:"proxyCount"`
	AliveCount  int        `json:"aliveCount"`
	CurrentIP   string     `json:"currentIP"`
	CurrentNode *ProxyNode `json:"currentNode,omitempty"`
	// PinnedNode 用户指定的固定出口节点（未指定或节点已删除时为 nil），
	// 与 CurrentNode 不同：这是用户主动指定的，不随请求命中而变化。
	PinnedNode     *ProxyNode `json:"pinnedNode,omitempty"`
	HTTPProxyBind  string     `json:"httpProxyBind"`
	SOCKSProxyBind string     `json:"socks5ProxyBind"`
	Version        string     `json:"version"`
}

type CheckResult struct {
	OK      bool   `json:"ok"`
	Latency int64  `json:"latency"`
	Error   string `json:"error,omitempty"`
	// Safety 连接安全探测结果（探测成功时填充，失败/超时则为 nil，不影响连通性结果）。
	Safety *SafetyProbe `json:"safety,omitempty"`
}

// SafetyProbe 表示一次连接安全探测的原始数据。
// 检测流程：直连 + 经代理分别请求回显端点（如 httpbin.org/anything），
// 对比出口 IP 与目标收到的请求头，判断源 IP 是否隐藏、头是否泄漏、是否有代理特征、
// 出口 IP 是否轮换、请求是否被代理改写。
type SafetyProbe struct {
	DirectIP  string `json:"directIp"`  // 直连回显端点时看到的出口 IP
	ProxiedIP string `json:"proxiedIp"` // 通过代理访问回显端点时看到的出口 IP
	// ProxiedIP2 第二次经代理采样到的出口 IP（用于识别轮换代理，为空表示未采样）。
	ProxiedIP2 string `json:"proxiedIp2,omitempty"`
	// HeaderLeaks 目标收到的请求头中出现的泄漏头（如 X-Forwarded-For / Forwarded / X-Real-IP）。
	HeaderLeaks []string `json:"headerLeaks,omitempty"`
	// ProxyMarkers 目标收到的请求头中暴露代理身份的特征头（如 Via / Proxy-Agent / X-Via）。
	ProxyMarkers []string `json:"proxyMarkers,omitempty"`
	// ReqIssues 连接信息问题：代理改写请求的信号（如回显端收到的 URL/Host 与请求目标不一致）。
	ReqIssues []string `json:"reqIssues,omitempty"`
}

// SafetyDetail 连接安全评分的子维度明细，供前端展示评分过程。
type SafetyDetail struct {
	// SourceIpHidden 源 IP 是否隐藏：true=代理出口 IP 与直连不同；false=透明；nil=无法对比。
	SourceIpHidden *bool    `json:"sourceIpHidden,omitempty"`
	HeaderLeaks    []string `json:"headerLeaks,omitempty"`
	ProxyMarkers   []string `json:"proxyMarkers,omitempty"`
	// RotatingIP 出口 IP 是否轮换（两次经代理采样结果不同，轮换代理连接安全更好）。
	RotatingIP bool `json:"rotatingIp,omitempty"`
	// ReqIssues 连接信息问题（请求被改写等）。
	ReqIssues []string `json:"reqIssues,omitempty"`
	// Mitm 是否检测到 HTTPS 中间人行为（伪造目标站点证书）。
	Mitm  bool `json:"mitm,omitempty"`
	Score int  `json:"score"` // 连接安全子评分（0-100）
}
