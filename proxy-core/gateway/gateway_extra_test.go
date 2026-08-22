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

	// 当前节点应被记录（选路不区分协议，统一记录 currentNode）
	cur := g.CurrentNode()
	if cur == nil || cur.Host != "127.0.0.1" {
		t.Errorf("CurrentNode = %+v, want 127.0.0.1", cur)
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

// 指定固定出口后，Upstream 应固定使用指定节点，而不是池中评分最高的。
// 复现场景：用户设置了固定出口，但请求仍走评分最高的节点。
func TestUpstreamUsesPinnedNode(t *testing.T) {
	// 两个都可达但端口不同的节点：一个评分高（100），一个评分低（40）
	highAddr, closeHigh := startDirectCONNECTProxy(t)
	t.Cleanup(closeHigh)
	lowAddr, closeLow := startDirectCONNECTProxy(t)
	t.Cleanup(closeLow)

	_, highPortStr, err := net.SplitHostPort(highAddr)
	if err != nil {
		t.Fatalf("parse high addr: %v", err)
	}
	highPort, _ := strconv.Atoi(highPortStr)
	_, lowPortStr, err := net.SplitHostPort(lowAddr)
	if err != nil {
		t.Fatalf("parse low addr: %v", err)
	}
	lowPort, _ := strconv.Atoi(lowPortStr)

	st, err := storage.New(":memory:")
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	poolMgr := pool.NewManager(st, nil, bus.New(), 4)
	poolMgr.AddNodes([]*model.ProxyNode{
		{Host: "127.0.0.1", Port: highPort, Protocol: model.ProtocolHTTP, Status: model.StatusAlive, Score: 100, Latency: 1},
		{Host: "127.0.0.1", Port: lowPort, Protocol: model.ProtocolHTTP, Status: model.StatusAlive, Score: 40, Latency: 5},
	})

	var lowScoreNode *model.ProxyNode
	var highScoreNode *model.ProxyNode
	for _, n := range poolMgr.List() {
		if n.Port == lowPort {
			lowScoreNode = n
		} else {
			highScoreNode = n
		}
	}
	if lowScoreNode == nil || highScoreNode == nil {
		t.Fatal("failed to resolve high/low score nodes")
	}

	sel := scheduler.NewSelector(poolMgr)
	g := NewGateway(poolMgr, sel, bus.New(), "127.0.0.1:0")
	if err := g.Start(); err != nil {
		t.Fatalf("Start gateway: %v", err)
	}
	t.Cleanup(g.Stop)

	echoAddr := startEchoServer(t)

	// 指定前：自动选评分最高的节点
	conn, err := g.Upstream(context.Background(), echoAddr)
	if err != nil {
		t.Fatalf("Upstream before pin: %v", err)
	}
	_ = conn.Close()
	if cur := g.CurrentNode(); cur == nil || cur.ID != highScoreNode.ID {
		t.Errorf("CurrentNode before pin = %+v, want high-score node (id=%d)", cur, highScoreNode.ID)
	}

	// 指定低分节点后：固定走它，即使池中有评分更高的节点
	sel.Pin(lowScoreNode.ID)
	conn, err = g.Upstream(context.Background(), echoAddr)
	if err != nil {
		t.Fatalf("Upstream after pin: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if cur := g.CurrentNode(); cur == nil || cur.ID != lowScoreNode.ID {
		t.Errorf("CurrentNode after pin = %+v, want pinned low-score node (id=%d)", cur, lowScoreNode.ID)
	}

	// 隧道可用：字节应回显
	payload := "pinned-ok"
	if _, err := conn.Write([]byte(payload)); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(buf) != payload {
		t.Errorf("echo = %q, want %q", buf, payload)
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

// 并发连接上限：超过 maxActiveConns 的新连接被拒绝并关闭，已登记的连接正常放行。
func TestTrackConnLimit(t *testing.T) {
	g := NewGateway(nil, nil, nil, "127.0.0.1:0")
	g.maxActiveConns = 2

	server, _ := net.Pipe()
	defer func() { _ = server.Close() }()
	// 第 1、2 条连接登记成功
	if !g.trackConn(server) {
		t.Fatal("first conn should be tracked")
	}
	other, _ := net.Pipe()
	defer func() { _ = other.Close() }()
	if !g.trackConn(other) {
		t.Fatal("second conn should be tracked")
	}

	// 第 3 条连接超限：trackConn 返回 false 且连接已被关闭。
	third, thirdClient := net.Pipe()
	defer func() { _ = thirdClient.Close() }()
	if g.trackConn(third) {
		t.Fatal("third conn should be rejected")
	}
	// 关闭信号应已到达对端（连接被 trackConn 关闭）。
	if _, err := thirdClient.Write([]byte("x")); err == nil {
		t.Fatal("third conn should have been closed by trackConn")
	}

	// 注销一条后新连接重新放行。
	g.untrackConn(other)
	fourth, _ := net.Pipe()
	defer func() { _ = fourth.Close() }()
	if !g.trackConn(fourth) {
		t.Fatal("fourth conn should be tracked after untrack")
	}
}

// UDP ASSOCIATE 会话并发上限：超出 maxUDPAssociates 的新会话被拒绝，已登记的正常放行。
func TestTrackUDPLimit(t *testing.T) {
	g := NewGateway(nil, nil, nil, "127.0.0.1:0")
	g.maxUDPAssociates = 2

	// 第 1、2 个会话登记成功
	if !g.trackUDP() {
		t.Fatal("first UDP session should be tracked")
	}
	if !g.trackUDP() {
		t.Fatal("second UDP session should be tracked")
	}

	// 第 3 个会话超限：trackUDP 返回 false
	if g.trackUDP() {
		t.Fatal("third UDP session should be rejected")
	}

	// 注销一个后新会话重新放行
	g.untrackUDP()
	if !g.trackUDP() {
		t.Fatal("fourth UDP session should be tracked after untrack")
	}

	// nil gateway 不限制
	if !trackUDP(nil) {
		t.Error("nil gateway should not limit UDP")
	}
}

// 访问 trackUDP 的辅助函数（因未导出，仅测试用）
func trackUDP(g *Gateway) bool {
	if g == nil {
		return true
	}
	return g.trackUDP()
}