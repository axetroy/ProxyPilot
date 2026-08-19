package rule

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/axetroy/ProxyPilot/proxy-core/config"
)

func newTestManager(t *testing.T, mutate func(*config.Config)) *Manager {
	t.Helper()
	cfg := config.New()
	cfg.PACDirectURLs = ""
	cfg.PACProxyURLs = ""
	if mutate != nil {
		mutate(cfg)
	}
	dir := t.TempDir()
	m := NewManager(cfg, nil, filepath.Join(dir, "test.db"))
	return m
}

func TestParseRules(t *testing.T) {
	text := `
# comment
example.com
*.sub.example.com
+.plus.example.net
  .dot.example.org
UPPER.CASE.COM
example.com
bad domain with space
-invalid
`
	got := ParseRules(text)
	want := []string{"example.com", "sub.example.com", "plus.example.net", "dot.example.org", "upper.case.com"}
	if len(got) != len(want) {
		t.Fatalf("ParseRules length = %d, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ParseRules[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestValidDomain(t *testing.T) {
	cases := map[string]bool{
		"example.com":        true,
		"a-b.example.co.uk":  true,
		"cn":                 true,
		"123.example.cn":     true,
		"":                   false,
		".example.com":       false,
		"example.com.":       false,
		"bad space.com":      false,
		"bad/example.com":    false,
		"bad:example.com":    false,
		"*bad.example.com":   false,
		strings.Repeat("a", 256) + ".com": false,
	}
	for in, want := range cases {
		if got := validDomain(in); got != want {
			t.Errorf("validDomain(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestMatchWhitelist(t *testing.T) {
	m := newTestManager(t, func(c *config.Config) {
		c.PACEnabled = true
		c.PACMode = ModeWhitelist
	})
	m.mu.Lock()
	m.direct = map[string]struct{}{"taobao.com": {}}
	m.proxy = map[string]struct{}{"google.com": {}}
	m.mu.Unlock()

	cases := map[string]Action{
		"localhost":        ActionDirect, // 本机
		"127.0.0.1":        ActionDirect, // 本机
		"192.168.1.1":      ActionDirect, // 内网
		"10.0.0.5":         ActionDirect, // 内网
		"router.local":     ActionDirect, // 内网命名
		"printer":          ActionDirect, // 单标签
		"baidu.com.cn":     ActionDirect, // .cn
		"example.cn":       ActionDirect, // .cn
		"google.com":       ActionProxy,  // 命中代理名单
		"taobao.com":       ActionDirect, // 命中直连名单
		"some-foreign.com": ActionProxy,  // 默认走代理
		"223.5.5.5":        ActionDirect, // 纯 IP：阿里 DNS（大陆）
		"8.8.8.8":          ActionProxy,  // 纯 IP：非大陆
	}
	for host, want := range cases {
		if got := m.Match(host); got != want {
			t.Errorf("Match(%q) = %s, want %s", host, got, want)
		}
	}
}

func TestMatchBlacklist(t *testing.T) {
	m := newTestManager(t, func(c *config.Config) {
		c.PACEnabled = true
		c.PACMode = ModeBlacklist
	})
	m.mu.Lock()
	m.proxy = map[string]struct{}{"google.com": {}}
	m.mu.Unlock()

	cases := map[string]Action{
		"google.com":       ActionProxy,  // 命中代理名单
		"baidu.com":        ActionDirect, // 默认直连
		"some-foreign.com": ActionDirect, // 默认直连
		"127.0.0.1":        ActionDirect,
		// 黑名单默认直连：纯 IP 无法命中域名代理名单，非大陆也直连放行
		"8.8.8.8": ActionDirect,
	}
	for host, want := range cases {
		if got := m.Match(host); got != want {
			t.Errorf("Match(%q) = %s, want %s", host, got, want)
		}
	}
}

// TestMatchSuffix 验证后缀匹配：真实流量几乎都是子域名，规则列表是裸域名，
// 子域名应命中父域条目。
func TestMatchSuffix(t *testing.T) {
	m := newTestManager(t, func(c *config.Config) {
		c.PACEnabled = true
		c.PACMode = ModeWhitelist
	})
	m.mu.Lock()
	m.direct = map[string]struct{}{"baidu.com": {}}
	m.proxy = map[string]struct{}{"google.com": {}, "youtube.com": {}}
	m.mu.Unlock()

	cases := map[string]Action{
		"www.google.com":        ActionProxy,  // 命中父域 google.com
		"mail.google.com":       ActionProxy,  // 多级子域同样命中
		"www.youtube.com":       ActionProxy,  // 命中父域 youtube.com
		"app.baidu.com":         ActionDirect, // 命中父域 baidu.com
		"a.b.baidu.com":         ActionDirect, // 多级子域命中
		"notgoogle.com":         ActionProxy,  // 非子域：默认走代理
		"google.com.evil.com":   ActionProxy,  // 中间子域 google.com.evil.com 不命中（只有 evil.com 会命中规则）
		"some-foreign-site.com": ActionProxy,
	}
	for host, want := range cases {
		if got := m.Match(host); got != want {
			t.Errorf("Match(%q) = %s, want %s", host, got, want)
		}
	}

	// 黑名单模式同样支持后缀匹配
	m.mu.Lock()
	m.mode = ModeBlacklist
	m.mu.Unlock()
	if got := m.Match("www.youtube.com"); got != ActionProxy {
		t.Errorf("blacklist Match(www.youtube.com) = %s, want proxy", got)
	}
	if got := m.Match("cdn.notgoogle.com"); got != ActionDirect {
		t.Errorf("blacklist Match(cdn.notgoogle.com) = %s, want direct", got)
	}
}

// TestApplyConfig 验证修改 cfg 后调用 ApplyConfig 能同步到管理器内部状态。
func TestApplyConfig(t *testing.T) {
	m := newTestManager(t, func(c *config.Config) {
		c.PACEnabled = true
		c.PACMode = ModeWhitelist
	})
	// 修改 cfg，未同步前管理器仍用旧值
	m.cfg.PACEnabled = false
	m.cfg.PACMode = ModeBlacklist
	if !m.Enabled() {
		t.Error("enabled should not change before ApplyConfig")
	}
	m.ApplyConfig()
	if m.Enabled() {
		t.Error("enabled should become false after ApplyConfig")
	}
	if m.Match("8.8.8.8") != ActionDirect {
		t.Error("mode should become blacklist after ApplyConfig")
	}
}

// TestShuntDisabled 分流开关关闭时，Shunt 恒返回 false（全部走代理）。
func TestShuntDisabled(t *testing.T) {
	m := newTestManager(t, func(c *config.Config) {
		c.PACEnabled = false
		c.PACMode = ModeWhitelist
	})
	m.mu.Lock()
	m.direct = map[string]struct{}{"taobao.com": {}}
	m.mu.Unlock()
	shunt := m.Shunt()
	if shunt("taobao.com") {
		t.Error("shunt should return false when disabled")
	}
	if shunt("baidu.com.cn") {
		t.Error("shunt should return false when disabled even for .cn")
	}
}

// TestShuntEnabled 开关开启时，命中直连（.cn / 直连名单）返回 true。
func TestShuntEnabled(t *testing.T) {
	m := newTestManager(t, func(c *config.Config) {
		c.PACEnabled = true
		c.PACMode = ModeWhitelist
	})
	m.mu.Lock()
	m.direct = map[string]struct{}{"taobao.com": {}}
	m.mu.Unlock()
	shunt := m.Shunt()
	if !shunt("taobao.com") {
		t.Error("shunt should return true for direct list domain")
	}
	if !shunt("baidu.com.cn") {
		t.Error("shunt should return true for .cn")
	}
	if shunt("google.com") {
		t.Error("shunt should return false for unknown foreign domain")
	}
}

// TestSyncNow 用 httptest 验证拉取-解析-写缓存的完整链路（不依赖真实网络）。
func TestSyncNow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/direct.txt":
			_, _ = w.Write([]byte("# direct\nbaidu.com\ntaobao.com\n"))
		case "/proxy.txt":
			_, _ = w.Write([]byte("# proxy\ngoogle.com\nfacebook.com\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	m := newTestManager(t, func(c *config.Config) {
		c.PACEnabled = true
		c.PACDirectURLs = srv.URL + "/direct.txt"
		c.PACProxyURLs = srv.URL + "/proxy.txt"
	})
	if err := m.SyncNow(context.TODO()); err != nil {
		t.Fatalf("SyncNow: %v", err)
	}
	dc, pc, _, syncErr, _ := m.Stats()
	if dc != 2 || pc != 2 {
		t.Fatalf("stats = direct %d proxy %d, want 2/2", dc, pc)
	}
	if syncErr != "" {
		t.Errorf("syncErr = %q, want empty", syncErr)
	}
	// 验证匹配结果
	if m.Match("taobao.com") != ActionDirect {
		t.Error("taobao.com should be direct after sync")
	}
	if m.Match("google.com") != ActionProxy {
		t.Error("google.com should be proxy after sync")
	}
	// 验证缓存文件已写入
	if _, err := os.Stat(m.cachePath); err != nil {
		t.Errorf("cache file not written: %v", err)
	}
}

// TestSyncNowFallbackMirror 主源失败时自动尝试备用镜像。
func TestSyncNowFallbackMirror(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/direct.txt":
			_, _ = w.Write([]byte("direct-mirror.com\n"))
		case "/proxy.txt":
			_, _ = w.Write([]byte("proxy-mirror.com\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	m := newTestManager(t, func(c *config.Config) {
		c.PACEnabled = true
		c.PACDirectURLs = "http://127.0.0.1:1/none.txt," + srv.URL + "/direct.txt"
		c.PACProxyURLs = srv.URL + "/proxy.txt"
	})
	if err := m.SyncNow(context.TODO()); err != nil {
		t.Fatalf("SyncNow: %v", err)
	}
	if m.Match("direct-mirror.com") != ActionDirect {
		t.Error("direct-mirror.com should be direct (from fallback mirror)")
	}
	if m.Match("proxy-mirror.com") != ActionProxy {
		t.Error("proxy-mirror.com should be proxy")
	}
}

// TestLoadCacheBuiltin 无缓存文件时回退到内置兜底列表。
func TestLoadCacheBuiltin(t *testing.T) {
	m := newTestManager(t, nil)
	if err := m.LoadCache(); err != nil {
		t.Fatalf("LoadCache: %v", err)
	}
	dc, pc, _, _, _ := m.Stats()
	if dc == 0 || pc == 0 {
		t.Fatalf("builtin rules should be non-empty, got direct=%d proxy=%d", dc, pc)
	}
	if m.Match("baidu.com") != ActionDirect {
		t.Error("baidu.com should be direct from builtin list")
	}
	if m.Match("google.com") != ActionProxy {
		t.Error("google.com should be proxy from builtin list")
	}
}

// TestMatchCustomRules 手动规则名单优先级高于同步名单与 .cn / geoip：
// 用户明确指定的域名（无论分流模式）按手动代理 > 手动直连裁决。
func TestMatchCustomRules(t *testing.T) {
	m := newTestManager(t, func(c *config.Config) {
		c.PACEnabled = true
		c.PACMode = ModeWhitelist
		c.PACCustomDirect = "google.com, mirror.taobao.com"
		c.PACCustomProxy = "forced.com, forced.cn"
	})
	m.mu.Lock()
	m.direct = map[string]struct{}{"taobao.com": {}, "forced.com": {}}
	m.proxy = map[string]struct{}{"google.com": {}, "forced.cn": {}, "some-proxy.com": {}}
	m.mu.Unlock()

	cases := map[string]Action{
		// 自定义代理：覆盖同步直连名单 / .cn
		"forced.com":     ActionProxy, // 同步直连名单里也有，但手动代理优先
		"forced.cn":      ActionProxy, // .cn 规则被手动代理覆盖
		"sub.forced.com": ActionProxy, // 手动规则同样按父域后缀匹配
		// 自定义直连：覆盖同步代理名单
		"google.com":        ActionDirect, // 同步代理名单里也有，但手动直连优先
		"www.google.com":    ActionDirect, // 后缀命中自定义直连
		"mirror.taobao.com": ActionDirect, // 自定义直连（同步里无此域名，属新增）
		// 未被手动命中的行为不变
		"some-proxy.com": ActionProxy, // 同步代理名单
		"taobao.com":     ActionDirect, // 同步直连名单
		"baidu.com":      ActionProxy,  // 白名单默认走代理
		"unknown.com":    ActionProxy,  // 默认走代理
	}
	for host, want := range cases {
		if got := m.Match(host); got != want {
			t.Errorf("Match(%q) = %s, want %s", host, got, want)
		}
	}

	// 黑名单模式下手动代理同样生效（覆盖默认直连）
	m2 := newTestManager(t, func(c *config.Config) {
		c.PACEnabled = true
		c.PACMode = ModeBlacklist
		c.PACCustomProxy = "forced.proxy.com"
		c.PACCustomDirect = "relaxed.com"
	})
	m2.mu.Lock()
	m2.proxy = map[string]struct{}{"other-proxy.com": {}}
	m2.mu.Unlock()
	if got := m2.Match("forced.proxy.com"); got != ActionProxy {
		t.Errorf("blacklist custom proxy: got %s, want proxy", got)
	}
	if got := m2.Match("relaxed.com"); got != ActionDirect {
		t.Errorf("blacklist custom direct: got %s, want direct", got)
	}
	if got := m2.Match("other-proxy.com"); got != ActionProxy {
		t.Errorf("blacklist synced proxy: got %s, want proxy", got)
	}
	if got := m2.Match("default.com"); got != ActionDirect {
		t.Errorf("blacklist default: got %s, want direct", got)
	}

	// ApplyConfig 重新加载手动名单（对应 /api/pac-config 更新路径）
	m2.cfg.PACCustomProxy = "new-forced.com"
	m2.ApplyConfig()
	if got := m2.Match("new-forced.com"); got != ActionProxy {
		t.Errorf("after ApplyConfig custom proxy: got %s, want proxy", got)
	}
	if got := m2.Match("forced.proxy.com"); got != ActionDirect {
		t.Errorf("after ApplyConfig removed custom proxy: got %s, want direct", got)
	}

	// CustomStats 返回手动名单条数
	dc, pc := m2.CustomStats()
	if dc != 1 || pc != 1 {
		t.Errorf("CustomStats = (%d, %d), want (1, 1)", dc, pc)
	}
}