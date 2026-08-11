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
var Version = "0.1.4"

// 持久化配置项的 key。
// APIBind / DBPath / SessionToken 属于启动期固定配置，不支持通过界面修改。
const (
	KeyHTTPBind      = "http_proxy_bind"
	KeySOCKSBind     = "socks5_proxy_bind"
	KeyCheckTarget   = "check_target"
	KeyCheckTimeout  = "check_timeout"
	KeyCheckConcurr  = "check_concurrency"
	KeyRefreshPeriod = "refresh_interval"
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
		{Key: KeyHTTPBind, Default: "127.0.0.1:7892", Desc: "HTTP 代理监听地址（被占用自动顺延）", Validate: validateHostPort},
		{Key: KeySOCKSBind, Default: "127.0.0.1:7893", Desc: "SOCKS5 代理监听地址（被占用自动顺延）", Validate: validateHostPort},
		{Key: KeyCheckTarget, Default: "https://www.cloudflare.com/cdn-cgi/trace", Desc: "节点检测目标 URL", Validate: validateURL},
		{Key: KeyCheckTimeout, Default: "10s", Desc: "单节点检测超时（如 5s、500ms）", Validate: validateDuration},
		{Key: KeyCheckConcurr, Default: "32", Desc: "并发检测节点数", Validate: validatePositiveInt},
		{Key: KeyRefreshPeriod, Default: "15m", Desc: "代理池自动检测周期（如 30m、1h）", Validate: validateDuration},
	}
}

func validateHostPort(v string) error {
	if _, _, err := net.SplitHostPort(v); err != nil {
		return fmt.Errorf("格式应为 host:port，如 127.0.0.1:7892")
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

type Config struct {
	APIBind          string
	DBPath           string
	HTTPProxyBind    string
	SOCKSProxyBind   string
	SessionToken     string
	CheckTarget      string
	CheckTimeout     time.Duration
	CheckConcurrency int
	RefreshInterval  time.Duration
}

func New() *Config {
	c := &Config{
		APIBind:          "127.0.0.1:17890",
		DBPath:           "proxypilot.db",
		HTTPProxyBind:    "127.0.0.1:7892",
		SOCKSProxyBind:   "127.0.0.1:7893",
		CheckTarget:      "https://www.cloudflare.com/cdn-cgi/trace",
		CheckTimeout:     10 * time.Second,
		CheckConcurrency: 32,
		RefreshInterval:  15 * time.Minute,
	}
	c.SessionToken = generatedSessionToken()
	c.ApplyEnv()
	return c
}

func (c *Config) ApplyEnv() {
	if v := os.Getenv("PROXYPILOT_API_BIND"); v != "" {
		c.APIBind = v
	}
	if v := os.Getenv("PROXYPILOT_DB_PATH"); v != "" {
		c.DBPath = v
	}
	if v := os.Getenv("PROXYPILOT_HTTP_BIND"); v != "" {
		c.HTTPProxyBind = v
	}
	if v := os.Getenv("PROXYPILOT_SOCKS5_BIND"); v != "" {
		c.SOCKSProxyBind = v
	}
	if v := os.Getenv("PROXYPILOT_TOKEN"); v != "" {
		c.SessionToken = v
	}
	if v := os.Getenv("PROXYPILOT_CHECK_TARGET"); v != "" {
		c.CheckTarget = v
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
}

func generatedSessionToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "fallback-session-token"
	}
	return hex.EncodeToString(b)
}

// settingKey 返回持久化配置项 key 对应的 Config 字段指针，
// 以及该 key 是否属于可配置项。
func (c *Config) settingKey(key string) (func(string), bool) {
	switch key {
	case KeyHTTPBind:
		return func(v string) { c.HTTPProxyBind = v }, true
	case KeySOCKSBind:
		return func(v string) { c.SOCKSProxyBind = v }, true
	case KeyCheckTarget:
		return func(v string) { c.CheckTarget = v }, true
	case KeyCheckTimeout:
		return func(v string) { if d, err := time.ParseDuration(v); err == nil { c.CheckTimeout = d } }, true
	case KeyCheckConcurr:
		return func(v string) { if n, err := strconv.Atoi(v); err == nil && n > 0 { c.CheckConcurrency = n } }, true
	case KeyRefreshPeriod:
		return func(v string) { if d, err := time.ParseDuration(v); err == nil { c.RefreshInterval = d } }, true
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
}

// SettingValue 返回指定 key 的当前值（作为字符串）。
func (c *Config) SettingValue(key string) (string, bool) {
	switch key {
	case KeyHTTPBind:
		return c.HTTPProxyBind, true
	case KeySOCKSBind:
		return c.SOCKSProxyBind, true
	case KeyCheckTarget:
		return c.CheckTarget, true
	case KeyCheckTimeout:
		return c.CheckTimeout.String(), true
	case KeyCheckConcurr:
		return strconv.Itoa(c.CheckConcurrency), true
	case KeyRefreshPeriod:
		return c.RefreshInterval.String(), true
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
