package validator

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/axetroy/ProxyPilot/proxy-core/model"
)

// TestParseSOCKS5UDP 验证 SOCKS5 UDP 数据报解析：IPv4 / 域名 / IPv6 及错误场景。
func TestParseSOCKS5UDP(t *testing.T) {
	// IPv4
	dgram := []byte{0, 0, 0, 0x01, 127, 0, 0, 1, 0x1f, 0x90, 'a', 'b', 'c'}
	host, port, payload, err := ParseSOCKS5UDP(dgram)
	if err != nil {
		t.Fatalf("parse ipv4: %v", err)
	}
	if host != "127.0.0.1" || port != 8080 || string(payload) != "abc" {
		t.Errorf("ipv4 got %s:%d %q", host, port, payload)
	}

	// 域名
	domain := []byte("example.com")
	dgram = []byte{0, 0, 0, 0x03, byte(len(domain))}
	dgram = append(dgram, domain...)
	dgram = append(dgram, 0, 53, 'q')
	host, port, payload, err = ParseSOCKS5UDP(dgram)
	if err != nil {
		t.Fatalf("parse domain: %v", err)
	}
	if host != "example.com" || port != 53 || string(payload) != "q" {
		t.Errorf("domain got %s:%d %q", host, port, payload)
	}

	// IPv6
	dgram = []byte{0, 0, 0, 0x04}
	dgram = append(dgram, net.ParseIP("::1").To16()...)
	dgram = append(dgram, 0x00, 0x35, 'z')
	host, port, payload, err = ParseSOCKS5UDP(dgram)
	if err != nil {
		t.Fatalf("parse ipv6: %v", err)
	}
	if host != "::1" || port != 53 || string(payload) != "z" {
		t.Errorf("ipv6 got %s:%d %q", host, port, payload)
	}

	// 截断
	if _, _, _, err := ParseSOCKS5UDP([]byte{0, 0}); err == nil {
		t.Error("expected error for truncated datagram")
	}
	// 分片（FRAG != 0）
	if _, _, _, err := ParseSOCKS5UDP([]byte{0, 0, 1, 0x01, 1, 2, 3, 4, 0, 1}); err == nil {
		t.Error("expected error for fragmented datagram")
	}
	// RSV 非零
	if _, _, _, err := ParseSOCKS5UDP([]byte{0, 1, 0, 0x01, 1, 2, 3, 4, 0, 1}); err == nil {
		t.Error("expected error for invalid RSV")
	}
}

// TestBuildSOCKS5UDPRoundTrip 验证构造出的数据报能被解析还原。
func TestBuildSOCKS5UDPRoundTrip(t *testing.T) {
	cases := []struct {
		host string
		port int
	}{
		{"127.0.0.1", 8080},
		{"example.com", 443},
		{"::1", 53},
	}
	for _, c := range cases {
		dgram, err := BuildSOCKS5UDP(c.host, c.port, []byte("payload"))
		if err != nil {
			t.Fatalf("build %s:%d: %v", c.host, c.port, err)
		}
		host, port, payload, err := ParseSOCKS5UDP(dgram)
		if err != nil {
			t.Fatalf("roundtrip parse %s:%d: %v", c.host, c.port, err)
		}
		if host != c.host || port != c.port || string(payload) != "payload" {
			t.Errorf("roundtrip %s:%d -> %s:%d %q", c.host, c.port, host, port, payload)
		}
	}
}

func TestBuildSOCKS5UDPInvalid(t *testing.T) {
	if _, err := BuildSOCKS5UDP("", 80, nil); err == nil {
		t.Error("expected error for empty host")
	}
	if _, err := BuildSOCKS5UDP("example.com", 70000, nil); err == nil {
		t.Error("expected error for port out of range")
	}
	bad := make([]byte, 256)
	for i := range bad {
		bad[i] = 'a'
	}
	if _, err := BuildSOCKS5UDP(string(bad), 80, nil); err == nil {
		t.Error("expected error for host longer than 255 bytes")
	}
}

// TestUDPAssociateNilNode 无节点直接报错。
func TestUDPAssociateNilNode(t *testing.T) {
	_, err := UDPAssociate(nil, time.Second)
	if err == nil {
		t.Fatal("expected error for nil node")
	}
}

// TestUDPAssociateRequiresSOCKS5Node HTTP 节点无法承载 UDP，必须报错。
func TestUDPAssociateRequiresSOCKS5Node(t *testing.T) {
	node := &model.ProxyNode{Host: "127.0.0.1", Port: 1, Protocol: model.ProtocolHTTP}
	_, err := UDPAssociate(node, time.Second)
	if err == nil {
		t.Fatal("expected error for non-socks5 node")
	}
}

