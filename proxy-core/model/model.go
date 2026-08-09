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
