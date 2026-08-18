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
	"sync/atomic"
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

func TestNewCheckerWithAnonymityDefaults(t *testing.T) {
	c := NewCheckerWithAnonymity("", "", 0)
	if c.TestTarget() != "https://www.apple.com/library/test/success.html" {
		t.Errorf("default target = %q", c.TestTarget())
	}
	if c.anonymityTarget != DefaultAnonymityTarget {
		t.Errorf("default anonymity target = %q, want %q", c.anonymityTarget, DefaultAnonymityTarget)
	}
	if c.timeout != 10*time.Second {
		t.Errorf("default timeout = %v, want 10s", c.timeout)
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
	if u.Scheme != "" {
		t.Errorf("scheme = %q, want empty (scheme set by httpProxyURL)", u.Scheme)
	}
	if u.Host != "10.0.0.1:3128" {
		t.Errorf("host = %q", u.Host)
	}
}

func TestHTTPProxyURLHTTPS(t *testing.T) {
	node := &model.ProxyNode{Host: "6.7.8.9", Port: 8443, Protocol: model.ProtocolHTTPS}
	u, err := httpProxyURL(node)
	if err != nil {
		t.Fatalf("httpProxyURL: %v", err)
	}
	if u.Scheme != "https" {
		t.Errorf("scheme = %q, want https", u.Scheme)
	}
	if u.Host != "6.7.8.9:8443" {
		t.Errorf("host = %q, want 6.7.8.9:8443", u.Host)
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

// ---------- 匿名性探测 ----------

// TestProbeAnonymityParsesEcho 验证回显端点返回的 origin/headers 能被正确解析：
// 回显端点固定返回一个“代理注入”了 X-Forwarded-For 与 Via 的响应，
// 用于验证头泄漏与代理特征的识别逻辑（不依赖真实网络）。
func TestProbeAnonymityParsesEcho(t *testing.T) {
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"origin": "203.0.113.9", "headers": {"X-Forwarded-For": "10.0.0.1", "Via": "1.1 squid", "User-Agent": "ProxyPilot/0.1"}}`)
	}))
	defer echo.Close()

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer target.Close()

	proxyAddr, closeProxy := startMockHTTPProxy(t)
	defer closeProxy()
	host, portStr, _ := net.SplitHostPort(proxyAddr)
	port, _ := strconv.Atoi(portStr)

	node := &model.ProxyNode{Host: host, Port: port, Protocol: model.ProtocolHTTP}
	c := NewCheckerWithAnonymity(target.URL, echo.URL, 5*time.Second)
	result, err := c.Check(node)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !result.OK {
		t.Fatalf("Check OK = false, want true (err=%s)", result.Error)
	}
	if result.Anonymity == nil {
		t.Fatal("Anonymity probe = nil, want non-nil")
	}
	if result.Anonymity.ProxiedIP != "203.0.113.9" {
		t.Errorf("ProxiedIP = %q, want 203.0.113.9", result.Anonymity.ProxiedIP)
	}
	if result.Anonymity.DirectIP != "203.0.113.9" {
		t.Errorf("DirectIP = %q, want 203.0.113.9", result.Anonymity.DirectIP)
	}
	if len(result.Anonymity.HeaderLeaks) != 1 || !strings.Contains(result.Anonymity.HeaderLeaks[0], "X-Forwarded-For") {
		t.Errorf("HeaderLeaks = %v, want [X-Forwarded-For...]", result.Anonymity.HeaderLeaks)
	}
	if len(result.Anonymity.ProxyMarkers) != 1 || !strings.Contains(result.Anonymity.ProxyMarkers[0], "Via") {
		t.Errorf("ProxyMarkers = %v, want [Via...]", result.Anonymity.ProxyMarkers)
	}
}

// TestProbeAnonymityEchoUnreachable 验证回显端点不可达时：
// 连通性结果不受影响（仍 OK），但 Anonymity 为 nil（上层回退启发式）。
func TestProbeAnonymityEchoUnreachable(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer target.Close()

	proxyAddr, closeProxy := startMockHTTPProxy(t)
	defer closeProxy()
	host, portStr, _ := net.SplitHostPort(proxyAddr)
	port, _ := strconv.Atoi(portStr)

	node := &model.ProxyNode{Host: host, Port: port, Protocol: model.ProtocolHTTP}
	// 回显端点指向一个未监听的端口，直连请求必然失败
	c := NewCheckerWithAnonymity(target.URL, "http://127.0.0.1:1/anything", 500*time.Millisecond)
	result, err := c.Check(node)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !result.OK {
		t.Fatalf("Check OK = false, want true (err=%s)", result.Error)
	}
	if result.Anonymity != nil {
		t.Error("Anonymity probe = non-nil, want nil when echo unreachable")
	}
}

// TestProbeAnonymitySecondSample 验证第二次经代理采样：
// 回显端点按请求顺序返回不同 origin，探测结果应填充 ProxiedIP2（轮换检测数据）。
// 直连第 1 次、经代理第 2、3 次，串行调用无并发。
func TestProbeAnonymitySecondSample(t *testing.T) {
	var count atomic.Int32
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := count.Add(1)
		origin := "203.0.113.1"
		switch n {
		case 2:
			origin = "203.0.113.9" // 代理第 1 次
		case 3:
			origin = "203.0.113.77" // 代理第 2 次（轮换）
		}
		_, _ = io.WriteString(w, fmt.Sprintf(`{"origin": %q, "headers": {}}`, origin))
	}))
	defer echo.Close()

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer target.Close()

	proxyAddr, closeProxy := startMockHTTPProxy(t)
	defer closeProxy()
	host, portStr, _ := net.SplitHostPort(proxyAddr)
	port, _ := strconv.Atoi(portStr)

	node := &model.ProxyNode{Host: host, Port: port, Protocol: model.ProtocolHTTP}
	c := NewCheckerWithAnonymity(target.URL, echo.URL, 5*time.Second)
	result, err := c.Check(node)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if result.Anonymity == nil {
		t.Fatal("Anonymity probe = nil, want non-nil")
	}
	if result.Anonymity.DirectIP != "203.0.113.1" {
		t.Errorf("DirectIP = %q, want 203.0.113.1", result.Anonymity.DirectIP)
	}
	if result.Anonymity.ProxiedIP != "203.0.113.9" {
		t.Errorf("ProxiedIP = %q, want 203.0.113.9", result.Anonymity.ProxiedIP)
	}
	if result.Anonymity.ProxiedIP2 != "203.0.113.77" {
		t.Errorf("ProxiedIP2 = %q, want 203.0.113.77 (rotating sample)", result.Anonymity.ProxiedIP2)
	}
}

// TestProbeAnonymityReqIssues 验证请求被代理改写时能识别连接信息问题：
// 回显端点固定返回一个与请求目标 host 不一致的 url 字段，应记录 ReqIssues。
func TestProbeAnonymityReqIssues(t *testing.T) {
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"origin": "203.0.113.9", "url": "http://evil.example/rewritten", "headers": {"Host": "evil.example"}}`)
	}))
	defer echo.Close()

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer target.Close()

	proxyAddr, closeProxy := startMockHTTPProxy(t)
	defer closeProxy()
	host, portStr, _ := net.SplitHostPort(proxyAddr)
	port, _ := strconv.Atoi(portStr)

	node := &model.ProxyNode{Host: host, Port: port, Protocol: model.ProtocolHTTP}
	c := NewCheckerWithAnonymity(target.URL, echo.URL, 5*time.Second)
	result, err := c.Check(node)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if result.Anonymity == nil {
		t.Fatal("Anonymity probe = nil, want non-nil")
	}
	if len(result.Anonymity.ReqIssues) < 2 {
		t.Fatalf("ReqIssues = %v, want >= 2 items (url + host rewritten)", result.Anonymity.ReqIssues)
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

// TestConnectTCPHTTPS 验证 https:// 代理节点（ProtocolHTTPS）：客户端到代理
// 服务器的连接先做 TLS 握手，再在其上发 CONNECT。用 httptest.NewTLSServer 提供
// 自签 TLS 层（证书不可信，ConnectTCP 走 InsecureSkipVerify 才能握手成功）。
func TestConnectTCPHTTPS(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "https-ok")
	}))
	defer target.Close()
	targetHost, targetPortStr, _ := net.SplitHostPort(strings.TrimPrefix(target.URL, "http://"))

	// TLS CONNECT 代理：外层是 TLS，收到 CONNECT 后 hijack 并直连目标转发
	proxy := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "expected CONNECT", http.StatusBadRequest)
			return
		}
		out, err := net.Dial("tcp", r.Host)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			_ = out.Close()
			http.Error(w, "hijack unsupported", http.StatusInternalServerError)
			return
		}
		client, _, err := hj.Hijack()
		if err != nil {
			_ = out.Close()
			return
		}
		if _, err := client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
			_ = client.Close()
			_ = out.Close()
			return
		}
		done := make(chan struct{}, 2)
		go func() { _, _ = io.Copy(out, client); _ = out.Close(); done <- struct{}{} }()
		go func() { _, _ = io.Copy(client, out); _ = client.Close(); done <- struct{}{} }()
		<-done
		<-done
	}))
	defer proxy.Close()
	_, proxyPortStr, _ := net.SplitHostPort(strings.TrimPrefix(proxy.URL, "https://"))
	proxyPort, _ := strconv.Atoi(proxyPortStr)

	node := &model.ProxyNode{Host: "127.0.0.1", Port: proxyPort, Protocol: model.ProtocolHTTPS}
	conn, err := ConnectTCP(node, net.JoinHostPort(targetHost, targetPortStr), 5*time.Second)
	if err != nil {
		t.Fatalf("ConnectTCP https: %v", err)
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
	if resp.StatusCode != http.StatusOK || string(body) != "https-ok" {
		t.Errorf("response = %d %q, want 200 https-ok", resp.StatusCode, body)
	}
}

