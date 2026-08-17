package gateway

import (
	"fmt"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/axetroy/ProxyPilot/proxy-core/bus"
	"github.com/axetroy/ProxyPilot/proxy-core/model"
	"github.com/axetroy/ProxyPilot/proxy-core/pool"
	"github.com/axetroy/ProxyPilot/proxy-core/scheduler"
	"github.com/axetroy/ProxyPilot/proxy-core/storage"
	"github.com/axetroy/ProxyPilot/proxy-core/validator"
)

// TestSOCKS5UDPAssociateRoundTrip 端到端验证 UDP ASSOCIATE：
// 客户端经本地混合端口建立 UDP 中继，数据报经「本地网关 -> 上游 SOCKS5 节点」直达 UDP 目标，
// 回包经同链路返回客户端。
// 上游 SOCKS5 节点复用本地 socksServer（直连模式），构成两层 UDP 中继。
func TestSOCKS5UDPAssociateRoundTrip(t *testing.T) {
	// 目标 UDP echo 服务。
	echoAddr := startUDPEcho(t)

	// 上游 SOCKS5 节点：直连模式（g==nil），收到数据报后直接转发到目标。
	upstream := &socksServer{addr: "127.0.0.1:0"}
	if err := upstream.Start(); err != nil {
		t.Fatalf("start upstream socks proxy: %v", err)
	}
	t.Cleanup(upstream.Stop)
	upstreamPort := upstream.ln.Addr().(*net.TCPAddr).Port

	// 组装节点池 + 选择器 + 网关（混合端口同时提供 HTTP/SOCKS5）。
	st, err := storage.New(":memory:")
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	poolMgr := pool.NewManager(st, nil, bus.New(), 4)
	poolMgr.AddNodes([]*model.ProxyNode{
		{Host: "127.0.0.1", Port: upstreamPort, Protocol: model.ProtocolSOCKS5, Status: model.StatusAlive, Score: 100, Latency: 1},
	})
	sel := scheduler.NewSelector(poolMgr)
	g := NewGateway(poolMgr, sel, nil, "127.0.0.1:0")
	if err := g.Start(); err != nil {
		t.Fatalf("Start gateway: %v", err)
	}
	t.Cleanup(g.Stop)

	// SOCKS5 UDP 客户端：TCP 握手 + UDP ASSOCIATE 拿到本地中继地址。
	client, err := startSOCKS5UDPClient(t, g.HTTPAddr())
	if err != nil {
		t.Fatalf("start socks5 udp client: %v", err)
	}
	t.Cleanup(client.Close)

	// 经中继发送数据报到 echo 服务并接收响应。
	payload := "udp-associate-ping"
	if err := client.Send("127.0.0.1", echoAddr.Port, []byte(payload)); err != nil {
		t.Fatalf("send udp datagram: %v", err)
	}
	host, port, resp, err := client.Receive()
	if err != nil {
		t.Fatalf("receive udp datagram: %v", err)
	}
	if string(resp) != payload {
		t.Errorf("echo payload = %q, want %q", resp, payload)
	}
	if host != "127.0.0.1" || port != echoAddr.Port {
		t.Errorf("echo source = %s:%d, want %s", host, port, echoAddr.String())
	}
}

// TestSOCKS5UDPAssociateRejectedWithoutSOCKS5Node 池中只有 HTTP 节点时，
// UDP ASSOCIATE 应返回 0x07（命令不支持），因为 HTTP 节点无法承载 UDP。
func TestSOCKS5UDPAssociateRejectedWithoutSOCKS5Node(t *testing.T) {
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

	code, err := socks5AssociateCode(t, g.HTTPAddr())
	if err != nil {
		t.Fatalf("udp associate: %v", err)
	}
	if code == 0x00 {
		t.Fatal("expected udp associate rejected without socks5 node, got code 0x00")
	}
	if code != 0x07 {
		t.Errorf("associate reply code = %d, want 0x07 (command not supported)", code)
	}
}

// TestSOCKS5UDPAssociateDirect 网关未配置节点池（g==nil）时走直连模式，
// 验证 UDP 中继本身可用。
func TestSOCKS5UDPAssociateDirect(t *testing.T) {
	echoAddr := startUDPEcho(t)

	socks := &socksServer{addr: "127.0.0.1:0"}
	if err := socks.Start(); err != nil {
		t.Fatalf("start socks proxy: %v", err)
	}
	t.Cleanup(socks.Stop)

	client, err := startSOCKS5UDPClient(t, socks.ln.Addr().String())
	if err != nil {
		t.Fatalf("start socks5 udp client: %v", err)
	}
	t.Cleanup(client.Close)

	payload := "direct-udp"
	if err := client.Send("127.0.0.1", echoAddr.Port, []byte(payload)); err != nil {
		t.Fatalf("send: %v", err)
	}
	_, _, resp, err := client.Receive()
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if string(resp) != payload {
		t.Errorf("echo payload = %q, want %q", resp, payload)
	}
}

// ---------- 测试辅助 ----------

