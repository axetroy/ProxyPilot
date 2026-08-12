package config

import (
	"net"
	"os"
	"testing"

	"github.com/axetroy/ProxyPilot/proxy-core/storage"
)

func TestSubDefaults(t *testing.T) {
	unsetEnv(t)
	defer restoreEnv(t)

	c := New()
	if !c.SubEnabled {
		t.Error("SubEnabled should default to true")
	}
	if c.SubListen != "127.0.0.1:17891" {
		t.Errorf("SubListen = %q, want 127.0.0.1:17891", c.SubListen)
	}
	if len(c.SubToken) != 32 {
		t.Errorf("SubToken len = %d, want 32", len(c.SubToken))
	}
	if c.SubToken == c.SessionToken {
		t.Error("SubToken should be independent of SessionToken")
	}
}

func TestSubEnvOverrides(t *testing.T) {
	unsetEnv(t)
	defer restoreEnv(t)

	_ = setEnv(t, "PROXYPILOT_SUB_ENABLED", "0")
	_ = setEnv(t, "PROXYPILOT_SUB_LISTEN", "0.0.0.0:17891")
	_ = setEnv(t, "PROXYPILOT_SUB_TOKEN", "0123456789abcdef0123456789abcdef")

	c := New()
	if c.SubEnabled {
		t.Error("SubEnabled should be false via env")
	}
	if c.SubListen != "0.0.0.0:17891" {
		t.Errorf("SubListen = %q", c.SubListen)
	}
	if c.SubToken != "0123456789abcdef0123456789abcdef" {
		t.Errorf("SubToken = %q", c.SubToken)
	}
}

func TestSubSettingsListAndValidate(t *testing.T) {
	unsetEnv(t)
	defer restoreEnv(t)

	// 订阅配置项应出现在 Settings() 列表中，供通用表单持久化
	found := map[string]bool{}
	for _, def := range Settings() {
		found[def.Key] = true
	}
	if !found[KeySubEnabled] || !found[KeySubListen] {
		t.Fatalf("Settings() missing sub keys: %v", found)
	}

	c := New()
	// 非法监听地址
	if _, err := c.ApplySetting(KeySubListen, "no-port"); err == nil {
		t.Error("expected error for invalid listen address")
	}
	if _, err := c.ApplySetting(KeySubListen, "127.0.0.1:0"); err == nil {
		t.Error("expected error for port 0")
	}
	if _, err := c.ApplySetting(KeySubEnabled, "2"); err == nil {
		t.Error("expected error for invalid bool")
	}
	// 合法值
	if changed, err := c.ApplySetting(KeySubEnabled, "0"); err != nil || !changed {
		t.Errorf("ApplySetting SubEnabled=0: changed=%v err=%v", changed, err)
	}
	if c.SubEnabled {
		t.Error("SubEnabled should be false")
	}
	if changed, err := c.ApplySetting(KeySubListen, "0.0.0.0:17891"); err != nil || !changed {
		t.Errorf("ApplySetting SubListen: changed=%v err=%v", changed, err)
	}
	if c.SubListen != "0.0.0.0:17891" {
		t.Errorf("SubListen = %q", c.SubListen)
	}
	// SettingValue 读取
	if v, ok := c.SettingValue(KeySubEnabled); !ok || v != "0" {
		t.Errorf("SettingValue(SubEnabled) = %q, %v", v, ok)
	}
}