// TestConnectTCPHTTPSWrongProtocol 验证：节点标为 https 但代理实际是明文 HTTP
// CONNECT 时，TLS 握手会失败并返回错误（而不是静默建立明文隧道）。
func TestConnectTCPHTTPSWrongProtocol(t *testing.T) {
	proxyAddr, closeProxy := startMockHTTPProxy(t)
	defer closeProxy()
	host, portStr, _ := net.SplitHostPort(proxyAddr)
	port, _ := strconv.Atoi(portStr)

	node := &model.ProxyNode{Host: host, Port: port, Protocol: model.ProtocolHTTPS}
	_, err := ConnectTCP(node, "example.com:80", 5*time.Second)
	if err == nil {
		t.Fatal("expected TLS handshake failure against plain HTTP proxy")
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

// TestSocks5UserPassAuthOK 完成 RFC1929 认证，服务端确认成功。
func TestSocks5UserPassAuthOK(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = server.Close(); _ = client.Close() }()

	// 服务端读取握手请求，返回认证成功响应。
	go func() {
		head := make([]byte, 2)
		if _, err := io.ReadFull(server, head); err != nil {
			return
		}
		if head[0] != 0x01 {
			return
		}
		user := make([]byte, int(head[1]))
		if _, err := io.ReadFull(server, user); err != nil {
			return
		}
		passLen := make([]byte, 1)
		if _, err := io.ReadFull(server, passLen); err != nil {
			return
		}
		pass := make([]byte, int(passLen[0]))
		if _, err := io.ReadFull(server, pass); err != nil {
			return
		}
		if string(user) == "user" && string(pass) == "pass" {
			_, _ = server.Write([]byte{0x01, 0x00})
		} else {
			_, _ = server.Write([]byte{0x01, 0x01})
		}
	}()

	if err := socks5UserPassAuth(client, "user", "pass"); err != nil {
		t.Fatalf("socks5UserPassAuth: %v", err)
	}
}

// TestSocks5UserPassAuthFailure 服务端拒绝认证时返回错误。
func TestSocks5UserPassAuthFailure(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = server.Close(); _ = client.Close() }()

	go func() {
		buf := make([]byte, 9) // 0x01 + len("foo") + "foo" + len("bar") + "bar"
		if _, err := io.ReadFull(server, buf); err != nil {
			return
		}
		_, _ = server.Write([]byte{0x01, 0x01}) // 认证失败
	}()

	if err := socks5UserPassAuth(client, "foo", "bar"); err == nil {
		t.Fatal("expected auth failure error")
	}
}

