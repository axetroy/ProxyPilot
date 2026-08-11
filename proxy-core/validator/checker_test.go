package validator

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

	"github.com/axetroy/ProxyPilot/proxy-core/model"
)

func TestNewCheckerDefaults(t *testing.T) {
	c := NewChecker("", 0)
	if c.TestTarget() != "https://www.apple.com/library/test/success.html" {
		t.Errorf("default target = %q", c.TestTarget())
	}
	if c.timeout != 10*time.Second {
		t.Errorf("default timeout = %v, want 10s", c.timeout)
	}
}

func TestNewCheckerCustom(t *testing.T) {
	c := NewChecker("http://example.com/204", 5*time.Second)
	if c.TestTarget() != "http://example.com/204" {
		t.Errorf("target = %q", c.TestTarget())
	}
	if c.timeout != 5*time.Second {
		t.Errorf("timeout = %v, want 5s", c.timeout)
	}
}

func TestHTTPProxyURL(t *testing.T) {
	node := &model.ProxyNode{Host: "1.2.3.4", Port: 8080, Protocol: model.ProtocolHTTP}
	u, err := httpProxyURL(node)
	if err != nil {
		t.Fatalf("httpProxyURL: %v", err)
	}
	if u.Scheme != "http" {
		t.Errorf("scheme = %q, want http", u.Scheme)
	}
	if u.Host != "1.2.3.4:8080" {
		t.Errorf("host = %q, want 1.2.3.4:8080", u.Host)
	}
	if u.User != nil {
		t.Errorf("expected no auth, got %v", u.User)
	}
}

func TestHTTPProxyURLSocks5(t *testing.T) {
	node := &model.ProxyNode{Host: "5.6.7.8", Port: 1080, Protocol: model.ProtocolSOCKS5}
	u, err := httpProxyURL(node)
	if err != nil {
		t.Fatalf("httpProxyURL: %v", err)
	}
	if u.Scheme != "socks5" {
		t.Errorf("scheme = %q, want socks5", u.Scheme)
	}
}

func TestHTTPProxyURLWithAuth(t *testing.T) {
	node := &model.ProxyNode{
		Host: "1.2.3.4", Port: 8080, Protocol: model.ProtocolHTTP,
		Username: "user", Password: "pass",
	}
	u, err := httpProxyURL(node)
	if err != nil {
		t.Fatalf("httpProxyURL: %v", err)
	}
	if u.User == nil {
		t.Fatal("expected auth in URL")
	}
	if u.User.Username() != "user" {
		t.Errorf("username = %q, want user", u.User.Username())
	}
	if pw, ok := u.User.Password(); !ok || pw != "pass" {
		t.Errorf("password = %q (ok=%v), want pass", pw, ok)
	}
}

func TestProxyURLWith(t *testing.T) {
	node := &model.ProxyNode{Host: "10.0.0.1", Port: 3128, Protocol: model.ProtocolHTTPS}
	u := proxyURLWith(node)
	if u.Scheme != "http" {
		t.Errorf("scheme = %q, want http (proxyURLWith always http)", u.Scheme)
	}
	if u.Host != "10.0.0.1:3128" {
		t.Errorf("host = %q", u.Host)
	}
}

func TestConnectTCPNilNode(t *testing.T) {
	_, err := ConnectTCP(nil, "example.com:80", time.Second)
	if err == nil {
		t.Fatal("expected error for nil node")
	}
}

func TestConnectTCPUnsupportedProtocol(t *testing.T) {
	// 未知协议应返回错误（不依赖网络：dial 到本地未监听端口会失败，
	// 但 unsupported protocol 分支在 dial 之后；这里只验证错误非 nil）
	node := &model.ProxyNode{Host: "127.0.0.1", Port: 1, Protocol: "unknown"}
	_, err := ConnectTCP(node, "example.com:80", 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected error for unsupported protocol")
	}
}

func TestConnectTCPTimeout(t *testing.T) {
	// 连接不可达地址应超时并返回错误
	node := &model.ProxyNode{Host: "192.0.2.1", Port: 80, Protocol: model.ProtocolHTTP}
	_, err := ConnectTCP(node, "example.com:80", 50*time.Millisecond)
	if err == nil {
		t.Fatal("expected error connecting to unreachable proxy")
	}
}

