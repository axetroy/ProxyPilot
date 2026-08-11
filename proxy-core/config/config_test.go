package config

import (
	"os"
	"testing"
	"time"

	"github.com/axetroy/ProxyPilot/proxy-core/storage"
)

func TestNewDefaults(t *testing.T) {
	// 确保环境变量不干扰默认值测试
	unsetEnv(t)
	defer restoreEnv(t)

	c := New()
	if c.APIBind != "127.0.0.1:17890" {
		t.Errorf("APIBind = %q, want 127.0.0.1:17890", c.APIBind)
	}
	if c.DBPath != "proxypilot.db" {
		t.Errorf("DBPath = %q, want proxypilot.db", c.DBPath)
	}
	if c.HTTPProxyBind != "127.0.0.1:7892" {
		t.Errorf("HTTPProxyBind = %q, want 127.0.0.1:7892", c.HTTPProxyBind)
	}
	if c.SOCKSProxyBind != "127.0.0.1:7893" {
		t.Errorf("SOCKSProxyBind = %q, want 127.0.0.1:7893", c.SOCKSProxyBind)
	}
	if c.CheckTarget != "https://www.cloudflare.com/cdn-cgi/trace" {
		t.Errorf("CheckTarget = %q, want default cloudflare trace target", c.CheckTarget)
	}
	if c.CheckTimeout != 10*time.Second {
		t.Errorf("CheckTimeout = %v, want 10s", c.CheckTimeout)
	}
	if c.CheckConcurrency != 32 {
		t.Errorf("CheckConcurrency = %d, want 32", c.CheckConcurrency)
	}
	if c.RefreshInterval != 15*time.Minute {
		t.Errorf("RefreshInterval = %v, want 15m", c.RefreshInterval)
	}
	if c.SessionToken == "" {
		t.Error("SessionToken should not be empty")
	}
}

func TestNewGeneratesRandomToken(t *testing.T) {
	unsetEnv(t)
	defer restoreEnv(t)

	c1 := New()
	c2 := New()
	if c1.SessionToken == c2.SessionToken {
		t.Fatal("expected distinct session tokens across instances")
	}
	if len(c1.SessionToken) != 32 { // 16 bytes hex
		t.Errorf("expected 32-char hex token, got %q (len %d)", c1.SessionToken, len(c1.SessionToken))
	}
}

func TestApplyEnvOverrides(t *testing.T) {
	unsetEnv(t)
	defer restoreEnv(t)

	_ = os.Setenv("PROXYPILOT_API_BIND", "0.0.0.0:9999")
	_ = os.Setenv("PROXYPILOT_DB_PATH", "/tmp/test.db")
	_ = os.Setenv("PROXYPILOT_HTTP_BIND", "0.0.0.0:8888")
	_ = os.Setenv("PROXYPILOT_SOCKS5_BIND", "0.0.0.0:8889")
	_ = os.Setenv("PROXYPILOT_TOKEN", "my-token")
	_ = os.Setenv("PROXYPILOT_CHECK_TARGET", "http://example.com/204")
	_ = os.Setenv("PROXYPILOT_CHECK_TIMEOUT", "3s")
	_ = os.Setenv("PROXYPILOT_CHECK_CONCURRENCY", "8")
	_ = os.Setenv("PROXYPILOT_REFRESH_INTERVAL", "1m")

	c := New()
	if c.APIBind != "0.0.0.0:9999" {
		t.Errorf("APIBind = %q", c.APIBind)
	}
	if c.DBPath != "/tmp/test.db" {
		t.Errorf("DBPath = %q", c.DBPath)
	}
	if c.HTTPProxyBind != "0.0.0.0:8888" {
		t.Errorf("HTTPProxyBind = %q", c.HTTPProxyBind)
	}
	if c.SOCKSProxyBind != "0.0.0.0:8889" {
		t.Errorf("SOCKSProxyBind = %q", c.SOCKSProxyBind)
	}
	if c.SessionToken != "my-token" {
		t.Errorf("SessionToken = %q", c.SessionToken)
	}
	if c.CheckTarget != "http://example.com/204" {
		t.Errorf("CheckTarget = %q", c.CheckTarget)
	}
	if c.CheckTimeout != 3*time.Second {
		t.Errorf("CheckTimeout = %v", c.CheckTimeout)
	}
	if c.CheckConcurrency != 8 {
		t.Errorf("CheckConcurrency = %d", c.CheckConcurrency)
	}
	if c.RefreshInterval != time.Minute {
		t.Errorf("RefreshInterval = %v", c.RefreshInterval)
	}
}

func TestApplyEnvIgnoresInvalidValues(t *testing.T) {
	unsetEnv(t)
	defer restoreEnv(t)

	_ = os.Setenv("PROXYPILOT_CHECK_TIMEOUT", "not-a-duration")
	_ = os.Setenv("PROXYPILOT_CHECK_CONCURRENCY", "-5")
	_ = os.Setenv("PROXYPILOT_REFRESH_INTERVAL", "garbage")

	c := New()
	if c.CheckTimeout != 10*time.Second {
		t.Errorf("CheckTimeout = %v, want default 10s", c.CheckTimeout)
	}
	if c.CheckConcurrency != 32 {
		t.Errorf("CheckConcurrency = %d, want default 32", c.CheckConcurrency)
	}
	if c.RefreshInterval != 15*time.Minute {
		t.Errorf("RefreshInterval = %v, want default 15m", c.RefreshInterval)
	}
}