// TestSocks5UserPassAuthEmptyUser 空用户名直接报错，不发送握手。
func TestSocks5UserPassAuthEmptyUser(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = server.Close(); _ = client.Close() }()

	if err := socks5UserPassAuth(client, "", "pass"); err == nil {
		t.Fatal("expected error for empty username")
	}
}

// TestWriteSocks5UserPass 发送用户名/密码子协商（UDP 会话路径）。
func TestWriteSocks5UserPass(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = server.Close(); _ = client.Close() }()

	go func() {
		buf := make([]byte, 14) // 0x01 + len("user") + "user" + len("pass") + "pass"
		n, err := io.ReadFull(server, buf)
		if err != nil {
			return
		}
		if n != 14 {
			return
		}
		_ = server.Close()
	}()

	if err := writeSocks5UserPass(client, "user", "pass"); err != nil {
		t.Fatalf("writeSocks5UserPass: %v", err)
	}
}

// TestWriteSocks5UserPassTooLong 超长凭据（>255 字节）直接报错。
func TestWriteSocks5UserPassTooLong(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = server.Close(); _ = client.Close() }()

	long := strings.Repeat("x", 300)
	if err := writeSocks5UserPass(client, long, "pass"); err == nil {
		t.Fatal("expected error for over-long username")
	}
}
