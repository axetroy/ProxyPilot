package model

import "testing"

func TestProxyNodeKey(t *testing.T) {
	cases := []struct {
		name string
		node *ProxyNode
		want string
	}{
		{"http", &ProxyNode{Host: "1.2.3.4", Port: 8080, Protocol: ProtocolHTTP}, "1.2.3.4:8080:http"},
		{"https", &ProxyNode{Host: "1.2.3.4", Port: 443, Protocol: ProtocolHTTPS}, "1.2.3.4:443:https"},
		{"socks5", &ProxyNode{Host: "1.2.3.4", Port: 1080, Protocol: ProtocolSOCKS5}, "1.2.3.4:1080:socks5"},
		{"ipv6", &ProxyNode{Host: "2001:db8::1", Port: 80, Protocol: ProtocolHTTP}, "2001:db8::1:80:http"},
	}
	for _, c := range cases {
		if got := c.node.Key(); got != c.want {
			t.Errorf("%s: Key() = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestStatusConstants(t *testing.T) {
	if string(StatusNew) != "new" || string(StatusChecking) != "checking" ||
		string(StatusAlive) != "alive" || string(StatusDead) != "dead" {
		t.Fatal("unexpected status constant values")
	}
}

func TestProtocolConstants(t *testing.T) {
	if string(ProtocolHTTP) != "http" || string(ProtocolHTTPS) != "https" ||
		string(ProtocolSOCKS5) != "socks5" {
		t.Fatal("unexpected protocol constant values")
	}
}
