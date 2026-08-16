package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/axetroy/ProxyPilot/proxy-core/storage"
)

// Version 通过 goreleaser 的 ldflags 注入（如 -X ...config.Version={{.Version}}），
// 本地开发时使用默认值。
var Version = "0.1.15"

// 持久化配置项的 key。
// APIBind / DBPath / SessionToken 属于启动期固定配置，不支持通过界面修改。
const (
	KeyProxyPort         = "proxy_port"
	KeyCheckTarget       = "check_target"
	KeyCheckAnonymityTgt = "check_anonymity_target"
	KeyCheckTimeout      = "check_timeout"
	KeyCheckConcurr      = "check_concurrency"
	KeyRefreshPeriod     = "refresh_interval"
	// 订阅导出相关：enabled/listen 通过前端设置，token 由专门的订阅接口管理
	// （不进 Settings() 列表，避免与通用设置表单混在一起）。
	KeySubEnabled = "subscription_enabled"
	KeySubListen  = "subscription_listen"
	KeySubHost    = "subscription_host"
	KeySubToken   = "subscription_token"
)

// SettingDef 描述一个可在前端配置的项。
type SettingDef struct {
	Key     string `json:"key"`
	Default string `json:"default"`
	Desc    string `json:"desc"`
	// Validate 校验用户输入，返回错误信息（nil 表示合法）。
	Validate func(v string) error `json:"-"`
}

// Settings 返回所有可在前端配置的项定义。
func Settings() []SettingDef {
	return []SettingDef{
		{Key: KeyProxyPort, Default: "7892", Desc: "代理监听端口（HTTP 与 SOCKS5 共用，仅本机，被占用自动顺延）", Validate: validatePort},
		{Key: KeyCheckTarget, Default: "https://www.apple.com/library/test/success.html", Desc: "节点检测目标 URL", Validate: validateURL},
		{Key: KeyCheckAnonymityTgt, Default: "https://httpbin.org/anything", Desc: "匿名性检测回显端点（需返回 origin 与 headers 字段）", Validate: validateURL},
		{Key: KeyCheckTimeout, Default: "10s", Desc: "单节点检测超时（如 5s、500ms）", Validate: validateDuration},
		{Key: KeyCheckConcurr, Default: "32", Desc: "并发检测节点数", Validate: validatePositiveInt},
		{Key: KeyRefreshPeriod, Default: "15m", Desc: "代理池自动检测周期（如 30m、1h）", Validate: validateDuration},
		{Key: KeySubEnabled, Default: "1", Desc: "订阅导出开关（0 关闭 / 1 开启）", Validate: validateBool},
		{Key: KeySubListen, Default: "127.0.0.1:17891", Desc: "订阅服务监听地址（如需局域网设备订阅改为 0.0.0.0:17891）", Validate: validateHostPort},
		{Key: KeySubHost, Default: "", Desc: "对外展示的订阅 IP（监听为 0.0.0.0 时用于生成订阅 URL）", Validate: validateIPOrEmpty},
	}
}

// validatePort 校验纯端口号（1-65535）。
func validatePort(v string) error {
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("端口必须是 1-65535 之间的整数")
	}
	return nil
}

func validateURL(v string) error {
	if !strings.HasPrefix(v, "http://") && !strings.HasPrefix(v, "https://") {
		return fmt.Errorf("必须是 http:// 或 https:// 开头的 URL")
	}
	return nil
}

func validateDuration(v string) error {
	if _, err := time.ParseDuration(v); err != nil {
		return fmt.Errorf("时长格式不合法，如 5s、10s、15m")
	}
	return nil
}

func validatePositiveInt(v string) error {
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fmt.Errorf("必须是正整数")
	}
	return nil
}

// validateBool 校验 0/1 布尔字符串。
func validateBool(v string) error {
	if v != "0" && v != "1" {
		return fmt.Errorf("必须是 0 或 1")
	}
	return nil
}

// validateHostPort 校验 host:port 监听地址。
func validateHostPort(v string) error {
	_, port, err := net.SplitHostPort(v)
	if err != nil {
		return fmt.Errorf("必须是 host:port 格式，如 127.0.0.1:17891")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("端口必须是 1-65535 之间的整数")
	}
	return nil
}

