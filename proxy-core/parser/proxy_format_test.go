package parser

import (
	"encoding/base64"
	"testing"

	"github.com/axetroy/ProxyPilot/proxy-core/model"
)

func mustB64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

func TestDetectSubscriptionFormat(t *testing.T) {
	cases := []struct {
		name string
		body string
		want model.SubscriptionFormat
	}{
		{"empty", "", model.SubFormatUnknown},
		{"raw list", "http://1.2.3.4:8080\nsocks5://5.6.7.8:1080\n", model.SubFormatRawList},
		{"host port list", "1.2.3.4:8080\n5.6.7.8:1080\n", model.SubFormatRawList},
		{
			"base64 wrapping raw list",
			mustB64("http://1.2.3.4:8080\nsocks5://5.6.7.8:1080\n"),
			model.SubFormatBase64,
		},
		{
			"base64 wrapping clash yaml",
			mustB64("proxies:\n  - name: a\n    type: ss\n    server: 1.2.3.4\n    port: 8080\n"),
			model.SubFormatBase64,
		},
		{
			"clash yaml",
			"proxies:\n  - name: a\n    type: ss\n    server: 1.2.3.4\n    port: 8080\n",
			model.SubFormatClash,
		},
		{
			"v2ray array",
			`[{"v":"2","ps":"x","add":"1.2.3.4","port":"8080","id":"uuid","aid":"0","type":"vmess"}]`,
			model.SubFormatV2Ray,
		},
		{
			"v2ray object outbounds",
			`{"outbounds":[{"protocol":"vmess","settings":{}}]}`,
			model.SubFormatV2Ray,
		},
		{
			"singbox object",
			`{"outbounds":[{"type":"shadowsocks","server":"1.2.3.4","server_port":8080,"tag":"x"}]}`,
			model.SubFormatSingBox,
		},
		{"ssr", "ssr://abc123\nssr://def456\n", model.SubFormatSSR},
		{"ss", "ss://abc123\n", model.SubFormatSS},
		{"unknown", "hello world this is not a subscription", model.SubFormatUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DetectSubscriptionFormat([]byte(c.body)); got != c.want {
				t.Fatalf("DetectSubscriptionFormat = %q, want %q", got, c.want)
			}
		})
	}
}

func TestSubscriptionFormatSupported(t *testing.T) {
	if !model.SubFormatBase64.Supported() {
		t.Errorf("base64 should be supported")
	}
	if !model.SubFormatRawList.Supported() {
		t.Errorf("raw list should be supported")
	}
	for _, f := range []model.SubscriptionFormat{
		model.SubFormatClash, model.SubFormatV2Ray, model.SubFormatSingBox,
		model.SubFormatSSR, model.SubFormatSS, model.SubFormatUnknown,
	} {
		if f.Supported() {
			t.Errorf("%q should not be supported yet", f)
		}
	}
}
