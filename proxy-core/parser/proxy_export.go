package parser

import (
	"encoding/base64"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/axetroy/ProxyPilot/proxy-core/model"
)

// FormatProxyLine 将节点序列化为订阅行（与 ParseLine 互逆）：
//   protocol://user:pass@host:port
//
// 用户名/密码经 URL 编码，IPv6 地址自动加 []，保证往返解析一致。
func FormatProxyLine(n *model.ProxyNode) string {
	var b strings.Builder
	b.WriteString(string(n.Protocol))
	b.WriteString("://")
	if n.Username != "" {
		u := url.UserPassword(n.Username, n.Password)
		b.WriteString(u.String())
		b.WriteString("@")
	}
	b.WriteString(net.JoinHostPort(n.Host, strconv.Itoa(n.Port)))
	return b.String()
}

// FormatProxyList 将节点列表逐行序列化，行与行之间用换行分隔。
func FormatProxyList(nodes []*model.ProxyNode) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, FormatProxyLine(n))
	}
	return out
}

// EncodeSubscription 将节点列表编码为订阅文本。
// base64Encoded=true 时输出 base64（常见订阅格式），否则输出明文（每行一个 URL）。
// base64 编码与 ParseProxyList 的 shouldDecodeBase64/StdEncoding 解码路径对应。
func EncodeSubscription(nodes []*model.ProxyNode, base64Encoded bool) string {
	lines := FormatProxyList(nodes)
	text := strings.Join(lines, "\n")
	if len(lines) > 0 {
		text += "\n"
	}
	if !base64Encoded {
		return text
	}
	if text == "" {
		return ""
	}
	return base64.StdEncoding.EncodeToString([]byte(text))
}
