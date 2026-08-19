package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/axetroy/ProxyPilot/proxy-core/config"
	"github.com/axetroy/ProxyPilot/proxy-core/rule"
	"github.com/gin-gonic/gin"
)

func TestPACConfigEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServices(t)
	r := NewRouter(s)

	// GET 默认配置
	w := doRequest(t, r, http.MethodGet, "/api/pac-config", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get status = %d, body %s", w.Code, w.Body.String())
	}
	var body struct {
		Code int `json:"code"`
		Data pacConfig `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != 0 {
		t.Fatalf("code = %d", body.Code)
	}
	if !body.Data.Enabled || body.Data.Mode != rule.ModeWhitelist {
		t.Errorf("default config = %+v", body.Data)
	}
	if body.Data.DirectURLs == "" || body.Data.ProxyURLs == "" {
		t.Errorf("default rule urls should be non-empty: %+v", body.Data)
	}

	// PUT 关闭分流开关（立即生效）
	w = doRequest(t, r, http.MethodPut, "/api/pac-config", map[string]any{"enabled": false})
	if w.Code != http.StatusOK {
		t.Fatalf("put enabled status = %d, body %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Enabled {
		t.Error("expected enabled=false after PUT")
	}
	if v, _ := s.Store.GetSetting(config.KeyPACEnabled); v != "0" {
		t.Errorf("persisted pac_enabled = %q, want 0", v)
	}

	// PUT 修改模式
	w = doRequest(t, r, http.MethodPut, "/api/pac-config", map[string]any{"mode": "blacklist"})
	if w.Code != http.StatusOK {
		t.Fatalf("put mode status = %d, body %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Mode != rule.ModeBlacklist {
		t.Errorf("mode = %q, want blacklist", body.Data.Mode)
	}
	if v, _ := s.Store.GetSetting(config.KeyPACMode); v != rule.ModeBlacklist {
		t.Errorf("persisted pac_mode = %q", v)
	}

	// PUT 非法模式 → 400 且不应用
	w = doRequest(t, r, http.MethodPut, "/api/pac-config", map[string]any{"mode": "invalid"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid mode status = %d, want 400", w.Code)
	}
	if s.Cfg.PACMode != rule.ModeBlacklist {
		t.Errorf("PACMode = %q after invalid put, want blacklist", s.Cfg.PACMode)
	}

	// PUT 非法 URL → 400
	w = doRequest(t, r, http.MethodPut, "/api/pac-config", map[string]any{"directUrls": "not-a-url"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid url status = %d, want 400", w.Code)
	}

	// PUT 非法刷新周期（小于 1 小时）→ 400
	w = doRequest(t, r, http.MethodPut, "/api/pac-config", map[string]any{"refresh": "10m"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid refresh status = %d, want 400", w.Code)
	}
}

// TestPACCustomRules 手动规则名单的增删与校验（整表覆盖）。
func TestPACCustomRules(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServices(t)
	r := NewRouter(s)

	// 初始为空
	w := doRequest(t, r, http.MethodGet, "/api/pac-config", nil)
	var body struct {
		Code int       `json:"code"`
		Data pacConfig `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data.CustomDirect) != 0 || len(body.Data.CustomProxy) != 0 {
		t.Errorf("initial custom lists = %v/%v, want empty", body.Data.CustomDirect, body.Data.CustomProxy)
	}
	// 空名单必须序列化为 []（而非 null），前端类型为 string[]，null 会导致 .includes 报错
	if !strings.Contains(w.Body.String(), `"customDirect":[]`) || !strings.Contains(w.Body.String(), `"customProxy":[]`) {
		t.Errorf("initial custom lists should serialize as []: %s", w.Body.String())
	}

	// 添加手动规则（大写去重，去空白）
	w = doRequest(t, r, http.MethodPut, "/api/pac-config", map[string]any{
		"customDirect": []string{"mirror.taobao.com", " Example.COM ", "example.com"},
		"customProxy":  []string{"forced.cn", "google.com"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("put custom status = %d, body %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data.CustomDirect) != 2 {
		t.Errorf("customDirect = %v, want 2 entries", body.Data.CustomDirect)
	}
	if len(body.Data.CustomProxy) != 2 {
		t.Errorf("customProxy = %v, want 2 entries", body.Data.CustomProxy)
	}
	if v, _ := s.Store.GetSetting(config.KeyPACCustomDirect); v != "mirror.taobao.com,example.com" {
		t.Errorf("persisted custom direct = %q", v)
	}
	if s.Cfg.PACCustomProxy != "forced.cn,google.com" {
		t.Errorf("cfg custom proxy = %q", s.Cfg.PACCustomProxy)
	}

	// 空数组清空直连名单
	w = doRequest(t, r, http.MethodPut, "/api/pac-config", map[string]any{"customDirect": []string{}})
	if w.Code != http.StatusOK {
		t.Fatalf("clear custom status = %d", w.Code)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data.CustomDirect) != 0 {
		t.Errorf("customDirect after clear = %v, want empty", body.Data.CustomDirect)
	}
	if v, _ := s.Store.GetSetting(config.KeyPACCustomDirect); v != "" {
		t.Errorf("persisted custom direct after clear = %q, want empty", v)
	}

	// 非法域名 → 400 且不应用
	w = doRequest(t, r, http.MethodPut, "/api/pac-config", map[string]any{
		"customProxy": []string{"bad domain.com", "valid.com"},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid domain status = %d, want 400", w.Code)
	}
	if s.Cfg.PACCustomProxy != "forced.cn,google.com" {
		t.Errorf("cfg custom proxy after invalid put = %q, want unchanged", s.Cfg.PACCustomProxy)
	}
}

// TestPACCustomRulesWithRuleManager 手动规则经 /api/pac-config 写入后立即影响分流判定。
func TestPACCustomRulesWithRuleManager(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServices(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/direct.txt":
			_, _ = w.Write([]byte("baidu.com\nforced.cn\ntaobao.com\n"))
		case "/gfw.txt":
			_, _ = w.Write([]byte("google.com\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	m := rule.NewManager(s.Cfg, s.Bus, t.TempDir()+"/test.db")
	s.Cfg.PACDirectURLs = srv.URL + "/direct.txt"
	s.Cfg.PACProxyURLs = srv.URL + "/gfw.txt"
	m.ApplyConfig()
	if err := m.SyncNow(context.TODO()); err != nil {
		t.Fatalf("SyncNow: %v", err)
	}
	s.Rule = m
	r := NewRouter(s)

	// 基线：.cn 域名默认直连
	if got := m.Match("forced.cn"); got != rule.ActionDirect {
		t.Fatalf("baseline Match(forced.cn) = %s, want direct", got)
	}

	// 添加自定义代理：覆盖 .cn 直连
	w := doRequest(t, r, http.MethodPut, "/api/pac-config", map[string]any{
		"customProxy": []string{"forced.cn"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("put status = %d, body %s", w.Code, w.Body.String())
	}
	if got := m.Match("forced.cn"); got != rule.ActionProxy {
		t.Errorf("Match(forced.cn) after custom proxy = %s, want proxy", got)
	}
	// 同步名单不受影响：google.com 仍走代理
	if got := m.Match("google.com"); got != rule.ActionProxy {
		t.Errorf("Match(google.com) = %s, want proxy", got)
	}

	// 删除后恢复 .cn 直连
	w = doRequest(t, r, http.MethodPut, "/api/pac-config", map[string]any{
		"customProxy": []string{},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("clear status = %d", w.Code)
	}
	if got := m.Match("forced.cn"); got != rule.ActionDirect {
		t.Errorf("Match(forced.cn) after clear = %s, want direct", got)
	}
}

func TestPACConfigWithRuleManager(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServices(t)

	// 规则源：httptest 提供两个列表
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/direct.txt":
			_, _ = w.Write([]byte("baidu.com\ntaobao.com\n"))
		case "/gfw.txt":
			_, _ = w.Write([]byte("google.com\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	m := rule.NewManager(s.Cfg, s.Bus, t.TempDir()+"/test.db")
	s.Cfg.PACDirectURLs = srv.URL + "/direct.txt"
	s.Cfg.PACProxyURLs = srv.URL + "/gfw.txt"
	m.ApplyConfig()
	if err := m.SyncNow(context.TODO()); err != nil {
		t.Fatalf("SyncNow: %v", err)
	}
	s.Rule = m

	r := NewRouter(s)
	w := doRequest(t, r, http.MethodGet, "/api/pac-config", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get status = %d", w.Code)
	}
	var body struct {
		Code int `json:"code"`
		Data pacConfig `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.DirectCount != 2 || body.Data.ProxyCount != 1 {
		t.Errorf("rule stats = direct %d proxy %d, want 2/1", body.Data.DirectCount, body.Data.ProxyCount)
	}
	if body.Data.SyncAt.IsZero() {
		t.Error("syncAt should be set after sync")
	}

	// POST /api/pac/sync 手动触发同步成功
	w = doRequest(t, r, http.MethodPost, "/api/pac/sync", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("sync status = %d, body %s", w.Code, w.Body.String())
	}
}