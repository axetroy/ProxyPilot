package gateway

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/axetroy/ProxyPilot/proxy-core/bus"
	"github.com/axetroy/ProxyPilot/proxy-core/model"
	"github.com/axetroy/ProxyPilot/proxy-core/pool"
	"github.com/axetroy/ProxyPilot/proxy-core/scheduler"
	"github.com/axetroy/ProxyPilot/proxy-core/storage"
)

// newTestGateway 组装带 HTTP 上游节点（直连 CONNECT 代理）的网关。
func newTestGateway(t *testing.T) *Gateway {
	t.Helper()
	upstreamAddr, closeFn := startDirectCONNECTProxy(t)
	t.Cleanup(closeFn)

	_, portStr, err := net.SplitHostPort(upstreamAddr)
	if err != nil {
		t.Fatalf("parse upstream addr: %v", err)
	}
	port, _ := strconv.Atoi(portStr)

	st, err := storage.New(":memory:")
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	poolMgr := pool.NewManager(st, nil, bus.New(), 4)
	poolMgr.AddNodes([]*model.ProxyNode{
		{Host: "127.0.0.1", Port: port, Protocol: model.ProtocolHTTP, Status: model.StatusAlive, Score: 100, Latency: 1},
	})
	sel := scheduler.NewSelector(poolMgr)

	g := NewGateway(poolMgr, sel, nil, "127.0.0.1:0")
	if err := g.Start(); err != nil {
		t.Fatalf("Start gateway: %v", err)
	}
	t.Cleanup(g.Stop)
	return g
}

// startEchoServer 启动一个 TCP echo 服务，返回地址。
func startEchoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen echo: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()
	return ln.Addr().String()
}

// CONNECT 隧道：通过混合端口建立隧道并回显字节（覆盖 tunnel + relay）。
func TestTunnelCONNECT(t *testing.T) {
	echoAddr := startEchoServer(t)
	g := newTestGateway(t)

	conn, err := net.Dial("tcp", g.HTTPAddr())
	if err != nil {
		t.Fatalf("dial gateway: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", echoAddr, echoAddr); err != nil {
		t.Fatalf("write CONNECT: %v", err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read CONNECT response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT status = %d, want 200", resp.StatusCode)
	}

	// 隧道建立后发送字节，应原样回显
	payload := "ping-through-tunnel"
	if _, err := conn.Write([]byte(payload)); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, len(payload))
	if _, err := io.ReadFull(br, buf); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(buf) != payload {
		t.Errorf("echo = %q, want %q", buf, payload)
	}
}

// CONNECT 缺少 Host 时返回 400。
func TestTunnelMissingHost(t *testing.T) {
	g := newTestGateway(t)

	conn, err := net.Dial("tcp", g.HTTPAddr())
	if err != nil {
		t.Fatalf("dial gateway: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := fmt.Fprintf(conn, "CONNECT HTTP/1.1\r\n\r\n"); err != nil {
		t.Fatalf("write CONNECT: %v", err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// Upstream 直接调用（不带协议）：应返回连接并记录当前节点。
func TestUpstreamAndCurrentNode(t *testing.T) {
	echoAddr := startEchoServer(t)
	g := newTestGateway(t)

	conn, err := g.Upstream(context.Background(), echoAddr)
	if err != nil {
		t.Fatalf("Upstream: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// 当前节点应被记录（HTTP 协议默认分支）
	cur := g.CurrentNode()
	if cur == nil || cur.Host != "127.0.0.1" {
		t.Errorf("CurrentNode = %+v, want 127.0.0.1", cur)
	}
	httpNode := g.CurrentHTTPNode()
	if httpNode == nil || httpNode.Host != "127.0.0.1" {
		t.Errorf("CurrentHTTPNode = %+v, want 127.0.0.1", httpNode)
	}
	if g.CurrentSOCKS5Node() != nil {
		t.Errorf("CurrentSOCKS5Node = %+v, want nil", g.CurrentSOCKS5Node())
	}

	// 连接可用：发送字节应回显
	if _, err := conn.Write([]byte("upstream-ok")); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, len("upstream-ok"))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(buf) != "upstream-ok" {
		t.Errorf("echo = %q", buf)
	}
}

// Upstream 无可用节点时报错。
func TestUpstreamNoNodes(t *testing.T) {
	st, err := storage.New(":memory:")
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	defer func() { _ = st.Close() }()

	poolMgr := pool.NewManager(st, nil, bus.New(), 4)
	sel := scheduler.NewSelector(poolMgr)
	g := NewGateway(poolMgr, sel, nil, "127.0.0.1:0")

	_, err = g.Upstream(context.Background(), "example.com:80")
	if err == nil {
		t.Fatal("Upstream with empty pool should fail")
	}
}

// SetAddr 更新配置端口，Stop 后重新 Start 使用新端口。
func TestSetAddr(t *testing.T) {
	g := NewGateway(nil, nil, nil, "127.0.0.1:0")
	if err := g.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	oldAddr := g.HTTPAddr()

	// 找一个空闲端口作为新配置
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	newPort := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()

	g.SetAddr(fmt.Sprintf("127.0.0.1:%d", newPort))
	g.Stop()
	if err := g.Start(); err != nil {
		t.Fatalf("restart: %v", err)
	}
	defer g.Stop()

	if g.HTTPAddr() == oldAddr {
		t.Errorf("addr unchanged after SetAddr: %s", g.HTTPAddr())
	}
	if !strings.HasSuffix(g.HTTPAddr(), fmt.Sprintf(":%d", newPort)) {
		t.Errorf("addr = %q, want port %d", g.HTTPAddr(), newPort)
	}
}

// httpServer.Start 独立监听（非混合模式）。
func TestHTTPServerStart(t *testing.T) {
	g := NewGateway(nil, nil, nil, "127.0.0.1:0")
	h := &httpServer{addr: "127.0.0.1:0", g: g}
	if err := h.Start(); err != nil {
		t.Fatalf("httpServer.Start: %v", err)
	}
	if h.ln == nil {
		t.Fatal("ln is nil after Start")
	}
	h.Stop()
}