func TestProxyURLRoundTrip(t *testing.T) {
	// 验证构造出的 URL 可被 url.Parse 正常解析
	node := &model.ProxyNode{Host: "1.2.3.4", Port: 8080, Protocol: model.ProtocolHTTP}
	u, err := httpProxyURL(node)
	if err != nil {
		t.Fatalf("httpProxyURL: %v", err)
	}
	parsed, err := url.Parse(u.String())
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", u.String(), err)
	}
	if parsed.Host != "1.2.3.4:8080" {
		t.Errorf("parsed host = %q", parsed.Host)
	}
}

// ---------- Check 探测 ----------

func TestCheckSuccess(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer target.Close()

	proxyAddr, closeProxy := startMockHTTPProxy(t)
	defer closeProxy()
	host, portStr, _ := net.SplitHostPort(proxyAddr)
	port, _ := strconv.Atoi(portStr)

	node := &model.ProxyNode{Host: host, Port: port, Protocol: model.ProtocolHTTP}
	c := NewChecker(target.URL, 5*time.Second)
	result, err := c.Check(node)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !result.OK {
		t.Errorf("Check OK = false, want true (err=%s)", result.Error)
	}
	if result.Latency < 0 {
		t.Errorf("Latency = %d, want >= 0", result.Latency)
	}
}

func TestCheckTargetError(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer target.Close()

	proxyAddr, closeProxy := startMockHTTPProxy(t)
	defer closeProxy()
	host, portStr, _ := net.SplitHostPort(proxyAddr)
	port, _ := strconv.Atoi(portStr)

	node := &model.ProxyNode{Host: host, Port: port, Protocol: model.ProtocolHTTP}
	c := NewChecker(target.URL, 5*time.Second)
	result, err := c.Check(node)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if result.OK {
		t.Error("Check OK = true, want false for 500 target")
	}
}

func TestCheckUnreachableProxy(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer target.Close()

	node := &model.ProxyNode{Host: "127.0.0.1", Port: 1, Protocol: model.ProtocolHTTP}
	c := NewChecker(target.URL, 500*time.Millisecond)
	result, err := c.Check(node)
	// 连接失败时 err 可能非 nil，也可能返回 result.OK=false
	if err == nil && result.OK {
		t.Error("expected failure for unreachable proxy")
	}
}

// ---------- ConnectTCP 隧道 ----------

func TestConnectTCPHTTP(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "hello")
	}))
	defer target.Close()
	targetHost, targetPortStr, _ := net.SplitHostPort(strings.TrimPrefix(target.URL, "http://"))

	proxyAddr, closeProxy := startMockHTTPProxy(t)
	defer closeProxy()
	host, portStr, _ := net.SplitHostPort(proxyAddr)
	port, _ := strconv.Atoi(portStr)

	node := &model.ProxyNode{Host: host, Port: port, Protocol: model.ProtocolHTTP}
	conn, err := ConnectTCP(node, net.JoinHostPort(targetHost, targetPortStr), 5*time.Second)
	if err != nil {
		t.Fatalf("ConnectTCP: %v", err)
	}
	defer func() { _ = conn.Close() }()
	// 通过隧道发送 HTTP 请求，验证字节完整转发
	if _, err := fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", net.JoinHostPort(targetHost, targetPortStr)); err != nil {
		t.Fatalf("write: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "hello" {
		t.Errorf("response = %d %q, want 200 hello", resp.StatusCode, body)
	}
}

func TestConnectTCPHTTPRejected(t *testing.T) {
	// 一个拒绝 CONNECT 的代理（返回 403）
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				br := bufio.NewReader(c)
				_, _ = br.ReadString('\n')
				_, _ = c.Write([]byte("HTTP/1.1 403 Forbidden\r\n\r\n"))
			}(conn)
		}
	}()
	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)
	node := &model.ProxyNode{Host: host, Port: port, Protocol: model.ProtocolHTTP}
	_, err = ConnectTCP(node, "example.com:80", 5*time.Second)
	if err == nil {
		t.Fatal("expected error for rejected CONNECT")
	}
}

