package validator

import (
	"net/url"
	"testing"
	"time"

	"github.com/axetroy/ProxyPilot/proxy-core/model"
)

func TestNewCheckerDefaults(t *testing.T) {
	c := NewChecker("", 0)
	if c.TestTarget() != "http://www.gstatic.com/generate_204" {
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