func TestApplyEnvEmptyValuesKeepDefaults(t *testing.T) {
	unsetEnv(t)
	defer restoreEnv(t)

	_ = os.Setenv("PROXYPILOT_API_BIND", "")
	_ = os.Setenv("PROXYPILOT_TOKEN", "")

	c := New()
	if c.APIBind != "127.0.0.1:17890" {
		t.Errorf("APIBind = %q, want default", c.APIBind)
	}
	if c.SessionToken == "" {
		t.Error("SessionToken should still be generated")
	}
}

// ---------- helpers ----------

var savedEnv = map[string]string{}

func unsetEnv(t *testing.T) {
	t.Helper()
	keys := []string{
		"PROXYPILOT_API_BIND", "PROXYPILOT_DB_PATH", "PROXYPILOT_HTTP_BIND",
		"PROXYPILOT_SOCKS5_BIND", "PROXYPILOT_TOKEN", "PROXYPILOT_CHECK_TARGET",
		"PROXYPILOT_CHECK_TIMEOUT", "PROXYPILOT_CHECK_CONCURRENCY", "PROXYPILOT_REFRESH_INTERVAL",
	}
	for _, k := range keys {
		if v, ok := os.LookupEnv(k); ok {
			savedEnv[k] = v
			_ = os.Unsetenv(k)
		}
	}
}

func restoreEnv(t *testing.T) {
	t.Helper()
	for k, v := range savedEnv {
		_ = os.Setenv(k, v)
	}
	savedEnv = map[string]string{}
}

// ---------- settings ----------

func TestApplySettingValidatesAndApplies(t *testing.T) {
	unsetEnv(t)
	defer restoreEnv(t)

	c := New()
	// 非法值：校验应失败且不修改
	if changed, err := c.ApplySetting(KeyHTTPBind, "not-a-port"); err == nil || changed {
		t.Errorf("expected error for invalid http bind, got changed=%v err=%v", changed, err)
	}
	if c.HTTPProxyBind != "127.0.0.1:7892" {
		t.Errorf("HTTPProxyBind should not change on invalid input, got %q", c.HTTPProxyBind)
	}
	if changed, err := c.ApplySetting(KeyCheckConcurr, "abc"); err == nil || changed {
		t.Errorf("expected error for invalid concurrency, got changed=%v err=%v", changed, err)
	}
	if c.CheckConcurrency != 32 {
		t.Errorf("CheckConcurrency should stay 32, got %d", c.CheckConcurrency)
	}

	// 合法值
	if changed, err := c.ApplySetting(KeyCheckTarget, "https://www.google.com/generate_204"); err != nil || !changed {
		t.Errorf("ApplySetting valid target: changed=%v err=%v", changed, err)
	}
	if c.CheckTarget != "https://www.google.com/generate_204" {
		t.Errorf("CheckTarget = %q", c.CheckTarget)
	}
	// 相同值不产生变更
	if changed, err := c.ApplySetting(KeyCheckTarget, "https://www.google.com/generate_204"); err != nil || changed {
		t.Errorf("ApplySetting same value should be no-op, changed=%v err=%v", changed, err)
	}
}

func TestLoadOverridesFromStore(t *testing.T) {
	unsetEnv(t)
	defer restoreEnv(t)

	c := New()
	st, err := storage.New(":memory:")
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	defer func() { _ = st.Close() }()

	// 持久化两个配置项
	if err := st.SetSetting(KeyHTTPBind, "127.0.0.1:7901"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if err := st.SetSetting(KeyRefreshPeriod, "30m"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	c.LoadOverrides(st)
	if c.HTTPProxyBind != "127.0.0.1:7901" {
		t.Errorf("HTTPProxyBind after LoadOverrides = %q, want 127.0.0.1:7901", c.HTTPProxyBind)
	}
	if c.RefreshInterval != 30*time.Minute {
		t.Errorf("RefreshInterval after LoadOverrides = %v, want 30m", c.RefreshInterval)
	}
	// 未持久化的保持默认
	if c.CheckTarget != "https://www.cloudflare.com/cdn-cgi/trace" {
		t.Errorf("CheckTarget should keep default, got %q", c.CheckTarget)
	}
}

func TestLoadOverridesSkipsInvalidPersisted(t *testing.T) {
	unsetEnv(t)
	defer restoreEnv(t)

	c := New()
	st, err := storage.New(":memory:")
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	defer func() { _ = st.Close() }()

	// 持久化的非法值应被跳过，保持默认
	if err := st.SetSetting(KeyCheckTarget, "not-a-url"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	c.LoadOverrides(st)
	if c.CheckTarget != "https://www.cloudflare.com/cdn-cgi/trace" {
		t.Errorf("invalid persisted value should be skipped, got %q", c.CheckTarget)
	}
}
