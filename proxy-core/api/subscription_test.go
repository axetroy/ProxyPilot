package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/axetroy/ProxyPilot/proxy-core/config"
	"github.com/axetroy/ProxyPilot/proxy-core/model"
	"github.com/axetroy/ProxyPilot/proxy-core/parser"
	"github.com/gin-gonic/gin"
)

func TestExportProxies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServices(t)
	s.Pool.AddNodes([]*model.ProxyNode{
		{Host: "1.1.1.1", Port: 80, Protocol: model.ProtocolHTTP, Status: model.StatusAlive},
		{Host: "2.2.2.2", Port: 1080, Protocol: model.ProtocolSOCKS5, Username: "u", Password: "p", Status: model.StatusAlive},
	})
	router := NewRouter(s)

	// json（默认）
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/export", nil)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("export json status = %d, body %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Code  int `json:"code"`
		Data  struct {
			Total int `json:"total"`
			Nodes []struct {
				Host string `json:"host"`
			} `json:"nodes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Total != 2 || len(body.Data.Nodes) != 2 {
		t.Fatalf("export json total = %d, nodes = %d", body.Data.Total, len(body.Data.Nodes))
	}

	// base64
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/export?format=base64", nil)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("export base64 status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("export base64 content-type = %q", ct)
	}
	plain, err := base64.StdEncoding.DecodeString(strings.TrimSpace(rec.Body.String()))
	if err != nil {
		t.Fatalf("decode exported base64: %v", err)
	}
	if nodes := parser.ParseProxyList(string(plain)); len(nodes) != 2 {
		t.Fatalf("export base64 nodes = %d, want 2", len(nodes))
	}

	// plain
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/export?format=plain", nil)
	router.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "socks5://u:p@2.2.2.2:1080") {
		t.Fatalf("export plain missing node: %q", rec.Body.String())
	}

	// 非法格式
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/export?format=yaml", nil)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("export invalid format status = %d, want 400", rec.Code)
	}
}

func TestExportEmptyPool(t *testing.T) {
	s := newTestServices(t)
	router := NewRouter(s)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/export?format=base64", nil)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Body.String() != "" {
		t.Fatalf("empty pool export = %q, want empty", rec.Body.String())
	}
}

func TestSubscriptionConfigAPI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServices(t)
	router := NewRouter(s)

	// GET 初始配置
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/subscription", nil)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get subscription status = %d", rec.Code)
	}
	var body struct {
		Data subscriptionConfig `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Data.Enabled || body.Data.Token != s.Cfg.SubToken {
		t.Fatalf("unexpected initial config: %+v", body.Data)
	}
	if !strings.Contains(body.Data.URL, "/sub/"+s.Cfg.SubToken) {
		t.Fatalf("subscription URL missing token: %q", body.Data.URL)
	}

	// PUT 关闭订阅
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/subscription", strings.NewReader(`{"enabled":false}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("put subscription status = %d, body %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Enabled {
		t.Fatal("expected enabled=false after PUT")
	}
	if v, _ := s.Store.GetSetting(config.KeySubEnabled); v != "0" {
		t.Fatalf("persisted sub enabled = %q, want 0", v)
	}

	// PUT 重置 token：token 应变化且持久化
	oldToken := s.Cfg.SubToken
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/subscription", strings.NewReader(`{"resetToken":true}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Token == oldToken {
		t.Fatal("token should change after reset")
	}
	if v, _ := s.Store.GetSetting(config.KeySubToken); v != body.Data.Token {
		t.Fatalf("persisted token = %q, want %q", v, body.Data.Token)
	}

	// PUT 非法监听地址
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/subscription", strings.NewReader(`{"listen":"bad-addr"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid listen status = %d, want 400", rec.Code)
	}

	// PUT 通配监听 + 选择的对外 host：URL 应拼接所选 host 并持久化
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/subscription", strings.NewReader(`{"listen":"0.0.0.0:17891","host":"192.168.1.50"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("put listen+host status = %d, body %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Host != "192.168.1.50" {
		t.Fatalf("host = %q, want 192.168.1.50", body.Data.Host)
	}
	if want := "http://192.168.1.50:17891/sub/" + s.Cfg.SubToken; body.Data.URL != want {
		t.Fatalf("URL = %q, want %q", body.Data.URL, want)
	}
	if v, _ := s.Store.GetSetting(config.KeySubHost); v != "192.168.1.50" {
		t.Fatalf("persisted host = %q, want 192.168.1.50", v)
	}

	// PUT 非法 host
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/subscription", strings.NewReader(`{"host":"not-an-ip"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid host status = %d, want 400", rec.Code)
	}
}

func TestSubscriptionServer(t *testing.T) {
	s := newTestServices(t)
	router := NewSubscriptionRouter(s)

	// 错误 token → 401
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/sub/wrong-token", nil)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token status = %d, want 401", rec.Code)
	}

	// 正确 token、空池 → 200 空内容
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/sub/"+s.Cfg.SubToken, nil)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("sub status = %d, body %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "" {
		t.Fatalf("empty sub = %q", rec.Body.String())
	}

	// 有节点：base64 可解析回节点行
	s.Pool.AddNodes([]*model.ProxyNode{
		{Host: "3.3.3.3", Port: 8080, Protocol: model.ProtocolHTTP, Status: model.StatusAlive},
	})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/sub/"+s.Cfg.SubToken, nil)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("sub status = %d", rec.Code)
	}
	plain, err := base64.StdEncoding.DecodeString(strings.TrimSpace(rec.Body.String()))
	if err != nil {
		t.Fatalf("decode sub base64: %v", err)
	}
	if nodes := parser.ParseProxyList(string(plain)); len(nodes) != 1 || nodes[0].Host != "3.3.3.3" {
		t.Fatalf("sub nodes = %+v", nodes)
	}

	// format=plain
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/sub/"+s.Cfg.SubToken+"?format=plain", nil)
	router.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "http://3.3.3.3:8080") {
		t.Fatalf("plain sub = %q", rec.Body.String())
	}
}

func TestSubscriptionServerDisabled(t *testing.T) {
	s := newTestServices(t)
	s.Cfg.SubEnabled = false
	router := NewSubscriptionRouter(s)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/sub/"+s.Cfg.SubToken, nil)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("disabled sub status = %d, want 404", rec.Code)
	}
}

func TestConstantTimeEqual(t *testing.T) {
	if !constantTimeEqual("abc", "abc") {
		t.Fatal("equal strings should match")
	}
	if constantTimeEqual("abc", "abd") {
		t.Fatal("different strings should not match")
	}
	if constantTimeEqual("", "x") {
		t.Fatal("empty vs non-empty should not match")
	}
}