// startUDPEcho 启动一个 UDP echo 服务（收到什么回什么）。
func startUDPEcho(t *testing.T) *net.UDPAddr {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("listen udp echo: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			n, addr, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			_, _ = conn.WriteToUDP(buf[:n], addr)
		}
	}()
	return conn.LocalAddr().(*net.UDPAddr)
}

// testUDPClient 是 SOCKS5 UDP 客户端：TCP 握手建立 UDP ASSOCIATE 会话，
// 之后通过本地中继收发 SOCKS5 UDP 数据报。
type testUDPClient struct {
	ctrl net.Conn
	udp  *net.UDPConn
}

func (c *testUDPClient) Send(host string, port int, payload []byte) error {
	dgram, err := validator.BuildSOCKS5UDP(host, port, payload)
	if err != nil {
		return err
	}
	_, err = c.udp.Write(dgram)
	return err
}

func (c *testUDPClient) Receive() (host string, port int, payload []byte, err error) {
	buf := make([]byte, 64*1024)
	n, err := c.udp.Read(buf)
	if err != nil {
		return "", 0, nil, err
	}
	return validator.ParseSOCKS5UDP(buf[:n])
}

func (c *testUDPClient) Close() {
	_ = c.udp.Close()
	_ = c.ctrl.Close()
}

// startSOCKS5UDPClient 建立 SOCKS5 会话并发送 UDP ASSOCIATE，返回中继客户端。
func startSOCKS5UDPClient(t *testing.T, proxyAddr string) (*testUDPClient, error) {
	t.Helper()
	ctrl, err := net.DialTimeout("tcp", proxyAddr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	// 握手：无认证
	if _, err := ctrl.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		_ = ctrl.Close()
		return nil, err
	}
	resp := make([]byte, 2)
	if _, err := io.ReadFull(ctrl, resp); err != nil {
		_ = ctrl.Close()
		return nil, err
	}
	if resp[0] != 0x05 || resp[1] != 0x00 {
		_ = ctrl.Close()
		return nil, fmt.Errorf("unexpected handshake reply %v", resp)
	}
	// UDP ASSOCIATE（DST 0.0.0.0:0）
	if _, err := ctrl.Write([]byte{0x05, 0x03, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		_ = ctrl.Close()
		return nil, err
	}
	head := make([]byte, 4)
	if _, err := io.ReadFull(ctrl, head); err != nil {
		_ = ctrl.Close()
		return nil, err
	}
	if head[0] != 0x05 || head[1] != 0x00 {
		_ = ctrl.Close()
		return nil, fmt.Errorf("udp associate rejected: code %d", head[1])
	}
	host, port, err := readSocks5AddrFromConn(t, ctrl, head[3])
	if err != nil {
		_ = ctrl.Close()
		return nil, err
	}
	relay, err := net.ResolveUDPAddr("udp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		_ = ctrl.Close()
		return nil, err
	}
	udp, err := net.DialUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}, relay)
	if err != nil {
		_ = ctrl.Close()
		return nil, err
	}
	return &testUDPClient{ctrl: ctrl, udp: udp}, nil
}

// socks5AssociateCode 建立 SOCKS5 会话并发送 UDP ASSOCIATE，返回 REPLY 的 REP 字段。
func socks5AssociateCode(t *testing.T, proxyAddr string) (byte, error) {
	t.Helper()
	ctrl, err := net.DialTimeout("tcp", proxyAddr, 5*time.Second)
	if err != nil {
		return 0, err
	}
	defer func() { _ = ctrl.Close() }()
	if _, err := ctrl.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		return 0, err
	}
	resp := make([]byte, 2)
	if _, err := io.ReadFull(ctrl, resp); err != nil {
		return 0, err
	}
	if _, err := ctrl.Write([]byte{0x05, 0x03, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		return 0, err
	}
	head := make([]byte, 4)
	if _, err := io.ReadFull(ctrl, head); err != nil {
		return 0, err
	}
	if head[0] != 0x05 {
		return 0, fmt.Errorf("bad socks5 version %d", head[0])
	}
	return head[1], nil
}

// readSocks5AddrFromConn 从连接读取一个 SOCKS5 地址（ATYP + ADDR + PORT）。
func readSocks5AddrFromConn(t *testing.T, r io.Reader, atyp byte) (string, int, error) {
	t.Helper()
	var host string
	switch atyp {
	case 0x01:
		b := make([]byte, 4)
		if _, err := io.ReadFull(r, b); err != nil {
			return "", 0, err
		}
		host = net.IP(b).String()
	case 0x03:
		b := make([]byte, 1)
		if _, err := io.ReadFull(r, b); err != nil {
			return "", 0, err
		}
		n := int(b[0])
		b = make([]byte, n)
		if _, err := io.ReadFull(r, b); err != nil {
			return "", 0, err
		}
		host = string(b)
	case 0x04:
		b := make([]byte, 16)
		if _, err := io.ReadFull(r, b); err != nil {
			return "", 0, err
		}
		host = net.IP(b).String()
	default:
		return "", 0, fmt.Errorf("unsupported address type %d", atyp)
	}
	pb := make([]byte, 2)
	if _, err := io.ReadFull(r, pb); err != nil {
		return "", 0, err
	}
	port := int(pb[0])<<8 | int(pb[1])
	return host, port, nil
}