// validateIPOrEmpty 校验 IP 地址（空串表示未选择，允许）。
func validateIPOrEmpty(v string) error {
	if v == "" {
		return nil
	}
	if net.ParseIP(v) == nil {
		return fmt.Errorf("必须是合法的 IP 地址")
	}
	return nil
}

type Config struct {
	APIBind              string
	DBPath               string
	ProxyHost            string // 代理监听 host，固定仅本机，不允许修改
	ProxyPort            int    // 代理监听端口（HTTP 与 SOCKS5 共用）
	SessionToken         string
	CheckTarget          string
	CheckAnonymityTarget string
	CheckTimeout         time.Duration
	CheckConcurrency     int
	RefreshInterval      time.Duration
	SubEnabled           bool   // 订阅导出开关
	SubListen            string // 订阅服务监听地址（默认仅本机，对外暴露需显式配置 0.0.0.0）
	SubHost              string // 对外展示的订阅 IP（监听为通配地址时用于生成订阅 URL，空则回退 127.0.0.1）
	SubToken             string // 订阅密钥（独立于 SessionToken，随订阅 URL 提供给外部客户端）
}

func New() *Config {
	c := &Config{
		APIBind:              "127.0.0.1:17890",
		DBPath:               "proxypilot.db",
		ProxyHost:            "127.0.0.1",
		ProxyPort:            7892,
		CheckTarget:          "https://www.apple.com/library/test/success.html",
		CheckAnonymityTarget: "https://httpbin.org/anything",
		CheckTimeout:         10 * time.Second,
		CheckConcurrency:     32,
		RefreshInterval:      15 * time.Minute,
		SubEnabled:           true,
		SubListen:            "127.0.0.1:17891",
		SubToken:             generatedSessionToken(),
	}
	c.SessionToken = generatedSessionToken()
	c.ApplyEnv()
	return c
}

// SubscriptionURL 返回订阅 URL（供 UI 展示与复制）。
// 用户配置了具体监听 IP 时原样拼接；监听为 0.0.0.0/:: 等通配地址时，
// 拼接用户在前端下拉选中的局域网 IP（SubHost），未选择时回退 127.0.0.1。
func (c *Config) SubscriptionURL() string {
	host, port, err := net.SplitHostPort(c.SubListen)
	if err != nil {
		host, port = "127.0.0.1", "17891"
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = c.SubHost
		if host == "" {
			host = "127.0.0.1"
		}
	}
	return "http://" + net.JoinHostPort(host, port) + "/sub/" + c.SubToken
}

// LANIPs 返回本机所有非回环的 IPv4 局域网地址（供前端下拉选择）。
func LANIPs() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var ips []string
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
			ips = append(ips, ipnet.IP.String())
		}
	}
	return ips
}

// ProxyAddr 返回代理网关的完整监听地址 host:port。
func (c *Config) ProxyAddr() string {
	return net.JoinHostPort(c.ProxyHost, strconv.Itoa(c.ProxyPort))
}

func (c *Config) ApplyEnv() {
	if v := os.Getenv("PROXYPILOT_API_BIND"); v != "" {
		c.APIBind = v
	}
	if v := os.Getenv("PROXYPILOT_DB_PATH"); v != "" {
		c.DBPath = v
	}
	if v := os.Getenv("PROXYPILOT_PROXY_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 65535 {
			c.ProxyPort = n
		}
	}
	if v := os.Getenv("PROXYPILOT_TOKEN"); v != "" {
		c.SessionToken = v
	}
	if v := os.Getenv("PROXYPILOT_CHECK_TARGET"); v != "" {
		c.CheckTarget = v
	}
	if v := os.Getenv("PROXYPILOT_CHECK_ANONYMITY_TARGET"); v != "" {
		c.CheckAnonymityTarget = v
	}
	if v := os.Getenv("PROXYPILOT_CHECK_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.CheckTimeout = d
		}
	}
	if v := os.Getenv("PROXYPILOT_CHECK_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.CheckConcurrency = n
		}
	}
	if v := os.Getenv("PROXYPILOT_REFRESH_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.RefreshInterval = d
		}
	}
	if v := os.Getenv("PROXYPILOT_SUB_ENABLED"); v != "" {
		c.SubEnabled = v == "1"
	}
	if v := os.Getenv("PROXYPILOT_SUB_LISTEN"); v != "" {
		c.SubListen = v
	}
	if v := os.Getenv("PROXYPILOT_SUB_HOST"); v != "" {
		c.SubHost = v
	}
	if v := os.Getenv("PROXYPILOT_SUB_TOKEN"); v != "" {
		c.SubToken = v
	}
}