// TestUDPAssociateSuccess 端到端：UDPAssociate 建链后，
// Send 的 UDP 数据报经上游中继到达 UDP echo 服务，响应经 Receive 收回。
func TestUDPAssociateSuccess(t *testing.T) {
	echo := startUDPEchoServer(t)
	proxyAddr, closeFn := startSocks5UDPServer(t, false)
	defer closeFn()
	host, portStr, _ := net.SplitHostPort(proxyAddr)
	port, _ := strconv.Atoi(portStr)

	node := &model.ProxyNode{Host: host, Port: port, Protocol: model.ProtocolSOCKS5}
	sess, err := UDPAssociate(node, 5*time.Second)
	if err != nil {
		t.Fatalf("UDPAssociate: %v", err)
	}
	defer func() { _ = sess.Close() }()

	payload := "udp-associate-test"
	if err := sess.Send("127.0.0.1", echo.Port, []byte(payload)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	rhost, rport, resp, err := sess.Receive()
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if string(resp) != payload {
		t.Errorf("resp = %q, want %q", resp, payload)
	}
	if rhost != "127.0.0.1" || rport != echo.Port {
		t.Errorf("source = %s:%d, want %s", rhost, rport, echo.String())
	}
}

// TestUDPAssociateRejected 上游拒绝 UDP ASSOCIATE 时应返回错误。
func TestUDPAssociateRejected(t *testing.T) {
	proxyAddr, closeFn := startSocks5UDPServer(t, true)
	defer closeFn()
	host, portStr, _ := net.SplitHostPort(proxyAddr)
	port, _ := strconv.Atoi(portStr)

	node := &model.ProxyNode{Host: host, Port: port, Protocol: model.ProtocolSOCKS5}
	_, err := UDPAssociate(node, 5*time.Second)
	if err == nil {
		t.Fatal("expected error for rejected udp associate")
	}
}

// ---------- 测试辅助 ----------

// startUDPEchoServer 启动一个 UDP echo 服务（收到什么回什么）。
func startUDPEchoServer(t *testing.T) *net.UDPAddr {
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

// startSocks5UDPServer 启动一个无认证的 SOCKS5 服务器，支持 UDP ASSOCIATE。
// 收到客户端的 SOCKS5 UDP 数据报后转发到目标；目标回包封装后返回客户端。
func startSocks5UDPServer(t *testing.T, rejectAssociate bool) (addr string, closeFn func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen socks5 udp server: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleSocks5UDPServerConn(conn, rejectAssociate)
		}
	}()
	return ln.Addr().String(), func() { _ = ln.Close() }
}

func handleSocks5UDPServerConn(conn net.Conn, rejectAssociate bool) {
	defer func() { _ = conn.Close() }()
	br := bufio.NewReader(conn)

	// 握手：选择无认证
	head := make([]byte, 2)
	if _, err := io.ReadFull(br, head); err != nil {
		return
	}
	if head[0] != 0x05 {
		return
	}
	methods := make([]byte, int(head[1]))
	if _, err := io.ReadFull(br, methods); err != nil {
		return
	}
	if _, err := conn.Write([]byte{0x05, 0x00}); err != nil {
		return
	}

	// 只接受 UDP ASSOCIATE
	req := make([]byte, 4)
	if _, err := io.ReadFull(br, req); err != nil {
		return
	}
	if req[0] != 0x05 || req[1] != 0x03 {
		return
	}
	if _, _, err := readAddrForReply(br, req[3]); err != nil {
		return
	}
	if rejectAssociate {
		_, _ = conn.Write([]byte{0x05, 0x01, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}

	// 建立 UDP 中继并返回其地址。
	relay, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		_, _ = conn.Write([]byte{0x05, 0x01, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	defer func() { _ = relay.Close() }()
	relayAddr := relay.LocalAddr().(*net.UDPAddr)
	reply := []byte{0x05, 0x00, 0x00, 0x01, 127, 0, 0, 1}
	reply = append(reply, byte(relayAddr.Port>>8), byte(relayAddr.Port))
	if _, err := conn.Write(reply); err != nil {
		return
	}

	// 转发循环：客户端 SOCKS5 dgram -> 目标；目标回包 -> 客户端。
	// 首个数据报的源地址即客户端地址。
	var clientAddr *net.UDPAddr
	buf := make([]byte, 64*1024)
	for {
		n, from, err := relay.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if clientAddr == nil {
			clientAddr = from
		}
		if from.Port == clientAddr.Port && from.IP.Equal(clientAddr.IP) {
			// 客户端数据报 -> 目标
			host, port, payload, err := ParseSOCKS5UDP(buf[:n])
			if err != nil {
				continue
			}
			target, err := net.ResolveUDPAddr("udp", net.JoinHostPort(host, strconv.Itoa(port)))
			if err != nil {
				continue
			}
			if _, err := relay.WriteToUDP(payload, target); err != nil {
				return
			}
		} else {
			// 目标响应 -> 客户端
			dgram, err := BuildSOCKS5UDP(from.IP.String(), from.Port, buf[:n])
			if err != nil {
				continue
			}
			if _, err := relay.WriteToUDP(dgram, clientAddr); err != nil {
				return
			}
		}
	}
}

// readAddrForReply 从 bufio.Reader 读取并丢弃一个 SOCKS5 地址（ATYP + ADDR + PORT）。
func readAddrForReply(br *bufio.Reader, atyp byte) (string, int, error) {
	var host string
	switch atyp {
	case 0x01:
		b := make([]byte, 4)
		if _, err := io.ReadFull(br, b); err != nil {
			return "", 0, err
		}
		host = net.IP(b).String()
	case 0x03:
		l := make([]byte, 1)
		if _, err := io.ReadFull(br, l); err != nil {
			return "", 0, err
		}
		b := make([]byte, int(l[0]))
		if _, err := io.ReadFull(br, b); err != nil {
			return "", 0, err
		}
		host = string(b)
	case 0x04:
		b := make([]byte, 16)
		if _, err := io.ReadFull(br, b); err != nil {
			return "", 0, err
		}
		host = net.IP(b).String()
	default:
		return "", 0, fmt.Errorf("unsupported address type %d", atyp)
	}
	pb := make([]byte, 2)
	if _, err := io.ReadFull(br, pb); err != nil {
		return "", 0, err
	}
	port := int(pb[0])<<8 | int(pb[1])
	return host, port, nil
}