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
)

// DefaultSafetyTarget 连接安全检测的回显端点默认值：
// 返回请求来源 IP 与收到的请求头，用于对比“直连 vs 经代理”的差异。
const DefaultSafetyTarget = "https://httpbin.org/anything"

// 连接安全探测关注的泄漏头（代理在转发请求时可能注入的真实客户端信息）。
var leakHeaders = []string{"X-Forwarded-For", "Forwarded", "X-Real-IP"}

// 连接安全探测关注的代理特征头（暴露代理身份/软件）。
var proxyMarkerHeaders = []string{"Via", "X-Via", "Proxy-Agent", "X-Proxy", "X-Proxy-Id"}

// Checker validates a single proxy node by connecting to a target through it.
type Checker struct {
	target          string
	safetyTarget string
	timeout         time.Duration
}

func NewChecker(target string, timeout time.Duration) *Checker {
	return NewCheckerWithSafety(target, DefaultSafetyTarget, timeout)
}

// NewCheckerWithSafety 构造检测器，safetyTarget 为空时使用默认回显端点。
func NewCheckerWithSafety(target, safetyTarget string, timeout time.Duration) *Checker {
	if target == "" {
		target = "https://www.apple.com/library/test/success.html"
	}
	if safetyTarget == "" {
		safetyTarget = DefaultSafetyTarget
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Checker{target: target, safetyTarget: safetyTarget, timeout: timeout}
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
	// 连通性通过后再做连接安全探测；探测失败不影响连通性结论。
	result.Safety = c.probeSafety(node)
	return result, nil
}

// probeSafety 通过“直连 + 经代理 ×2”请求回显端点：
//   - 对比直连/代理出口 IP 判断源 IP 是否隐藏；
//   - 检查目标收到的请求头判断头泄漏与代理特征；
//   - 第二次经代理采样出口 IP，识别轮换代理；
//   - 对比回显端收到的 URL/Host 与请求目标，识别请求被改写。
//
// 任一环节失败（网络不通/端点格式不符）返回 nil，由上层回退到启发式评分。
func (c *Checker) probeSafety(node *model.ProxyNode) *model.SafetyProbe {
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

	probe := &model.SafetyProbe{
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
	if reqURL, err := url.Parse(c.safetyTarget); err == nil {
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

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.safetyTarget, nil)
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
	conn, err := dialNode(node, timeout)
	if err != nil {
		return nil, err
	}
	if err := handshake(conn, node, addr, timeout); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

// ConnectChain 通过链路建立到目标 addr 的隧道：客户端 → nodes[0] → nodes[1] → … → target。
// 每一跳都在上一层隧道之上按该跳节点的协议握手（HTTP CONNECT / HTTPS TLS+CONNECT / SOCKS5），
// 握手目标是下一跳节点的地址，最后一跳握手到目标地址，最终返回直达 target 的隧道。
// 链路中各跳节点协议可以混合。单跳链路等价于 ConnectTCP。
func ConnectChain(nodes []*model.ProxyNode, target string, timeout time.Duration) (net.Conn, error) {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("empty proxy chain")
	}
	if len(nodes) == 1 {
		return ConnectTCP(nodes[0], target, timeout)
	}

	// 每跳均分总超时：N 跳链路的单跳预算 = 总超时 / N，
	// 避免多跳时每跳都按总超时等待，最坏总耗时膨胀到 N×timeout。
	perHop := timeout / time.Duration(len(nodes))
	if perHop <= 0 {
		perHop = timeout
	}

	conn, err := dialNode(nodes[0], perHop)
	if err != nil {
		return nil, err
	}
	for i, node := range nodes {
		next := target
		if i+1 < len(nodes) {
			next = net.JoinHostPort(nodes[i+1].Host, strconv.Itoa(nodes[i+1].Port))
		}
		if err := handshake(conn, node, next, perHop); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("chain hop %d (%s) handshake to %s failed: %w", i+1, node.Key(), next, err)
		}
	}
	return conn, nil
}

// chainHopKey 返回链路节点在测试结果中的展示地址：protocol://host:port。
func chainHopKey(n *model.ProxyNode) string {
	return string(n.Protocol) + "://" + net.JoinHostPort(n.Host, strconv.Itoa(n.Port))
}

// TestChain 逐跳测试链路连通性并测量每跳耗时：
// 依次建立 客户端 → nodes[0] → … → target 的隧道，记录每一跳的建链耗时；
// 某一跳失败时停止测试，该跳及后续跳的 Error 字段给出失败原因。
// 不依赖外部网络的目标：target 传需要到达的地址（如检测目标 host:port）。
func TestChain(nodes []*model.ProxyNode, target string, timeout time.Duration) model.ChainTestResult {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	res := model.ChainTestResult{Hops: make([]model.ChainHopResult, 0, len(nodes))}
	if len(nodes) == 0 {
		return res
	}

	perHop := timeout / time.Duration(len(nodes))
	if perHop <= 0 {
		perHop = timeout
	}

	start := time.Now()
	conn, err := dialNode(nodes[0], perHop)
	if err != nil {
		res.Hops = append(res.Hops, model.ChainHopResult{
			Hop: 1, NodeID: nodes[0].ID,
			Key:      chainHopKey(nodes[0]),
			Protocol: string(nodes[0].Protocol),
			Latency:  time.Since(start).Milliseconds(), Error: err.Error(),
		})
		return res
	}
	for i, node := range nodes {
		next := target
		if i+1 < len(nodes) {
			next = net.JoinHostPort(nodes[i+1].Host, strconv.Itoa(nodes[i+1].Port))
		}
		hopStart := time.Now()
		err := handshake(conn, node, next, perHop)
		latency := time.Since(hopStart).Milliseconds()
		hop := model.ChainHopResult{
			Hop: i + 1, NodeID: node.ID,
			Key:      chainHopKey(node),
			Protocol: string(node.Protocol), Latency: latency,
		}
		if err != nil {
			hop.Error = fmt.Sprintf("handshake to %s failed: %v", next, err)
			res.Hops = append(res.Hops, hop)
			res.TotalLatency = time.Since(start).Milliseconds()
			break
		}
		hop.OK = true
		res.Hops = append(res.Hops, hop)
		if i == len(nodes)-1 {
			res.TotalLatency = time.Since(start).Milliseconds()
		}
	}
	_ = conn.Close()
	if len(res.Hops) == len(nodes) {
		allOK := true
		for _, h := range res.Hops {
			if !h.OK {
				allOK = false
				break
			}
		}
		res.OK = allOK
	}
	return res
}

// ChainError 提取链路测试失败原因：最后一跳的错误信息。
// 测试无任何跳（空链/探测中断）时返回"未知错误"。
func ChainError(res model.ChainTestResult) string {
	if len(res.Hops) == 0 {
		return "未知错误"
	}
	last := res.Hops[len(res.Hops)-1]
	if last.Error != "" {
		return last.Error
	}
	return "链路不可用"
}

// dialNode 建立到代理节点本身的 TCP 连接（HTTPS 代理附带 TLS 握手）。
func dialNode(node *model.ProxyNode, timeout time.Duration) (net.Conn, error) {
	proxyAddr := net.JoinHostPort(node.Host, strconv.Itoa(node.Port))
	conn, err := net.DialTimeout("tcp", proxyAddr, timeout)
	if err != nil {
		return nil, err
	}
	if node.Protocol == model.ProtocolHTTPS {
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
		return tlsConn, nil
	}
	return conn, nil
}

// handshake 在已建立的连接之上完成到 addr 的代理握手（不负责建连）。
// 用于单跳（ConnectTCP）与链路（ConnectChain）复用同一握手逻辑。
// 握手期间对连接设置超时，避免上游代理不响应时连接永久挂起。
func handshake(conn net.Conn, node *model.ProxyNode, addr string, timeout time.Duration) error {
	_ = conn.SetDeadline(time.Now().Add(timeout))
	defer func() { _ = conn.SetDeadline(time.Time{}) }()
	switch node.Protocol {
	case model.ProtocolSOCKS5:
		return socks5Connect(conn, node, addr)
	case model.ProtocolHTTP, model.ProtocolHTTPS:
		return httpConnect(conn, node, addr)
	default:
		return fmt.Errorf("unsupported protocol %q", node.Protocol)
	}
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

// socks5Connect 在已建立的连接之上完成 SOCKS5 握手并 CONNECT 到 addr。
// 手写 RFC1928/1929（打招呼 + 认证协商 + CONNECT），
// 便于在链路中复用上一层隧道作为底层连接（x/net/proxy 的 Dialer 无法在已有连接上握手）。
// 连接超时由调用方（handshake）统一设置。
func socks5Connect(conn net.Conn, node *model.ProxyNode, addr string) error {
	// 打招呼：协商支持的认证方法（无认证 + 用户名/密码，节点带凭据时提供后者）。
	methods := []byte{0x00}
	if node.Username != "" {
		methods = append(methods, 0x02)
	}
	greet := append([]byte{0x05, byte(len(methods))}, methods...)
	if _, err := conn.Write(greet); err != nil {
		return err
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		return err
	}
	if reply[0] != 0x05 {
		return fmt.Errorf("socks5: unsupported version %d", reply[0])
	}
	switch reply[1] {
	case 0x00: // 无认证
	case 0x02: // 用户名/密码（RFC1929）
		if err := socks5UserPassAuth(conn, node.Username, node.Password); err != nil {
			return err
		}
	default:
		return fmt.Errorf("socks5: no acceptable auth method (0x%02x)", reply[1])
	}

	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("socks5: invalid target %q", addr)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return fmt.Errorf("socks5: invalid target port %q", portStr)
	}

	// CONNECT 请求：VER=5, CMD=1, RSV=0, ATYP, ADDR, PORT
	req := []byte{0x05, 0x01, 0x00}
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			req = append(req, 0x01)
			req = append(req, ip4...)
		} else {
			req = append(req, 0x04)
			req = append(req, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return fmt.Errorf("socks5: host too long (%d bytes)", len(host))
		}
		req = append(req, 0x03, byte(len(host)))
		req = append(req, host...)
	}
	req = append(req, byte(port>>8), byte(port))
	if _, err := conn.Write(req); err != nil {
		return err
	}

	// 响应：VER, REP, RSV, ATYP, BND.ADDR, BND.PORT
	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil {
		return err
	}
	if head[0] != 0x05 {
		return fmt.Errorf("socks5: bad response version %d", head[0])
	}
	if head[1] != 0x00 {
		return fmt.Errorf("socks5 connect failed: code %d", head[1])
	}
	var addrLen int
	switch head[3] {
	case 0x01: // IPv4
		addrLen = 4
	case 0x04: // IPv6
		addrLen = 16
	case 0x03: // 域名
		lb := make([]byte, 1)
		if _, err := io.ReadFull(conn, lb); err != nil {
			return err
		}
		addrLen = int(lb[0])
	default:
		return fmt.Errorf("socks5: unexpected atyp %d", head[3])
	}
	bnd := make([]byte, addrLen+2)
	if _, err := io.ReadFull(conn, bnd); err != nil {
		return err
	}
	return nil
}

// socks5UserPassAuth 完成 RFC1929 用户名/密码认证。
func socks5UserPassAuth(conn net.Conn, user, pass string) error {
	if user == "" {
		return fmt.Errorf("socks5: user/pass auth requires credentials")
	}
	// RFC1929 用户名/密码长度各限制 255 字节（与 writeSocks5UserPass 校验一致）。
	if len(user) > 255 || len(pass) > 255 {
		return fmt.Errorf("socks5 credentials too long")
	}
	buf := []byte{0x01, byte(len(user))}
	buf = append(buf, user...)
	buf = append(buf, byte(len(pass)))
	buf = append(buf, pass...)
	if _, err := conn.Write(buf); err != nil {
		return err
	}
	resp := make([]byte, 2)
	if _, err := io.ReadFull(conn, resp); err != nil {
		return err
	}
	if resp[0] != 0x01 || resp[1] != 0x00 {
		return fmt.Errorf("socks5 auth failed (status %d)", resp[1])
	}
	return nil
}
