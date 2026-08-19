package gateway

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/axetroy/ProxyPilot/proxy-core/bus"
	"github.com/axetroy/ProxyPilot/proxy-core/model"
	"github.com/axetroy/ProxyPilot/proxy-core/pool"
	"github.com/axetroy/ProxyPilot/proxy-core/scheduler"
	"github.com/axetroy/ProxyPilot/proxy-core/storage"
)

// TestTrafficCounted 验证 HTTP 转发路径的流量被正确计入对应节点的统计桶。
// 请求 N 次后：total 与 byNode 的连接数、下载字节都应随请求累加。
func TestTrafficCounted(t *testing.T) {
	// 目标服务返回固定长度响应，便于断言下载字节下限。
	body := strings.Repeat("x", 4096)
	httpTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, body)
	}))
	defer httpTarget.Close()

	// 上游 HTTP CONNECT 代理（作为池中的 HTTP 节点）
	upstreamHTTPAddr, closeHTTP := startDirectCONNECTProxy(t)
	defer closeHTTP()
	_, upstreamHTTPPortStr, err := net.SplitHostPort(upstreamHTTPAddr)
	if err != nil {
		t.Fatalf("parse upstream http addr: %v", err)
	}
	upstreamHTTPPort, err := strconv.Atoi(upstreamHTTPPortStr)
	if err != nil {
		t.Fatalf("parse upstream http port: %v", err)
	}

	st, err := storage.New(":memory:")
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	defer func() { _ = st.Close() }()

	poolMgr := pool.NewManager(st, nil, bus.New(), 4)
	node := &model.ProxyNode{Host: "127.0.0.1", Port: upstreamHTTPPort, Protocol: model.ProtocolHTTP, Status: model.StatusAlive, Score: 100, Latency: 1}
	if poolMgr.AddNodes([]*model.ProxyNode{node}) != 1 {
		t.Fatal("AddNodes did not add node")
	}
	sel := scheduler.NewSelector(poolMgr)
	g := NewGateway(poolMgr, sel, nil, "127.0.0.1:0")
	if err := g.Start(); err != nil {
		t.Fatalf("Start gateway: %v", err)
	}
	defer g.Stop()

	// 经网关转发 N 次请求，每次读取完整响应体。
	const requests = 3
	transport := &http.Transport{Proxy: http.ProxyURL(&url.URL{Scheme: "http", Host: g.HTTPAddr()})}
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	for i := 0; i < requests; i++ {
		resp, err := client.Get(httpTarget.URL + "/count")
		if err != nil {
			t.Fatalf("request %d via gateway: %v", i, err)
		}
		if _, err := io.Copy(io.Discard, resp.Body); err != nil {
			t.Fatalf("read response %d: %v", i, err)
		}
		_ = resp.Body.Close()
	}

	snap := g.Traffic()
	if snap.Total.Connections != requests {
		t.Errorf("total connections = %d, want %d", snap.Total.Connections, requests)
	}
	if snap.Total.Download < int64(requests*len(body)) {
		t.Errorf("total download = %d, want >= %d", snap.Total.Download, requests*len(body))
	}
	if snap.Total.Upload <= 0 {
		t.Errorf("total upload = %d, want > 0 (request bytes)", snap.Total.Upload)
	}
	if len(snap.ByNode) != 1 {
		t.Fatalf("byNode size = %d, want 1", len(snap.ByNode))
	}
	nodeStat := snap.ByNode[0]
	if nodeStat.ID != node.ID {
		t.Errorf("node id = %d, want %d", nodeStat.ID, node.ID)
	}
	if nodeStat.Connections != requests {
		t.Errorf("node connections = %d, want %d", nodeStat.Connections, requests)
	}
	if nodeStat.Download != snap.Total.Download {
		t.Errorf("node download = %d, want total %d", nodeStat.Download, snap.Total.Download)
	}
}

// TestTrafficDirect 智能分流命中直连的目标流量计入 direct 桶。
func TestTrafficDirect(t *testing.T) {
	body := "direct-ok"
	httpTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, body)
	}))
	defer httpTarget.Close()

	st, err := storage.New(":memory:")
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	defer func() { _ = st.Close() }()

	poolMgr := pool.NewManager(st, nil, bus.New(), 4)
	sel := scheduler.NewSelector(poolMgr)
	g := NewGateway(poolMgr, sel, nil, "127.0.0.1:0")
	// 全部目标直连：分流函数恒真。
	g.SetShunt(func(host string) bool { return true })
	if err := g.Start(); err != nil {
		t.Fatalf("Start gateway: %v", err)
	}
	defer g.Stop()

	transport := &http.Transport{Proxy: http.ProxyURL(&url.URL{Scheme: "http", Host: g.HTTPAddr()})}
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	resp, err := client.Get(httpTarget.URL + "/direct")
	if err != nil {
		t.Fatalf("request via direct shunt: %v", err)
	}
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatalf("read response: %v", err)
	}
	_ = resp.Body.Close()

	snap := g.Traffic()
	if snap.Direct.Connections != 1 {
		t.Errorf("direct connections = %d, want 1", snap.Direct.Connections)
	}
	if snap.Direct.Download < int64(len(body)) {
		t.Errorf("direct download = %d, want >= %d", snap.Direct.Download, len(body))
	}
	if len(snap.ByNode) != 0 {
		t.Errorf("byNode size = %d, want 0 (shunt bypasses node pool)", len(snap.ByNode))
	}
}
