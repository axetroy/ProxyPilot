package parser

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/axetroy/ProxyPilot/proxy-core/model"
)

func TestParseBareHostPort(t *testing.T) {
	line := "1.2.3.4:8080"
	n := ParseLine(line)
	if n == nil {
		t.Fatal("expected a node")
	}
	if n.Host != "1.2.3.4" || n.Port != 8080 || n.Protocol != model.ProtocolHTTP {
		t.Fatalf("unexpected node: %+v", n)
	}
}

func TestParseURLWithAuth(t *testing.T) {
	line := "socks5://user:pass@5.6.7.8:1080"
	n := ParseLine(line)
	if n == nil {
		t.Fatal("expected a node")
	}
	if n.Protocol != model.ProtocolSOCKS5 || n.Username != "user" || n.Password != "pass" {
		t.Fatalf("unexpected node: %+v", n)
	}
}

func TestParseHTTPS(t *testing.T) {
	line := "https://9.9.9.9:443"
	n := ParseLine(line)
	if n == nil {
		t.Fatal("expected a node")
	}
	if n.Protocol != model.ProtocolHTTPS {
		t.Fatalf("expected https node, got %+v", n)
	}
}

func TestParseInvalidLines(t *testing.T) {
	for _, line := range []string{"", "   ", "not-a-proxy", "http://"} {
		if n := ParseLine(line); n != nil {
			t.Fatalf("expected nil for %q, got %+v", line, n)
		}
	}
}

func TestParseBase64List(t *testing.T) {
	raw := "1.1.1.1:80\n2.2.2.2:443"
	encoded := base64.StdEncoding.EncodeToString([]byte(raw))
	nodes := ParseProxyList(encoded)
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}
	if nodes[0].Host != "1.1.1.1" {
		t.Fatalf("unexpected first node: %+v", nodes[0])
	}
}

func TestParseMixedContent(t *testing.T) {
	content := strings.Join([]string{
		"3.3.3.3:3128",
		"socks5://4.4.4.4:1080",
		"",
	}, "\n")
	nodes := ParseProxyList(content)
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}
}

func TestParseSocksVariants(t *testing.T) {
	for _, scheme := range []string{"socks", "socks5", "socks5h"} {
		n := ParseLine(scheme + "://1.2.3.4:1080")
		if n == nil {
			t.Fatalf("expected node for %s://", scheme)
		}
		if n.Protocol != model.ProtocolSOCKS5 {
			t.Fatalf("%s:// protocol = %q, want socks5", scheme, n.Protocol)
		}
	}
}

func TestParseTLSScheme(t *testing.T) {
	n := ParseLine("tls://1.2.3.4:443")
	if n == nil {
		t.Fatal("expected node for tls://")
	}
	if n.Protocol != model.ProtocolHTTPS {
		t.Fatalf("protocol = %q, want https", n.Protocol)
	}
}

func TestParseSchemeCaseInsensitive(t *testing.T) {
	n := ParseLine("SOCKS5://1.2.3.4:1080")
	if n == nil || n.Protocol != model.ProtocolSOCKS5 {
		t.Fatalf("expected socks5 node, got %+v", n)
	}
	n = ParseLine("HTTP://1.2.3.4:8080")
	if n == nil || n.Protocol != model.ProtocolHTTP {
		t.Fatalf("expected http node, got %+v", n)
	}
}

func TestParseUserOnly(t *testing.T) {
	n := ParseLine("socks5://user@1.2.3.4:1080")
	if n == nil {
		t.Fatal("expected node")
	}
	if n.Username != "user" || n.Password != "" {
		t.Fatalf("expected user-only auth, got %+v", n)
	}
}

func TestParseIPv6(t *testing.T) {
	n := ParseLine("http://[2001:db8::1]:8080")
	if n == nil {
		t.Fatal("expected node for ipv6")
	}
	if n.Host != "2001:db8::1" || n.Port != 8080 {
		t.Fatalf("unexpected ipv6 node: %+v", n)
	}
}

func TestParsePortBoundaries(t *testing.T) {
	for _, line := range []string{
		"1.2.3.4:0",
		"1.2.3.4:65536",
		"1.2.3.4:-1",
		"1.2.3.4:abc",
		"http://1.2.3.4:0",
		"http://1.2.3.4:70000",
	} {
		if n := ParseLine(line); n != nil {
			t.Fatalf("expected nil for %q, got %+v", line, n)
		}
	}
}

func TestParseLineTrimsWhitespace(t *testing.T) {
	n := ParseLine("  1.2.3.4:8080  ")
	if n == nil {
		t.Fatal("expected node")
	}
	if n.Host != "1.2.3.4" {
		t.Fatalf("host = %q", n.Host)
	}
}

func TestParseLineSetsStatusNew(t *testing.T) {
	n := ParseLine("1.2.3.4:8080")
	if n == nil {
		t.Fatal("expected node")
	}
	if n.Status != model.StatusNew {
		t.Fatalf("status = %q, want new", n.Status)
	}
	if n.CreatedAt.IsZero() {
		t.Fatal("expected CreatedAt set")
	}
}

func TestParseUnknownScheme(t *testing.T) {
	for _, line := range []string{
		"ftp://1.2.3.4:21",
		"ss://1.2.3.4:8388",
		"vmess://1.2.3.4:443",
	} {
		if n := ParseLine(line); n != nil {
			t.Fatalf("expected nil for %q, got %+v", line, n)
		}
	}
}

func TestParseBase64WithWhitespace(t *testing.T) {
	raw := "1.1.1.1:80\n2.2.2.2:443"
	encoded := base64.StdEncoding.EncodeToString([]byte(raw))
	// 在 base64 中间插入换行，验证 stripWhitespace 逻辑
	withNewline := encoded[:10] + "\n" + encoded[10:]
	nodes := ParseProxyList(withNewline)
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes from whitespaced base64, got %d", len(nodes))
	}
}

func TestParsePlainTextNotBase64(t *testing.T) {
	// 长度足够但不是合法 base64 的明文，应按文本解析
	content := "not base64 at all!!\n1.2.3.4:8080"
	nodes := ParseProxyList(content)
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if nodes[0].Host != "1.2.3.4" {
		t.Fatalf("host = %q", nodes[0].Host)
	}
}

func TestParseSubscriptionBody(t *testing.T) {
	body := []byte("1.1.1.1:80\nsocks5://2.2.2.2:1080")
	nodes := ParseSubscriptionBody(body)
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}
}

func TestParseCRLF(t *testing.T) {
	content := "1.1.1.1:80\r\n2.2.2.2:443\r\n"
	nodes := ParseProxyList(content)
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes from CRLF content, got %d", len(nodes))
	}
}

func TestProtocolFromScheme(t *testing.T) {
	cases := []struct {
		scheme string
		want   model.ProxyProtocol
	}{
		{"http", model.ProtocolHTTP},
		{"HTTP", model.ProtocolHTTP},
		{"https", model.ProtocolHTTPS},
		{"tls", model.ProtocolHTTPS},
		{"socks5", model.ProtocolSOCKS5},
		{"socks", model.ProtocolSOCKS5},
		{"socks5h", model.ProtocolSOCKS5},
		{"ftp", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := protocolFromScheme(c.scheme); got != c.want {
			t.Errorf("protocolFromScheme(%q) = %q, want %q", c.scheme, got, c.want)
		}
	}
}