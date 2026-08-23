package parser

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/axetroy/ProxyPilot/proxy-core/model"
)

// DetectSubscriptionFormat 嗅探订阅内容的格式（不做完整解析，仅用于识别与展示）。
// 识别顺序：base64 包装 → ssr:// / ss:// → JSON(V2Ray/Sing-box) → Clash(YAML) → 明文行式 → 未知。
func DetectSubscriptionFormat(body []byte) model.SubscriptionFormat {
	raw := strings.TrimSpace(string(body))
	if raw == "" {
		return model.SubFormatUnknown
	}

	// 1) base64 外层包装：解码后为可见文本即判为 base64（交付格式即 base64，内部按现有解析器解包）。
	if decoded, ok := tryDecodeBase64(raw); ok {
		_ = decoded
		return model.SubFormatBase64
	}

	// 2) ssr:// / ss:// 明文列表。
	if strings.Contains(raw, "ssr://") {
		return model.SubFormatSSR
	}
	if strings.Contains(raw, "ss://") {
		return model.SubFormatSS
	}

	// 3) JSON：V2Ray / Sing-box。
	if strings.HasPrefix(raw, "{") || strings.HasPrefix(raw, "[") {
		return detectJSONFormat(raw)
	}

	// 4) YAML：Clash。
	if isYAMLClash(raw) {
		return model.SubFormatClash
	}

	// 5) 明文行式：protocol://... 或 host:port。
	if strings.Contains(raw, "://") || looksLikeHostPortList(raw) {
		return model.SubFormatRawList
	}

	return model.SubFormatUnknown
}

// detectJSONFormat 在已确认为 JSON 的前提下区分 V2Ray 与 Sing-box。
func detectJSONFormat(raw string) model.SubscriptionFormat {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &obj); err == nil {
		if _, ok := obj["outbounds"]; ok {
			// Sing-box 的 outbound 用 server_port；V2Ray 用 port + protocol。
			if strings.Contains(raw, `"server_port"`) {
				return model.SubFormatSingBox
			}
			return model.SubFormatV2Ray
		}
	}
	var arr []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &arr); err == nil && len(arr) > 0 {
		// V2RayN 标准订阅：数组，元素含 ps / add 等字段。
		if _, ok := arr[0]["ps"]; ok {
			return model.SubFormatV2Ray
		}
		if _, ok := arr[0]["add"]; ok {
			return model.SubFormatV2Ray
		}
	}
	return model.SubFormatUnknown
}

// isYAMLClash 粗略判断是否为 Clash 风格 YAML：含 proxies: 且带有节点字段。
func isYAMLClash(raw string) bool {
	if strings.HasPrefix(raw, "{") || strings.HasPrefix(raw, "[") {
		return false
	}
	if !strings.Contains(raw, "proxies:") {
		return false
	}
	return strings.Contains(raw, "type:") ||
		strings.Contains(raw, "server:") ||
		strings.Contains(raw, "server-port:")
}

// looksLikeHostPortList 判断文本是否像 "host:port" 逐行列表。
func looksLikeHostPortList(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// 含 ':' 但不是协议行（无 ://）即视为 host:port。
		return strings.Contains(line, ":") && !strings.Contains(line, "://")
	}
	return false
}

// tryDecodeBase64 尝试按标准 base64 解码（忽略空白）；解码后须为可见文本才视为 base64，
// 避免把任意恰好可解的 base64 误判为订阅（如普通英文句子）。
func tryDecodeBase64(s string) (string, bool) {
	trimmed := stripWhitespace(s)
	if len(trimmed) < 16 {
		return "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		return "", false
	}
	text := string(decoded)
	if !isMostlyPrintable(text) {
		return "", false
	}
	return text, true
}

// isMostlyPrintable 判断文本是否基本由可见 ASCII / 常见空白组成（排除二进制乱码）。
func isMostlyPrintable(s string) bool {
	if !utf8.ValidString(s) || len(s) == 0 {
		return false
	}
	printable := 0
	for _, r := range s {
		if r == '\n' || r == '\r' || r == '\t' || (r >= 0x20 && r <= 0x7e) {
			printable++
		}
	}
	return float64(printable)/float64(len([]rune(s))) > 0.9
}