func generatedSessionToken() string {
	return NewToken()
}

// NewToken 生成 32 位 hex 随机密钥（用于 session token 与订阅密钥重置）。
func NewToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "fallback-" + strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b)
}

// settingKey 返回持久化配置项 key 对应的 Config 字段指针，
// 以及该 key 是否属于可配置项。
func (c *Config) settingKey(key string) (func(string), bool) {
	switch key {
	case KeyProxyPort:
		return func(v string) {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 65535 {
				c.ProxyPort = n
			}
		}, true
	case KeyCheckTarget:
		return func(v string) { c.CheckTarget = v }, true
	case KeyCheckAnonymityTgt:
		return func(v string) { c.CheckAnonymityTarget = v }, true
	case KeyCheckTimeout:
		return func(v string) {
			if d, err := time.ParseDuration(v); err == nil {
				c.CheckTimeout = d
			}
		}, true
	case KeyCheckConcurr:
		return func(v string) {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				c.CheckConcurrency = n
			}
		}, true
	case KeyRefreshPeriod:
		return func(v string) {
			if d, err := time.ParseDuration(v); err == nil {
				c.RefreshInterval = d
			}
		}, true
	case KeySubEnabled:
		return func(v string) { c.SubEnabled = v == "1" }, true
	case KeySubListen:
		return func(v string) { c.SubListen = v }, true
	case KeySubHost:
		return func(v string) { c.SubHost = v }, true
	default:
		return nil, false
	}
}

// LoadOverrides 从持久化存储中加载用户在前端修改过的配置，覆盖当前值。
// 调用时机：main 启动时、构造各组件之前。
func (c *Config) LoadOverrides(st *storage.Store) {
	for _, def := range Settings() {
		v, err := st.GetSetting(def.Key)
		if err != nil || v == "" {
			continue
		}
		if def.Validate != nil {
			if err := def.Validate(v); err != nil {
				continue // 跳过持久化中的非法值
			}
		}
		if apply, ok := c.settingKey(def.Key); ok {
			apply(v)
		}
	}
	// 订阅密钥独立加载：首次启动生成并持久化，之后保持一致。
	if v, err := st.GetSetting(KeySubToken); err == nil && v != "" {
		c.SubToken = v
	} else {
		_ = st.SetSetting(KeySubToken, c.SubToken)
	}
}

// SettingValue 返回指定 key 的当前值（作为字符串）。
func (c *Config) SettingValue(key string) (string, bool) {
	switch key {
	case KeyProxyPort:
		return strconv.Itoa(c.ProxyPort), true
	case KeyCheckTarget:
		return c.CheckTarget, true
	case KeyCheckAnonymityTgt:
		return c.CheckAnonymityTarget, true
	case KeyCheckTimeout:
		return c.CheckTimeout.String(), true
	case KeyCheckConcurr:
		return strconv.Itoa(c.CheckConcurrency), true
	case KeyRefreshPeriod:
		return c.RefreshInterval.String(), true
	case KeySubEnabled:
		if c.SubEnabled {
			return "1", true
		}
		return "0", true
	case KeySubListen:
		return c.SubListen, true
	case KeySubHost:
		return c.SubHost, true
	default:
		return "", false
	}
}

// ValidateSetting 只校验配置值是否合法，不修改 Config。
// 用于 API 批量更新前的整体校验（原子性：任一非法则全部不应用）。
func ValidateSetting(key, value string) error {
	def := settingDef(key)
	if def == nil {
		return fmt.Errorf("未知配置项: %s", key)
	}
	if def.Validate != nil {
		if err := def.Validate(value); err != nil {
			return err
		}
	}
	return nil
}

// ApplySetting 校验并应用一个配置值到 Config（不持久化）。
// 返回 (是否修改, 错误)。
func (c *Config) ApplySetting(key, value string) (bool, error) {
	if err := ValidateSetting(key, value); err != nil {
		return false, err
	}
	apply, _ := c.settingKey(key)
	if old, ok := c.SettingValue(key); ok && old == value {
		return false, nil
	}
	apply(value)
	return true, nil
}

func settingDef(key string) *SettingDef {
	for _, def := range Settings() {
		if def.Key == key {
			return &def
		}
	}
	return nil
}
