package parser

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/axetroy/ProxyPilot/proxy-core/model"
)

func TestFormatProxyLine(t *testing.T) {
	cases := []struct {
		name string
		node *model.ProxyNode
		want string
	}{
		{
			name: "socks5 with auth",
			node: &model.ProxyNode{Host: "5.6.7.8", Port: 1080, Protocol: model.ProtocolSOCKS5, Username: "user", Password: "pass"},
			want: "socks5://user:pass@5.6.7.8:1080",
		},
		{
			name: "http without auth",
			node: &model.ProxyNode{Host: "1.2.3.4", Port: 8080, Protocol: model.ProtocolHTTP},
			want: "http://1.2.3.4:8080",
		},
		{
			name: "https",
			node: &model.ProxyNode{Host: "9.9.9.9", Port: 443, Protocol: model.ProtocolHTTPS},
			want: "https://9.9.9.9:443",
		},
		{
			name: "ipv6",
			node: &model.ProxyNode{Host: "::1", Port: 1080, Protocol: model.ProtocolSOCKS5},
			want: "socks5://[::1]:1080",
		},
		{
			name: "special chars encoded",
			node: &model.ProxyNode{Host: "1.2.3.4", Port: 8080, Protocol: model.ProtocolHTTP, Username: "a@b", Password: "p:w"},
			want: "http://a%40b:p%3Aw@1.2.3.4:8080",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatProxyLine(tc.node); got != tc.want {
				t.Fatalf("FormatProxyLine = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFormatParseRoundTrip(t *testing.T) {
	nodes := []*model.ProxyNode{
		{Host: "1.2.3.4", Port: 8080, Protocol: model.ProtocolHTTP},
		{Host: "5.6.7.8", Port: 1080, Protocol: model.ProtocolSOCKS5, Username: "user", Password: "pass"},
		{Host: "::1", Port: 443, Protocol: model.ProtocolHTTPS},
		{Host: "10.0.0.1", Port: 3128, Protocol: model.ProtocolHTTP, Username: "a@b:c", Password: "p:w/x"},
	}
	for _, n := range nodes {
		line := FormatProxyLine(n)
		parsed := ParseLine(line)
		if parsed == nil {
			t.Fatalf("ParseLine(%q) returned nil", line)
		}
		if parsed.Protocol != n.Protocol || parsed.Host != n.Host || parsed.Port != n.Port ||
			parsed.Username != n.Username || parsed.Password != n.Password {
			t.Fatalf("round trip mismatch: %q -> %+v (want %+v)", line, parsed, n)
		}
	}
}

func TestEncodeSubscriptionPlain(t *testing.T) {
	nodes := []*model.ProxyNode{
		{Host: "1.1.1.1", Port: 80, Protocol: model.ProtocolHTTP},
		{Host: "2.2.2.2", Port: 1080, Protocol: model.ProtocolSOCKS5, Username: "u", Password: "p"},
	}
	got := EncodeSubscription(nodes, false)
	want := "http://1.1.1.1:80\nsocks5://u:p@2.2.2.2:1080\n"
	if got != want {
		t.Fatalf("EncodeSubscription(plain) = %q, want %q", got, want)
	}
}

func TestEncodeSubscriptionBase64RoundTrip(t *testing.T) {
	nodes := []*model.ProxyNode{
		{Host: "1.1.1.1", Port: 80, Protocol: model.ProtocolHTTP},
		{Host: "2.2.2.2", Port: 1080, Protocol: model.ProtocolSOCKS5, Username: "u", Password: "p"},
	}
	encoded := EncodeSubscription(nodes, true)
	if strings.ContainsAny(encoded, " \n\r\t") {
		t.Fatalf("base64 output should not contain whitespace: %q", encoded)
	}
	// 与解析路径互逆：base64 → 明文 → 节点列表
	plain, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}
	reparsed := ParseProxyList(string(plain))
	if len(reparsed) != len(nodes) {
		t.Fatalf("round trip nodes = %d, want %d", len(reparsed), len(nodes))
	}
	for i, n := range reparsed {
		if n.Host != nodes[i].Host || n.Port != nodes[i].Port || n.Username != nodes[i].Username || n.Password != nodes[i].Password {
			t.Fatalf("round trip node[%d] mismatch: %+v vs %+v", i, n, nodes[i])
		}
	}
}

func TestEncodeSubscriptionEmpty(t *testing.T) {
	if got := EncodeSubscription(nil, false); got != "" {
		t.Fatalf("empty plain = %q, want \"\"", got)
	}
	if got := EncodeSubscription(nil, true); got != "" {
		t.Fatalf("empty base64 = %q, want \"\"", got)
	}
}
