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
	SuccessCount   int           `json:"successCount"`
	FailCount      int           `json:"failCount"`
	LastCheck      time.Time     `json:"lastCheck"`
	CreatedAt      time.Time     `json:"createdAt"`
	UpdatedAt      time.Time     `json:"updatedAt"`
	SubscriptionID int64         `json:"subscriptionId,omitempty"`
	// ScoreBreakdown 评分明细，仅 API 输出时填充（不持久化），用于前端展示评分计算过程。
	ScoreBreakdown *ScoreBreakdown `json:"scoreBreakdown,omitempty"`
}

// ScoreBreakdown 表示一次评分的各维度明细，与 CalculateScore 的权重口径一致。
type ScoreBreakdown struct {
	SuccessRate    int     `json:"successRate"`    // 成功率（0-100）
	LatencyScore   int     `json:"latencyScore"`   // 延迟分（0-100）
	Stability      int     `json:"stability"`      // 稳定性（0-100）
	Anonymity      int     `json:"anonymity"`      // 匿名性（0-100）
	WeightSuccess  float64 `json:"weightSuccess"`  // 成功率权重
	WeightLatency  float64 `json:"weightLatency"`  // 延迟权重
	WeightStability float64 `json:"weightStability"` // 稳定性权重
	WeightAnonymity float64 `json:"weightAnonymity"` // 匿名性权重
	Score          int     `json:"score"`          // 加权总分（0-100）
}

func (n *ProxyNode) Key() string {
	return n.Host + ":" + strconv.Itoa(n.Port) + ":" + string(n.Protocol)
}

type Subscription struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	URL         string    `json:"url"`
	Interval    int64     `json:"interval"` // seconds
	Enabled     bool      `json:"enabled"`
	LastFetch   time.Time `json:"lastFetch"`
	CreatedAt   time.Time `json:"createdAt"`
	ProxyCount  int       `json:"proxyCount,omitempty"`
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
	Running           bool       `json:"running"`
	ProxyCount        int        `json:"proxyCount"`
	AliveCount        int        `json:"aliveCount"`
	CurrentIP         string     `json:"currentIP"`
	CurrentNode       *ProxyNode `json:"currentNode,omitempty"`
	CurrentHTTPNode   *ProxyNode `json:"currentHttpNode,omitempty"`
	CurrentSOCKS5Node *ProxyNode `json:"currentSocks5Node,omitempty"`
	HTTPProxyBind     string     `json:"httpProxyBind"`
	SOCKSProxyBind    string     `json:"socks5ProxyBind"`
	Version           string     `json:"version"`
}

type CheckResult struct {
	OK      bool   `json:"ok"`
	Latency int64  `json:"latency"`
	Error   string `json:"error,omitempty"`
}