func TestConnectTCPSOCKS5(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "socks-ok")
	}))
	defer target.Close()
	targetHost, targetPortStr, _ := net.SplitHostPort(strings.TrimPrefix(target.URL, "http://"))

	proxyAddr, closeProxy := startMockSOCKS5Proxy(t)
	defer closeProxy()
	host, portStr, _ := net.SplitHostPort(proxyAddr)
	port, _ := strconv.Atoi(portStr)

	node := &model.ProxyNode{Host: host, Port: port, Protocol: model.ProtocolSOCKS5}
	conn, err := ConnectTCP(node, net.JoinHostPort(targetHost, targetPortStr), 5*time.Second)
	if err != nil {
		t.Fatalf("ConnectTCP socks5: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", net.JoinHostPort(targetHost, targetPortStr)); err != nil {
		t.Fatalf("write: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "socks-ok" {
		t.Errorf("response = %d %q, want 200 socks-ok", resp.StatusCode, body)
	}
}

// ---------- mock 代理 ----------

// startMockHTTPProxy 启动一个直连 CONNECT 代理：
// 收到 `CONNECT host:port` 后直接拨号目标并双向转发，作为测试中的上游 HTTP 节点。
func startMockHTTPProxy(t *testing.T) (addr string, closeFn func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen mock http proxy: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleMockCONNECT(conn)
		}
	}()
	return ln.Addr().String(), func() { _ = ln.Close() }
}

func handleMockCONNECT(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	br := bufio.NewReader(conn)
	line, err := br.ReadString('\n')
	if err != nil {
		return
	}
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return
	}
	method := strings.ToUpper(fields[0])
	if method == "CONNECT" {
		// CONNECT 隧道：拨号目标并双向转发
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
		return
	}
	// 绝对形式请求（如 `GET http://host:port/path HTTP/1.1`）：解析目标并转发
	u, err := url.Parse(fields[1])
	if err != nil || u.Host == "" {
		_, _ = conn.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
		return
	}
	out, err := net.Dial("tcp", u.Host)
	if err != nil {
		_, _ = conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return
	}
	defer func() { _ = out.Close() }()
	// 请求行改写为 origin-form 后转发
	version := "HTTP/1.1"
	if len(fields) >= 3 {
		version = fields[2]
	}
	if _, err := fmt.Fprintf(out, "%s %s %s\r\n", method, u.RequestURI(), version); err != nil {
		return
	}
	// 转发剩余请求头
	for {
		l, err := br.ReadString('\n')
		if err != nil {
			return
		}
		if _, err := out.Write([]byte(l)); err != nil {
			return
		}
		if l == "\r\n" || l == "\n" {
			break
		}
	}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(out, br); _ = out.Close(); done <- struct{}{} }()
	go func() { _, _ = io.Copy(conn, out); _ = conn.Close(); done <- struct{}{} }()
	<-done
	<-done
}

// startMockSOCKS5Proxy 启动一个无认证的直连 SOCKS5 代理。
func startMockSOCKS5Proxy(t *testing.T) (addr string, closeFn func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen mock socks5 proxy: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleMockSOCKS5(conn)
		}
	}()
	return ln.Addr().String(), func() { _ = ln.Close() }
}

func handleMockSOCKS5(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	br := bufio.NewReader(conn)
	// 握手：版本 + 方法数
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
	if _, err := conn.Write([]byte{0x05, 0x00}); err != nil { // 选择无认证
		return
	}
	// 请求：版本、命令、保留、ATYP
	req := make([]byte, 4)
	if _, err := io.ReadFull(br, req); err != nil {
		return
	}
	if req[0] != 0x05 || req[1] != 0x01 {
		return
	}
	var host string
	switch req[3] {
	case 0x01:
		b := make([]byte, 4)
		if _, err := io.ReadFull(br, b); err != nil {
			return
		}
		host = net.IP(b).String()
	case 0x03:
		l := make([]byte, 1)
		if _, err := io.ReadFull(br, l); err != nil {
			return
		}
		b := make([]byte, int(l[0]))
		if _, err := io.ReadFull(br, b); err != nil {
			return
		}
		host = string(b)
	case 0x04:
		b := make([]byte, 16)
		if _, err := io.ReadFull(br, b); err != nil {
			return
		}
		host = net.IP(b).String()
	default:
		return
	}
	pb := make([]byte, 2)
	if _, err := io.ReadFull(br, pb); err != nil {
		return
	}
	port := int(pb[0])<<8 | int(pb[1])
	out, err := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		_, _ = conn.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	defer func() { _ = out.Close() }()
	if _, err := conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(out, br); _ = out.Close(); done <- struct{}{} }()
	go func() { _, _ = io.Copy(conn, out); _ = conn.Close(); done <- struct{}{} }()
	<-done
	<-done
}