func TestSubscriptionURL(t *testing.T) {
	c := &Config{SubListen: "127.0.0.1:17891", SubToken: "abc123"}
	if got, want := c.SubscriptionURL(), "http://127.0.0.1:17891/sub/abc123"; got != want {
		t.Errorf("SubscriptionURL = %q, want %q", got, want)
	}
	// 用户配置的具体 IP 原样拼接
	c.SubListen = "192.168.1.100:17891"
	if got, want := c.SubscriptionURL(), "http://192.168.1.100:17891/sub/abc123"; got != want {
		t.Errorf("SubscriptionURL(192.168.1.100) = %q, want %q", got, want)
	}
	// 通配监听地址（0.0.0.0/::）拼接用户下拉选中的 SubHost
	for _, listen := range []string{"0.0.0.0:17891", "[::]:17891"} {
		c.SubListen = listen
		c.SubHost = "192.168.1.50"
		if got, want := c.SubscriptionURL(), "http://192.168.1.50:17891/sub/abc123"; got != want {
			t.Errorf("SubscriptionURL(%s, host) = %q, want %q", listen, got, want)
		}
		// SubHost 为空时回退 127.0.0.1
		c.SubHost = ""
		if got, want := c.SubscriptionURL(), "http://127.0.0.1:17891/sub/abc123"; got != want {
			t.Errorf("SubscriptionURL(%s, no host) = %q, want %q", listen, got, want)
		}
	}
	// IPv6 回环地址原样拼接
	c.SubListen = "[::1]:17891"
	if got, want := c.SubscriptionURL(), "http://[::1]:17891/sub/abc123"; got != want {
		t.Errorf("SubscriptionURL([::1]) = %q, want %q", got, want)
	}
	// 非法监听地址回退默认
	c.SubListen = "no-port"
	if got, want := c.SubscriptionURL(), "http://127.0.0.1:17891/sub/abc123"; got != want {
		t.Errorf("SubscriptionURL(no-port) = %q, want %q", got, want)
	}
}

func TestSubscriptionHostValidation(t *testing.T) {
	// 空值允许（未选择）
	if err := validateIPOrEmpty(""); err != nil {
		t.Errorf("validateIPOrEmpty(\"\") = %v, want nil", err)
	}
	// 合法 IP 允许
	if err := validateIPOrEmpty("192.168.1.50"); err != nil {
		t.Errorf("validateIPOrEmpty(192.168.1.50) = %v, want nil", err)
	}
	// 非法值拒绝
	if err := validateIPOrEmpty("not-an-ip"); err == nil {
		t.Error("validateIPOrEmpty(not-an-ip) should fail")
	}
	// 环境变量覆盖
	unsetEnv(t)
	defer restoreEnv(t)
	_ = setEnv(t, "PROXYPILOT_SUB_HOST", "10.0.0.8")
	c := New()
	if c.SubHost != "10.0.0.8" {
		t.Errorf("SubHost via env = %q, want 10.0.0.8", c.SubHost)
	}
}

func TestLANIPs(t *testing.T) {
	ips := LANIPs()
	// 返回值要么为空，要么全是合法非回环 IPv4
	for _, ip := range ips {
		parsed := net.ParseIP(ip)
		if parsed == nil || parsed.IsLoopback() || parsed.To4() == nil {
			t.Errorf("LANIPs() contains invalid entry %q", ip)
		}
	}
}

func TestLoadOverridesSubTokenPersists(t *testing.T) {
	unsetEnv(t)
	defer restoreEnv(t)

	st, err := storage.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	c := New()
	c.LoadOverrides(st)
	first := c.SubToken
	if len(first) != 32 {
		t.Fatalf("generated token len = %d", len(first))
	}

	// 第二次加载应复用持久化的 token
	c2 := New()
	c2.LoadOverrides(st)
	if c2.SubToken != first {
		t.Errorf("LoadOverrides should reuse persisted token, got %q want %q", c2.SubToken, first)
	}
}

func TestLoadOverridesSubListen(t *testing.T) {
	unsetEnv(t)
	defer restoreEnv(t)

	st, err := storage.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	_ = st.SetSetting(KeySubListen, "0.0.0.0:17891")
	_ = st.SetSetting(KeySubEnabled, "0")

	c := New()
	c.LoadOverrides(st)
	if c.SubListen != "0.0.0.0:17891" {
		t.Errorf("SubListen = %q, want 0.0.0.0:17891", c.SubListen)
	}
	if c.SubEnabled {
		t.Error("SubEnabled should be false after load")
	}
}

func setEnv(t *testing.T, k, v string) error {
	t.Helper()
	savedEnv[k] = v
	return os.Setenv(k, v)
}
