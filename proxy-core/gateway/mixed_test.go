package gateway

import (
	"bufio"
	"fmt"
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

	"golang.org/x/net/proxy"
)

// 混合模式：HTTP 与 SOCKS5 配置为同一端口时，网关在单一端口上同时提供两种协议。
// 端到端验证：HTTP 请求与 SOCKS5 握手都走同一个端口，并正确路由到对应协议的上游节点。
func TestMixedPortServesHTTPAndSOCKS5(t *testing.T) {
	// 1. HTTP 目标服务
	httpTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "http-ok")
	}))
	defer httpTarget.Close()
	httpTargetHost, httpTargetPort, err := net.SplitHostPort(strings.TrimPrefix(httpTarget.URL, "http://"))
	if err != nil {
		t.Fatalf("parse http target addr: %v", err)
	}

	// 2. 上游 HTTP CONNECT 代理（作为池中的 HTTP 节点）
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

	// 3. 上游 SOCKS5 代理（直连模式，作为池中的 SOCKS5 节点）
	upstreamSocks := &socksServer{addr: "127.0.0.1:0"}
	if err := upstreamSocks.Start(); err != nil {
		t.Fatalf("start upstream socks proxy: %v", err)
	}
	defer upstreamSocks.Stop()
	upstreamSocksPort := upstreamSocks.ln.Addr().(*net.TCPAddr).Port

	// 4. 组装节点池 + 选择器 + 网关（HTTP 与 SOCKS5 同一端口 → 混合模式）
	st, err := storage.New(":memory:")
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	defer func() { _ = st.Close() }()

	poolMgr := pool.NewManager(st, nil, bus.New(), 4)
	poolMgr.AddNodes([]*model.ProxyNode{
		{Host: "127.0.0.1", Port: upstreamHTTPPort, Protocol: model.ProtocolHTTP, Status: model.StatusAlive, Score: 100, Latency: 1},
		{Host: "127.0.0.1", Port: upstreamSocksPort, Protocol: model.ProtocolSOCKS5, Status: model.StatusAlive, Score: 100, Latency: 1},
	})
	sel := scheduler.NewSelector(poolMgr)

	g := NewGateway(poolMgr, sel, nil, "127.0.0.1:0")
	if err := g.Start(); err != nil {
		t.Fatalf("Start mixed gateway: %v", err)
	}
	defer g.Stop()

	if g.HTTPAddr() != g.SOCKSAddr() {
		t.Errorf("expected shared port, got HTTP=%s SOCKS5=%s", g.HTTPAddr(), g.SOCKSAddr())
	}
	mixedAddr := g.HTTPAddr()

	// 5. HTTP 请求走混合端口（forward → HTTP 上游节点 → 目标）
	transport := &http.Transport{Proxy: http.ProxyURL(&url.URL{Scheme: "http", Host: mixedAddr})}
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	resp, err := client.Get(httpTarget.URL + "/hello")
	if err != nil {
		t.Fatalf("http request via mixed port: %v", err)
	}
	body, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		t.Fatalf("read http response: %v", readErr)
	}
	if resp.StatusCode != http.StatusOK || string(body) != "http-ok" {
		t.Errorf("http via mixed port = %d %q, want 200 http-ok", resp.StatusCode, body)
	}

	// 6. SOCKS5 连接走混合端口（SOCKS5 握手 → SOCKS5 上游节点 → 目标 HTTP 服务）
	dialer, err := proxy.SOCKS5("tcp", mixedAddr, nil, proxy.Direct)
	if err != nil {
		t.Fatalf("build socks5 dialer: %v", err)
	}
	conn, err := dialer.Dial("tcp", net.JoinHostPort(httpTargetHost, httpTargetPort))
	if err != nil {
		t.Fatalf("socks5 dial via mixed port: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// 通过 SOCKS5 隧道发送一个普通 HTTP 请求，验证字节完整转发
	if _, err := fmt.Fprintf(conn, "GET /socks HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", net.JoinHostPort(httpTargetHost, httpTargetPort)); err != nil {
		t.Fatalf("write http request over socks5: %v", err)
	}
	proxyResp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read http response over socks5: %v", err)
	}
	proxyBody, _ := io.ReadAll(proxyResp.Body)
	_ = proxyResp.Body.Close()
	if proxyResp.StatusCode != http.StatusOK || string(proxyBody) != "http-ok" {
		t.Errorf("socks5 via mixed port = %d %q, want 200 http-ok", proxyResp.StatusCode, proxyBody)
	}
}

// 混合模式端口被占用时整体顺延，且 HTTP/SOCKS5 地址始终相同。
func TestMixedPortShiftWhenOccupied(t *testing.T) {
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("blocker listen: %v", err)
	}
	defer func() { _ = blocker.Close() }()

	blockedPort := blocker.Addr().(*net.TCPAddr).Port
	addr := fmt.Sprintf("127.0.0.1:%d", blockedPort)

	g := NewGateway(nil, nil, nil, addr)
	if err := g.Start(); err != nil {
		t.Fatalf("Start with blocked port: %v", err)
	}
	defer g.Stop()

	if g.HTTPAddr() == addr {
		t.Errorf("expected port shift, still on blocked port %s", addr)
	}
	if g.HTTPAddr() != g.SOCKSAddr() {
		t.Errorf("mixed mode ports differ: HTTP=%s SOCKS5=%s", g.HTTPAddr(), g.SOCKSAddr())
	}
	if !g.Running() {
		t.Error("Running() = false after Start")
	}
}

// 混合模式 Start 幂等：重复调用不会重复绑定。
func TestMixedStartIdempotent(t *testing.T) {
	g := NewGateway(nil, nil, nil, "127.0.0.1:0")
	if err := g.Start(); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if err := g.Start(); err != nil {
		t.Fatalf("second Start should be no-op: %v", err)
	}
	defer g.Stop()

	addr := g.HTTPAddr()
	if g.SOCKSAddr() != addr {
		t.Errorf("SOCKSAddr = %q, want %q (shared port)", g.SOCKSAddr(), addr)
	}
}

// startDirectCONNECTProxy 启动一个极简的直连 CONNECT 代理：
// 收到 `CONNECT host:port` 后直接拨号目标并双向转发，作为测试中的上游 HTTP 节点。
func startDirectCONNECTProxy(t *testing.T) (addr string, closeFn func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen connect proxy: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleDirectCONNECT(conn)
		}
	}()
	return ln.Addr().String(), func() { _ = ln.Close() }
}

func handleDirectCONNECT(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	br := bufio.NewReader(conn)
	line, err := br.ReadString('\n')
	if err != nil {
		return
	}
	fields := strings.Fields(line)
	if len(fields) < 2 || !strings.EqualFold(fields[0], "CONNECT") {
		_, _ = conn.Write([]byte("HTTP/1.1 405 Method Not Allowed\r\n\r\n"))
		return
	}
	target := fields[1]
	out, err := net.Dial("tcp", target)
	if err != nil {
		_, _ = conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return
	}
	defer func() { _ = out.Close() }()

	// 消费剩余请求头直到空行
	for {
		l, err := br.ReadString('\n')
		if err != nil {
			return
		}
		if l == "\r\n" || l == "\n" {
			break
		}
	}
	if _, err := conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(out, br); _ = out.Close(); done <- struct{}{} }()
	go func() { _, _ = io.Copy(conn, out); _ = conn.Close(); done <- struct{}{} }()
	<-done
	<-done
}
