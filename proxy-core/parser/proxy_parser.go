package parser

import (
	"encoding/base64"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

"github.com/axetroy/ProxyPilot/proxy-core/model"
)

// ParseProxyList parses raw subscription text into proxy nodes.
// Supports: base64 encoded lists, plain text URL per line, host:port per line.
func ParseProxyList(content string) []*model.ProxyNode {
	text := content
	if shouldDecodeBase64(content) {
		if decoded, err := base64.StdEncoding.DecodeString(stripWhitespace(content)); err == nil {
			text = string(decoded)
		}
	}
	var out []*model.ProxyNode
	for _, line := range strings.Split(text, "\n") {
		node := ParseLine(line)
		if node != nil {
			out = append(out, node)
		}
	}
	return out
}

// ParseSubscriptionBody derives proxy nodes from an arbitrary subscription body.
func ParseSubscriptionBody(body []byte) []*model.ProxyNode {
	return ParseProxyList(string(body))
}

// ParseLine parses a single proxy URI/line.
func ParseLine(line string) *model.ProxyNode {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	if !strings.Contains(line, "://") {
		return parseBareHostPort(line)
	}
	u, err := url.Parse(line)
	if err != nil {
		return nil
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port <= 0 || port > 65535 {
		return nil
	}
	proto := protocolFromScheme(u.Scheme)
	if proto == "" {
		return nil
	}
	username := ""
	password := ""
	if u.User != nil {
		username = u.User.Username()
		if pw, ok := u.User.Password(); ok {
			password = pw
		}
	}
	return &model.ProxyNode{
		Host:      u.Hostname(),
		Port:      port,
		Protocol:  proto,
		Username:  username,
		Password:  password,
		Status:    model.StatusNew,
		CreatedAt: time.Now(),
	}
}

func parseBareHostPort(line string) *model.ProxyNode {
	host, portStr, err := net.SplitHostPort(line)
	if err != nil {
		return nil
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return nil
	}
	return &model.ProxyNode{
		Host:      host,
		Port:      port,
		Protocol:  model.ProtocolHTTP,
		Status:    model.StatusNew,
		CreatedAt: time.Now(),
	}
}

func protocolFromScheme(scheme string) model.ProxyProtocol {
	switch strings.ToLower(scheme) {
	case "http":
		return model.ProtocolHTTP
	case "https", "tls":
		return model.ProtocolHTTPS
	case "socks5", "socks", "socks5h":
		return model.ProtocolSOCKS5
	default:
		return ""
	}
}

func shouldDecodeBase64(text string) bool {
	trimmed := stripWhitespace(text)
	if len(trimmed) < 16 {
		return false
	}
	_, err := base64.StdEncoding.DecodeString(trimmed)
	return err == nil
}

func stripWhitespace(s string) string {
	replacer := strings.NewReplacer(" ", "", "\t", "", "\n", "", "\r", "")
	return replacer.Replace(s)
}