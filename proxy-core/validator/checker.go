package validator

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/axetroy/ProxyPilot/proxy-core/model"
	"golang.org/x/net/proxy"
)

// DefaultAnonymityTarget 匿名性检测的回显端点默认值：
// 返回请求来源 IP 与收到的请求头，用于对比“直连 vs 经代理”的差异。
const DefaultAnonymityTarget = "https://httpbin.org/anything"

// 匿名性探测关注的泄漏头（代理在转发请求时可能注入的真实客户端信息）。
var leakHeaders = []string{"X-Forwarded-For", "Forwarded", "X-Real-IP"}

// 匿名性探测关注的代理特征头（暴露代理身份/软件）。
var proxyMarkerHeaders = []string{"Via", "X-Via", "Proxy-Agent", "X-Proxy", "X-Proxy-Id"}

// Checker validates a single proxy node by connecting to a target through it.
type Checker struct {
	target          string
	anonymityTarget string
	timeout         time.Duration
}

func NewChecker(target string, timeout time.Duration) *Checker {
	return NewCheckerWithAnonymity(target, DefaultAnonymityTarget, timeout)
}

// NewCheckerWithAnonymity 构造检测器，anonymityTarget 为空时使用默认回显端点。
func NewCheckerWithAnonymity(target, anonymityTarget string, timeout time.Duration) *Checker {
	if target == "" {
		target = "https://www.apple.com/library/test/success.html"
	}
	if anonymityTarget == "" {
		anonymityTarget = DefaultAnonymityTarget
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Checker{target: target, anonymityTarget: anonymityTarget, timeout: timeout}
}

func (c *Checker) TestTarget() string { return c.target }

// Check 通过代理节点对一个轻量目标做一次探测。
// 这里测量的是“代理能否快速建立连接并返回一个简单响应”的耗时，
// 不是完整页面加载时间，也不是浏览器级别的端到端耗时。
func (c *Checker) Check(node *model.ProxyNode) (model.CheckResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	transport := &http.Transport{
		Proxy:                 func(*http.Request) (*url.URL, error) { return httpProxyURL(node) },
		DialContext:           (&net.Dialer{Timeout: c.timeout}).DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		DisableKeepAlives:     true,
		// https:// 代理的地址常为 IP 或自签证书，跳过校验否则可用代理也会被判死
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.target, nil)
	if err != nil {
		return model.CheckResult{OK: false}, err
	}
	req.Header.Set("User-Agent", "ProxyPilot/0.1")

	start := time.Now()
	client := &http.Client{Transport: transport, Timeout: c.timeout}
	resp, err := client.Do(req)
	if err != nil {
		return model.CheckResult{OK: false, Error: err.Error(), Latency: time.Since(start).Milliseconds()}, err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))

	latency := time.Since(start).Milliseconds()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return model.CheckResult{
			OK:      false,
			Latency: latency,
			Error:   fmt.Sprintf("target returned %d", resp.StatusCode),
		}, nil
	}

	result := model.CheckResult{OK: true, Latency: latency}
	// 连通性通过后再做匿名性探测；探测失败不影响连通性结论。
	result.Anonymity = c.probeAnonymity(node)
	return result, nil
}

// probeAnonymity 通过“直连 + 经代理 ×2”请求回显端点：
//   - 对比直连/代理出口 IP 判断源 IP 是否隐藏；
//   - 检查目标收到的请求头判断头泄漏与代理特征；
//   - 第二次经代理采样出口 IP，识别轮换代理；
//   - 对比回显端收到的 URL/Host 与请求目标，识别请求被改写。
//
// 任一环节失败（网络不通/端点格式不符）返回 nil，由上层回退到启发式评分。
func (c *Checker) probeAnonymity(node *model.ProxyNode) *model.AnonymityProbe {
	direct, err := c.fetchEcho(node, false)
	if err != nil {
		return nil
	}
	proxied, err := c.fetchEcho(node, true)
	if err != nil {
		return nil
	}
	// 第二次经代理采样：仅用于轮换检测，失败不影响结果。
	proxied2 := ""
	if again, err := c.fetchEcho(node, true); err == nil {
		proxied2 = again.origin
	}
	_ = direct.headers // 直连头仅用于对照，暂不参与评分

	probe := &model.AnonymityProbe{
		DirectIP:   direct.origin,
		ProxiedIP:  proxied.origin,
		ProxiedIP2: proxied2,
	}
	for _, h := range leakHeaders {
		if v := proxied.headers.Get(h); v != "" {
			probe.HeaderLeaks = append(probe.HeaderLeaks, h+": "+v)
		}
	}
	for _, h := range proxyMarkerHeaders {
		if v := proxied.headers.Get(h); v != "" {
			probe.ProxyMarkers = append(probe.ProxyMarkers, h+": "+v)
		}
	}
	// 连接信息：回显端收到的 URL/Host 与请求目标不一致 → 请求被代理改写。
	if reqURL, err := url.Parse(c.anonymityTarget); err == nil {
		if got := proxied.receivedURL; got != "" {
			if gotURL, err := url.Parse(got); err == nil && gotURL.Host != "" && gotURL.Host != reqURL.Host {
				probe.ReqIssues = append(probe.ReqIssues, "回显端收到的请求 URL 与目标不一致: "+got)
			}
		}
		if got := proxied.headers.Get("Host"); got != "" && got != reqURL.Host {
			probe.ReqIssues = append(probe.ReqIssues, "回显端收到的 Host 头与目标不一致: "+got)
		}
	}
	return probe
}

