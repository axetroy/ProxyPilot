package validator

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/axetroy/ProxyPilot/proxy-core/model"
	"golang.org/x/net/proxy"
)

// Checker validates a single proxy node by connecting to a target through it.
type Checker struct {
	target  string
	timeout time.Duration
}

func NewChecker(target string, timeout time.Duration) *Checker {
	if target == "" {
		target = "http://www.gstatic.com/generate_204"
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Checker{target: target, timeout: timeout}
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
	return model.CheckResult{OK: true, Latency: latency}, nil
}

func proxyURLWith(node *model.ProxyNode) *url.URL {
	u := &url.URL{Scheme: "http"}
	u.Host = net.JoinHostPort(node.Host, strconv.Itoa(node.Port))
	if node.Username != "" {
		u.User = url.UserPassword(node.Username, node.Password)
	}
	return u
}

func httpProxyURL(node *model.ProxyNode) (*url.URL, error) {
	u := proxyURLWith(node)
	if node.Protocol == model.ProtocolSOCKS5 {
		u.Scheme = "socks5"
	}
	return u, nil
}

// ConnectTCP establishes a TCP tunnel to addr through the node (CONNECT for http
// proxies, SOCKS5 handshake for socks5). Returns the tunneled connection.
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
	case model.ProtocolHTTP, model.ProtocolHTTPS:
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
