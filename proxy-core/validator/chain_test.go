package validator

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/axetroy/ProxyPilot/proxy-core/model"
)

// connectProxy 返回一个简单的 HTTP CONNECT 转发代理：
// 收到 CONNECT host:port 后，建立到 forwardTo 的连接并双向转发，
// 用于在测试中模拟链路中的一跳。
func connectProxy(forwardTo string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT required", http.StatusMethodNotAllowed)
			return
		}
		up, err := net.Dial("tcp", forwardTo)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			_ = up.Close()
			http.Error(w, "hijack unsupported", http.StatusInternalServerError)
			return
		}
		down, _, err := hj.Hijack()
		if err != nil {
			_ = up.Close()
			return
		}
		if _, err := down.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
			_ = up.Close()
			_ = down.Close()
			return
		}
		go func() { _, _ = io.Copy(up, down); _ = up.Close() }()
		go func() { _, _ = io.Copy(down, up); _ = down.Close() }()
	})
}

func nodeFromServer(t *testing.T, srv *httptest.Server) *model.ProxyNode {
	t.Helper()
	host, portStr, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("split listener addr: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	return &model.ProxyNode{Host: host, Port: port, Protocol: model.ProtocolHTTP}
}

// TestConnectChainMultiHop 验证 HTTP CONNECT 链路 A→B→target 建立隧道并完成请求。
func TestConnectChainMultiHop(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "chain-ok")
	}))
	defer target.Close()

	hopB := httptest.NewServer(connectProxy(target.Listener.Addr().String()))
	defer hopB.Close()
	hopA := httptest.NewServer(connectProxy(hopB.Listener.Addr().String()))
	defer hopA.Close()

	nodes := []*model.ProxyNode{
		nodeFromServer(t, hopA),
		nodeFromServer(t, hopB),
	}
	targetAddr := target.Listener.Addr().String()

	conn, err := ConnectChain(nodes, targetAddr, 5*time.Second)
	if err != nil {
		t.Fatalf("ConnectChain: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// 通过隧道发送明文 HTTP 请求，验证隧道真正到达 target。
	req, _ := http.NewRequest(http.MethodGet, "http://"+targetAddr+"/", nil)
	if err := req.Write(conn); err != nil {
		t.Fatalf("write request: %v", err)
	}
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, req)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if resp.StatusCode != http.StatusOK || string(body) != "chain-ok" {
		t.Fatalf("chain response = %d %q, want 200 chain-ok", resp.StatusCode, body)
	}
}

// TestConnectChainSingleHop 单跳链路等价于 ConnectTCP。
func TestConnectChainSingleHop(t *testing.T) {
	hop := httptest.NewServer(connectProxy("127.0.0.1:1")) // 目标不可达，验证握手阶段即失败路径
	defer hop.Close()

	nodes := []*model.ProxyNode{nodeFromServer(t, hop)}
	_, err := ConnectChain(nodes, "127.0.0.1:1", time.Second)
	if err == nil {
		t.Fatal("expected error for unreachable target through chain")
	}
}

// TestConnectChainEmpty 空链直接报错。
func TestConnectChainEmpty(t *testing.T) {
	if _, err := ConnectChain(nil, "127.0.0.1:1", time.Second); err == nil {
		t.Fatal("expected error for empty chain")
	}
}

// TestConnectChainBrokenHop 中间跳不可用时整链失败。
func TestConnectChainBrokenHop(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer target.Close()

	hopB := httptest.NewServer(connectProxy(target.Listener.Addr().String()))
	defer hopB.Close()

	// hopA 是失效代理：监听一个立即拒绝连接的地址
	dead, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	deadAddr := dead.Addr().String()
	_ = dead.Close() // 端口随即关闭，连接会被拒绝

	host, portStr, _ := net.SplitHostPort(deadAddr)
	port, _ := strconv.Atoi(portStr)
	nodes := []*model.ProxyNode{
		{Host: host, Port: port, Protocol: model.ProtocolHTTP},
		nodeFromServer(t, hopB),
	}
	_, err = ConnectChain(nodes, target.Listener.Addr().String(), time.Second)
	if err == nil {
		t.Fatal("expected error when first hop is unreachable")
	}
}

// TestTestChainAllHopsOK 验证全链路健康时每跳都 OK 且延迟为正。
func TestTestChainAllHopsOK(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer target.Close()

	hopB := httptest.NewServer(connectProxy(target.Listener.Addr().String()))
	defer hopB.Close()
	hopA := httptest.NewServer(connectProxy(hopB.Listener.Addr().String()))
	defer hopA.Close()

	nodes := []*model.ProxyNode{
		nodeFromServer(t, hopA),
		nodeFromServer(t, hopB),
	}
	res := TestChain(nodes, target.Listener.Addr().String(), 5*time.Second)
	if !res.OK {
		t.Fatalf("TestChain = %+v, want OK", res)
	}
	if len(res.Hops) != 2 {
		t.Fatalf("hops = %d, want 2", len(res.Hops))
	}
	for i, h := range res.Hops {
		if !h.OK || h.Error != "" {
			t.Errorf("hop %d = %+v, want OK", i, h)
		}
		if h.Latency < 0 {
			t.Errorf("hop %d latency = %d, want >= 0", i, h.Latency)
		}
	}
	if res.TotalLatency < 0 {
		t.Errorf("TotalLatency = %d, want >= 0", res.TotalLatency)
	}
}

// TestTestChainBrokenHop 中间跳不可用时，测试结果在失败跳处截断且错误可读。
func TestTestChainBrokenHop(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer target.Close()

	hopB := httptest.NewServer(connectProxy(target.Listener.Addr().String()))
	defer hopB.Close()

	dead, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	deadAddr := dead.Addr().String()
	_ = dead.Close()

	host, portStr, _ := net.SplitHostPort(deadAddr)
	port, _ := strconv.Atoi(portStr)
	nodes := []*model.ProxyNode{
		{Host: host, Port: port, Protocol: model.ProtocolHTTP},
		nodeFromServer(t, hopB),
	}
	res := TestChain(nodes, target.Listener.Addr().String(), time.Second)
	if res.OK {
		t.Fatalf("TestChain = %+v, want !OK", res)
	}
	if len(res.Hops) == 0 {
		t.Fatal("expected at least the failed hop recorded")
	}
	last := res.Hops[len(res.Hops)-1]
	if last.OK {
		t.Errorf("last hop = %+v, want failed", last)
	}
	if last.Error == "" {
		t.Errorf("hop %d error empty, want readable failure", last.Hop)
	}
}

// TestTestChainEmpty 空链返回空结果，不崩溃。
func TestTestChainEmpty(t *testing.T) {
	res := TestChain(nil, "127.0.0.1:1", time.Second)
	if len(res.Hops) != 0 {
		t.Fatalf("hops = %d, want 0", len(res.Hops))
	}
}