// echoResult 是一次回显探测的解析结果。
type echoResult struct {
	origin      string
	headers     http.Header
	receivedURL string // 回显端声称收到的请求 URL
}

// fetchEcho 请求回显端点并解析来源 IP、收到的请求头与请求 URL。
// viaProxy 为 true 时请求经 node 代理转发，否则直连。
func (c *Checker) fetchEcho(node *model.ProxyNode, viaProxy bool) (echoResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: c.timeout}).DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		DisableKeepAlives:     true,
		// 同上：https 代理跳过证书校验
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	if viaProxy {
		transport.Proxy = func(*http.Request) (*url.URL, error) { return httpProxyURL(node) }
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.anonymityTarget, nil)
	if err != nil {
		return echoResult{}, err
	}
	req.Header.Set("User-Agent", "ProxyPilot/0.1")

	client := &http.Client{Transport: transport, Timeout: c.timeout}
	resp, err := client.Do(req)
	if err != nil {
		return echoResult{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return echoResult{}, fmt.Errorf("echo target returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return echoResult{}, err
	}
	var echo struct {
		Origin  string            `json:"origin"`
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
	}
	if err := json.Unmarshal(body, &echo); err != nil {
		return echoResult{}, err
	}

	headers := make(http.Header)
	for k, v := range echo.Headers {
		headers.Set(k, strings.TrimSpace(v))
	}
	return echoResult{
		origin:      strings.TrimSpace(echo.Origin),
		headers:     headers,
		receivedURL: strings.TrimSpace(echo.URL),
	}, nil
}

func proxyURLWith(node *model.ProxyNode) *url.URL {
	u := &url.URL{}
	u.Host = net.JoinHostPort(node.Host, strconv.Itoa(node.Port))
	if node.Username != "" {
		u.User = url.UserPassword(node.Username, node.Password)
	}
	return u
}

func httpProxyURL(node *model.ProxyNode) (*url.URL, error) {
	u := proxyURLWith(node)
	switch node.Protocol {
	case model.ProtocolSOCKS5:
		u.Scheme = "socks5"
	case model.ProtocolHTTPS:
		// https:// 代理：到代理服务器的连接走 TLS，由 http.Transport 自动建立。
		u.Scheme = "https"
	default:
		u.Scheme = "http"
	}
	return u, nil
}

// ConnectTCP establishes a TCP tunnel to addr through the node (CONNECT for http
// proxies, SOCKS5 handshake for socks5). Returns the tunneled connection.
// ProtocolHTTPS 节点要求客户端到代理服务器的连接本身走 TLS（https:// 代理），
// 因此先在 TCP 之上完成 TLS 握手，再在其上发送 CONNECT。
func ConnectTCP(node *model.ProxyNode, addr string, timeout time.Duration) (net.Conn, error) {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if node == nil {
		return nil, fmt.Errorf("nil proxy node")
	}
	if node.Protocol == model.ProtocolSOCKS5 {
		return socks5Connect(node, addr, timeout)
	}
	proxyAddr := net.JoinHostPort(node.Host, strconv.Itoa(node.Port))
	conn, err := net.DialTimeout("tcp", proxyAddr, timeout)
	if err != nil {
		return nil, err
	}
	var handshakeErr error
	switch node.Protocol {
	case model.ProtocolHTTPS:
		// https:// 代理：与代理服务器之间先建立 TLS 隧道，再发 CONNECT。
		// InsecureSkipVerify 关闭证书校验：代理地址常为 IP/自签证书，
		// 校验没有意义且会直接导致可用代理被判死。
		tlsConn := tls.Client(conn, &tls.Config{
			ServerName:         node.Host,
			InsecureSkipVerify: true,
		})
		if err := tlsConn.Handshake(); err != nil {
			_ = conn.Close()
			return nil, err
		}
		handshakeErr = httpConnect(tlsConn, node, addr)
		if handshakeErr != nil {
			_ = tlsConn.Close()
			return nil, handshakeErr
		}
		return tlsConn, nil
	case model.ProtocolHTTP:
		handshakeErr = httpConnect(conn, node, addr)
	default:
		handshakeErr = fmt.Errorf("unsupported protocol %q", node.Protocol)
	}
	if handshakeErr != nil {
		_ = conn.Close()
		return nil, handshakeErr
	}
	return conn, nil
}

func httpConnect(conn net.Conn, node *model.ProxyNode, addr string) error {
	req := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: addr},
		Host:   addr,
	}
	if node.Username != "" {
		token := base64.StdEncoding.EncodeToString([]byte(node.Username + ":" + node.Password))
		req.Header = http.Header{}
		req.Header.Set("Proxy-Authorization", "Basic "+token)
	}
	if err := req.Write(conn); err != nil {
		return err
	}
	reader := bufio.NewReaderSize(conn, 16<<10)
	resp, err := http.ReadResponse(reader, req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("CONNECT handshake failed: %s", resp.Status)
	}
	return nil
}

// socks5Connect establishes a TCP connection through a SOCKS5 proxy node.
func socks5Connect(node *model.ProxyNode, addr string, timeout time.Duration) (net.Conn, error) {
	auth := (*proxy.Auth)(nil)
	if node.Username != "" {
		auth = &proxy.Auth{User: node.Username, Password: node.Password}
	}

	dialer, err := proxy.SOCKS5("tcp", net.JoinHostPort(node.Host, strconv.Itoa(node.Port)), auth, proxy.Direct)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if ctxDialer, ok := dialer.(proxy.ContextDialer); ok {
		return ctxDialer.DialContext(ctx, "tcp", addr)
	}
	return dialer.Dial("tcp", addr)
}
