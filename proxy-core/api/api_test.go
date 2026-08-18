package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"github.com/axetroy/ProxyPilot/proxy-core/bus"
	"github.com/axetroy/ProxyPilot/proxy-core/collector"
	"github.com/axetroy/ProxyPilot/proxy-core/config"
	"github.com/axetroy/ProxyPilot/proxy-core/gateway"
	"github.com/axetroy/ProxyPilot/proxy-core/model"
	"github.com/axetroy/ProxyPilot/proxy-core/pool"
	"github.com/axetroy/ProxyPilot/proxy-core/scheduler"
	"github.com/axetroy/ProxyPilot/proxy-core/storage"
)

// mockChecker 提供确定性的检测结果，避免真实网络。
type mockChecker struct{}

func (mockChecker) Check(node *model.ProxyNode) (model.CheckResult, error) {
	return model.CheckResult{OK: true, Latency: 10}, nil
}

// newTestServices 构造完整的 Services（内存存储 + mock 检测器 + 未启动网关）。
func newTestServices(t *testing.T) *Services {
	t.Helper()
	unsetEnv(t)
	t.Cleanup(restoreEnv)

	st, err := storage.New(":memory:")
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	b := bus.New()
	poolMgr := pool.NewManager(st, mockChecker{}, b, 4)
	col := collector.NewManager(st, b, poolMgr, 5*time.Second)
	sel := scheduler.NewSelector(poolMgr)
	gw := gateway.NewGateway(poolMgr, sel, b, "127.0.0.1:0")

	return &Services{
		Cfg:       config.New(),
		Store:     st,
		Pool:      poolMgr,
		Collector: col,
		Gateway:   gw,
		Selector:  sel,
		Bus:       b,
	}
}

// ---------- env helpers ----------

var savedEnv = map[string]string{}

func unsetEnv(t *testing.T) {
	t.Helper()
	keys := []string{
		"PROXYPILOT_API_BIND", "PROXYPILOT_DB_PATH", "PROXYPILOT_PROXY_PORT",
		"PROXYPILOT_TOKEN", "PROXYPILOT_CHECK_TARGET",
		"PROXYPILOT_CHECK_TIMEOUT", "PROXYPILOT_CHECK_CONCURRENCY", "PROXYPILOT_REFRESH_INTERVAL",
		"PROXYPILOT_SUB_ENABLED", "PROXYPILOT_SUB_LISTEN", "PROXYPILOT_SUB_TOKEN",
		"PROXYPILOT_HISTORY_RETENTION_DAYS",
		"PROXYPILOT_PAC_ENABLED", "PROXYPILOT_PAC_MODE",
		"PROXYPILOT_PAC_DIRECT_URLS", "PROXYPILOT_PAC_PROXY_URLS", "PROXYPILOT_PAC_REFRESH_INTERVAL",
	}
	for _, k := range keys {
		if v, ok := os.LookupEnv(k); ok {
			savedEnv[k] = v
			_ = os.Unsetenv(k)
		}
	}
}

func restoreEnv() {
	for k, v := range savedEnv {
		_ = os.Setenv(k, v)
	}
	savedEnv = map[string]string{}
}

// doRequest 执行一次带可选 JSON body 的请求。
func doRequest(t *testing.T, r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// decodeResponse 解析统一响应结构。
func decodeResponse(t *testing.T, w *httptest.ResponseRecorder) response {
	t.Helper()
	var resp response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response %q: %v", w.Body.String(), err)
	}
	return resp
}

// ---------- buildSystemStatus ----------

func TestBuildSystemStatus(t *testing.T) {
	node := &model.ProxyNode{Host: "1.2.3.4", Port: 8080}

	s := buildSystemStatus(true, 10, 5, node, "127.0.0.1:7892", "127.0.0.1:7892", "0.1.4")
	if !s.Running || s.ProxyCount != 10 || s.AliveCount != 5 {
		t.Errorf("basic fields wrong: %+v", s)
	}
	if s.CurrentIP != "1.2.3.4" {
		t.Errorf("CurrentIP = %q, want 1.2.3.4", s.CurrentIP)
	}
	if s.CurrentNode == nil || s.CurrentNode.Host != "1.2.3.4" {
		t.Errorf("CurrentNode = %+v, want 1.2.3.4", s.CurrentNode)
	}
	if s.HTTPProxyBind != "127.0.0.1:7892" || s.SOCKSProxyBind != "127.0.0.1:7892" {
		t.Errorf("binds wrong: %+v", s)
	}

	// 无当前节点
	s2 := buildSystemStatus(false, 0, 0, nil, "", "", "0.1.4")
	if s2.CurrentIP != "" {
		t.Errorf("CurrentIP = %q, want empty", s2.CurrentIP)
	}
	if s2.CurrentNode != nil {
		t.Errorf("CurrentNode = %+v, want nil", s2.CurrentNode)
	}
}

// ---------- status / settings ----------

func TestStatusEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServices(t)
	r := NewRouter(s)

	w := doRequest(t, r, http.MethodGet, "/api/status", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", w.Code)
	}
	resp := decodeResponse(t, w)
	if resp.Code != 0 {
		t.Errorf("resp.Code = %d, want 0", resp.Code)
	}
	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("data type = %T", resp.Data)
	}
	if data["running"] != false {
		t.Errorf("running = %v, want false", data["running"])
	}
	if data["version"] != config.Version {
		t.Errorf("version = %v, want %s", data["version"], config.Version)
	}
}

func TestListSettings(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServices(t)
	r := NewRouter(s)

	w := doRequest(t, r, http.MethodGet, "/api/settings", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", w.Code)
	}
	resp := decodeResponse(t, w)
	items, ok := resp.Data.([]any)
	if !ok {
		t.Fatalf("data type = %T", resp.Data)
	}
	if len(items) != len(config.Settings()) {
		t.Errorf("settings count = %d, want %d", len(items), len(config.Settings()))
	}
	// 应包含 proxy_port
	found := false
	for _, it := range items {
		m := it.(map[string]any)
		if m["key"] == "proxy_port" {
			found = true
			if m["value"] != "7892" {
				t.Errorf("proxy_port value = %v, want 7892", m["value"])
			}
		}
	}
	if !found {
		t.Error("proxy_port not found in settings")
	}
}

func TestUpdateSettingsValid(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServices(t)
	r := NewRouter(s)

	w := doRequest(t, r, http.MethodPut, "/api/settings", map[string]string{"check_target": "https://example.com/204"})
	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	resp := decodeResponse(t, w)
	if resp.Code != 0 {
		t.Errorf("resp.Code = %d, want 0", resp.Code)
	}
	if s.Cfg.CheckTarget != "https://example.com/204" {
		t.Errorf("CheckTarget = %q, want updated", s.Cfg.CheckTarget)
	}
	// 应持久化
	if v, _ := s.Store.GetSetting("check_target"); v != "https://example.com/204" {
		t.Errorf("persisted check_target = %q", v)
	}
}

func TestUpdateSettingsInvalid(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServices(t)
	r := NewRouter(s)

	w := doRequest(t, r, http.MethodPut, "/api/settings", map[string]string{"proxy_port": "abc"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want 400", w.Code)
	}
	// 非法值不应应用
	if s.Cfg.ProxyPort != 7892 {
		t.Errorf("ProxyPort = %d, want unchanged 7892", s.Cfg.ProxyPort)
	}
}

func TestUpdateSettingsEmpty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServices(t)
	r := NewRouter(s)

	w := doRequest(t, r, http.MethodPut, "/api/settings", map[string]string{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want 400", w.Code)
	}
}

// ---------- subscriptions ----------

func TestCreateSubscription(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServices(t)
	r := NewRouter(s)

	w := doRequest(t, r, http.MethodPost, "/api/subscription", map[string]any{
		"name": "test-sub", "url": "https://example.com/sub", "interval": 3600,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	resp := decodeResponse(t, w)
	if resp.Code != 0 {
		t.Errorf("resp.Code = %d, want 0", resp.Code)
	}
	subs, err := s.Collector.List()
	if err != nil || len(subs) != 1 {
		t.Fatalf("subscriptions = %d (err=%v), want 1", len(subs), err)
	}
	if subs[0].Name != "test-sub" {
		t.Errorf("name = %q", subs[0].Name)
	}
}

func TestCreateSubscriptionBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServices(t)
	r := NewRouter(s)

	// 缺 name/url
	w := doRequest(t, r, http.MethodPost, "/api/subscription", map[string]any{"name": "x"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want 400", w.Code)
	}
}

func TestListSubscriptions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServices(t)
	r := NewRouter(s)

	doRequest(t, r, http.MethodPost, "/api/subscription", map[string]any{
		"name": "sub1", "url": "https://example.com/1", "interval": 3600,
	})

	w := doRequest(t, r, http.MethodGet, "/api/subscriptions", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", w.Code)
	}
	resp := decodeResponse(t, w)
	items, ok := resp.Data.([]any)
	if !ok {
		t.Fatalf("data type = %T", resp.Data)
	}
	if len(items) != 1 {
		t.Errorf("subscriptions = %d, want 1", len(items))
	}
}

func TestDeleteSubscription(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServices(t)
	r := NewRouter(s)

	doRequest(t, r, http.MethodPost, "/api/subscription", map[string]any{
		"name": "sub1", "url": "https://example.com/1", "interval": 3600,
	})
	subs, _ := s.Collector.List()
	id := subs[0].ID

	w := doRequest(t, r, http.MethodDelete, fmt.Sprintf("/api/subscription/%d", id), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", w.Code)
	}
	// 再删一次应 404
	w2 := doRequest(t, r, http.MethodDelete, fmt.Sprintf("/api/subscription/%d", id), nil)
	if w2.Code != http.StatusNotFound {
		t.Fatalf("second delete status = %d, want 404", w2.Code)
	}
	// 非法 id 应 400
	w3 := doRequest(t, r, http.MethodDelete, "/api/subscription/abc", nil)
	if w3.Code != http.StatusBadRequest {
		t.Fatalf("bad id status = %d, want 400", w3.Code)
	}
}

func TestRefreshSubscription(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 订阅源：返回一个节点列表
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("1.2.3.4:8080\n5.6.7.8:1080"))
	}))
	defer src.Close()

	s := newTestServices(t)
	r := NewRouter(s)

	doRequest(t, r, http.MethodPost, "/api/subscription", map[string]any{
		"name": "sub1", "url": src.URL, "interval": 3600,
	})
	subs, _ := s.Collector.List()
	id := subs[0].ID

	w := doRequest(t, r, http.MethodPost, fmt.Sprintf("/api/subscription/%d/refresh", id), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	// 节点应进入池
	if n := s.Pool.Count(); n != 2 {
		t.Errorf("pool count = %d, want 2", n)
	}
	// 不存在的订阅应 404
	w2 := doRequest(t, r, http.MethodPost, "/api/subscription/99999/refresh", nil)
	if w2.Code != http.StatusNotFound {
		t.Fatalf("missing sub status = %d, want 404", w2.Code)
	}
}

func TestUpdateSubscription(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServices(t)
	r := NewRouter(s)

	doRequest(t, r, http.MethodPost, "/api/subscription", map[string]any{
		"name": "sub1", "url": "https://example.com/1", "interval": 3600,
	})
	subs, _ := s.Collector.List()
	id := subs[0].ID

	w := doRequest(t, r, http.MethodPut, fmt.Sprintf("/api/subscription/%d", id), map[string]any{
		"name": "renamed", "url": "https://example.com/2", "interval": 7200, "enabled": false,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	subs, _ = s.Collector.List()
	if subs[0].Name != "renamed" || subs[0].URL != "https://example.com/2" || subs[0].Enabled {
		t.Errorf("subscription not updated: %+v", subs[0])
	}
}

// ---------- proxies ----------

func TestListProxies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServices(t)
	r := NewRouter(s)

	s.Pool.AddNodes([]*model.ProxyNode{
		{Host: "1.2.3.4", Port: 8080, Protocol: model.ProtocolHTTP, Status: model.StatusAlive},
		{Host: "5.6.7.8", Port: 1080, Protocol: model.ProtocolSOCKS5, Status: model.StatusDead},
	})

	w := doRequest(t, r, http.MethodGet, "/api/proxies", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", w.Code)
	}
	resp := decodeResponse(t, w)
	items, ok := resp.Data.([]any)
	if !ok {
		t.Fatalf("data type = %T", resp.Data)
	}
	if len(items) != 2 {
		t.Errorf("proxies = %d, want 2", len(items))
	}

	// 按状态过滤
	w2 := doRequest(t, r, http.MethodGet, "/api/proxies?status=alive", nil)
	resp2 := decodeResponse(t, w2)
	items2, _ := resp2.Data.([]any)
	if len(items2) != 1 {
		t.Errorf("alive proxies = %d, want 1", len(items2))
	}
}

func TestDeleteProxy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServices(t)
	r := NewRouter(s)

	s.Pool.AddNodes([]*model.ProxyNode{
		{Host: "1.2.3.4", Port: 8080, Protocol: model.ProtocolHTTP, Status: model.StatusAlive},
	})
	nodes := s.Pool.List()
	id := nodes[0].ID

	w := doRequest(t, r, http.MethodDelete, fmt.Sprintf("/api/proxy/%d", id), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", w.Code)
	}
	if s.Pool.Count() != 0 {
		t.Errorf("pool count = %d, want 0", s.Pool.Count())
	}
	// 非法 id
	w2 := doRequest(t, r, http.MethodDelete, "/api/proxy/abc", nil)
	if w2.Code != http.StatusBadRequest {
		t.Fatalf("bad id status = %d, want 400", w2.Code)
	}
}

func TestCheckProxiesWithID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServices(t)
	r := NewRouter(s)

	s.Pool.AddNodes([]*model.ProxyNode{
		{Host: "1.2.3.4", Port: 8080, Protocol: model.ProtocolHTTP, Status: model.StatusAlive},
	})
	nodes := s.Pool.List()
	id := nodes[0].ID

	w := doRequest(t, r, http.MethodPost, "/api/proxy/check", map[string]any{"id": id})
	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	resp := decodeResponse(t, w)
	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("data type = %T", resp.Data)
	}
	if data["ok"] != true {
		t.Errorf("check result ok = %v, want true", data["ok"])
	}

	// 不存在的节点应 404
	w2 := doRequest(t, r, http.MethodPost, "/api/proxy/check", map[string]any{"id": 99999})
	if w2.Code != http.StatusNotFound {
		t.Fatalf("missing node status = %d, want 404", w2.Code)
	}
}

func TestCheckProxiesAll(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServices(t)
	r := NewRouter(s)

	w := doRequest(t, r, http.MethodPost, "/api/proxy/check", map[string]any{})
	if w.Code != http.StatusAccepted {
		t.Fatalf("status code = %d, want 202", w.Code)
	}
}

// ---------- pinned proxy（指定固定出口） ----------

func TestPinProxyEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServices(t)
	r := NewRouter(s)

	s.Pool.AddNodes([]*model.ProxyNode{
		{Host: "1.2.3.4", Port: 8080, Protocol: model.ProtocolHTTP, Status: model.StatusAlive, Score: 90},
		{Host: "5.6.7.8", Port: 8081, Protocol: model.ProtocolHTTP, Status: model.StatusAlive, Score: 50},
	})
	nodes := s.Pool.List()
	pinID := nodes[0].ID

	w := doRequest(t, r, http.MethodPut, "/api/proxy/pin", map[string]any{"id": pinID})
	if w.Code != http.StatusOK {
		t.Fatalf("pin status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	resp := decodeResponse(t, w)
	if resp.Code != 0 {
		t.Errorf("resp.Code = %d, want 0", resp.Code)
	}
	if s.Selector.PinnedID() != pinID {
		t.Errorf("PinnedID() = %d, want %d", s.Selector.PinnedID(), pinID)
	}
	// 应持久化
	if v, _ := s.Store.GetSetting(config.KeyPinnedProxy); v != fmt.Sprintf("%d", pinID) {
		t.Errorf("persisted pinned setting = %q, want %d", v, pinID)
	}

	// status 响应应包含 pinnedNode
	w2 := doRequest(t, r, http.MethodGet, "/api/status", nil)
	if w2.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", w2.Code)
	}
	resp2 := decodeResponse(t, w2)
	data, ok := resp2.Data.(map[string]any)
	if !ok {
		t.Fatalf("data type = %T", resp2.Data)
	}
	pm, hasPin := data["pinnedNode"].(map[string]any)
	if !hasPin {
		t.Fatal("expected pinnedNode in status response")
	}
	if int64(pm["id"].(float64)) != pinID {
		t.Errorf("pinnedNode id = %v, want %d", pm["id"], pinID)
	}
}

func TestUnpinProxyEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServices(t)
	r := NewRouter(s)

	s.Pool.AddNodes([]*model.ProxyNode{
		{Host: "1.2.3.4", Port: 8080, Protocol: model.ProtocolHTTP, Status: model.StatusAlive},
	})
	pinID := s.Pool.List()[0].ID

	doRequest(t, r, http.MethodPut, "/api/proxy/pin", map[string]any{"id": pinID})
	w := doRequest(t, r, http.MethodDelete, "/api/proxy/pin", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("unpin status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	if s.Selector.PinnedID() != 0 {
		t.Errorf("PinnedID() = %d after unpin, want 0", s.Selector.PinnedID())
	}
	if v, _ := s.Store.GetSetting(config.KeyPinnedProxy); v != "" {
		t.Errorf("persisted pinned setting = %q after unpin, want empty", v)
	}

	// 取消后 /api/status 的响应体不应再包含 pinnedNode（omitempty 缺席）
	w2 := doRequest(t, r, http.MethodGet, "/api/status", nil)
	if w2.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", w2.Code)
	}
	resp2 := decodeResponse(t, w2)
	data, ok := resp2.Data.(map[string]any)
	if !ok {
		t.Fatalf("data type = %T", resp2.Data)
	}
	if _, hasPin := data["pinnedNode"]; hasPin {
		t.Error("expected pinnedNode absent from status response after unpin")
	}
}

func TestPinProxyErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServices(t)
	r := NewRouter(s)

	// 不存在的节点 -> 404
	w := doRequest(t, r, http.MethodPut, "/api/proxy/pin", map[string]any{"id": 99999})
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing node pin status = %d, want 404", w.Code)
	}
	// 非法 id（<=0）-> 400
	w2 := doRequest(t, r, http.MethodPut, "/api/proxy/pin", map[string]any{"id": 0})
	if w2.Code != http.StatusBadRequest {
		t.Fatalf("zero id pin status = %d, want 400", w2.Code)
	}
	// 缺 id -> 400
	w3 := doRequest(t, r, http.MethodPut, "/api/proxy/pin", map[string]any{})
	if w3.Code != http.StatusBadRequest {
		t.Fatalf("missing id pin status = %d, want 400", w3.Code)
	}
}

func TestDeleteProxyClearsPin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServices(t)
	r := NewRouter(s)

	s.Pool.AddNodes([]*model.ProxyNode{
		{Host: "1.2.3.4", Port: 8080, Protocol: model.ProtocolHTTP, Status: model.StatusAlive},
	})
	pinID := s.Pool.List()[0].ID

	doRequest(t, r, http.MethodPut, "/api/proxy/pin", map[string]any{"id": pinID})
	if s.Selector.PinnedID() != pinID {
		t.Fatalf("setup: PinnedID() = %d, want %d", s.Selector.PinnedID(), pinID)
	}

	w := doRequest(t, r, http.MethodDelete, fmt.Sprintf("/api/proxy/%d", pinID), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	// 删除的正是固定节点时，应自动取消指定并清除持久化
	if s.Selector.PinnedID() != 0 {
		t.Errorf("PinnedID() = %d after deleting pinned node, want 0", s.Selector.PinnedID())
	}
	if v, _ := s.Store.GetSetting(config.KeyPinnedProxy); v != "" {
		t.Errorf("persisted pinned setting = %q after delete, want empty", v)
	}
}

// ---------- gateway ----------

func TestGatewayStartStop(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServices(t)
	r := NewRouter(s)

	w := doRequest(t, r, http.MethodPost, "/api/gateway/start", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("start status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	if !s.Gateway.Running() {
		t.Error("gateway not running after start")
	}
	resp := decodeResponse(t, w)
	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("data type = %T", resp.Data)
	}
	if data["http"] == "" || data["socks5"] == "" {
		t.Errorf("gateway addrs missing: %+v", data)
	}

	w2 := doRequest(t, r, http.MethodPost, "/api/gateway/stop", nil)
	if w2.Code != http.StatusOK {
		t.Fatalf("stop status = %d, want 200", w2.Code)
	}
	if s.Gateway.Running() {
		t.Error("gateway still running after stop")
	}
}

func TestUpdateSettingsRestartsGateway(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServices(t)
	r := NewRouter(s)

	// 先启动网关
	w := doRequest(t, r, http.MethodPost, "/api/gateway/start", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("start status = %d, want 200", w.Code)
	}
	oldAddr := s.Gateway.HTTPAddr()

	// 修改 proxy_port 应触发网关重启
	w2 := doRequest(t, r, http.MethodPut, "/api/settings", map[string]string{"proxy_port": "17899"})
	if w2.Code != http.StatusOK {
		t.Fatalf("update status = %d, want 200 (body=%s)", w2.Code, w2.Body.String())
	}
	if !s.Gateway.Running() {
		t.Fatal("gateway should still be running after port change")
	}
	if s.Gateway.HTTPAddr() == oldAddr {
		t.Errorf("gateway addr unchanged: %s", s.Gateway.HTTPAddr())
	}
	if !strings.Contains(s.Gateway.HTTPAddr(), ":17899") {
		t.Errorf("gateway addr = %q, want port 17899", s.Gateway.HTTPAddr())
	}
}

func TestUpdateSettingsHotReload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServices(t)
	r := NewRouter(s)

	// check_target 更新应重建检测器（不报错）
	w := doRequest(t, r, http.MethodPut, "/api/settings", map[string]string{"check_target": "https://example.com/204"})
	if w.Code != http.StatusOK {
		t.Fatalf("update status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	// check_concurrency 更新
	w2 := doRequest(t, r, http.MethodPut, "/api/settings", map[string]string{"check_concurrency": "8"})
	if w2.Code != http.StatusOK {
		t.Fatalf("update concurrency status = %d, want 200", w2.Code)
	}
	if s.Cfg.CheckConcurrency != 8 {
		t.Errorf("CheckConcurrency = %d, want 8", s.Cfg.CheckConcurrency)
	}
	// refresh_interval 更新（duration 格式）
	w3 := doRequest(t, r, http.MethodPut, "/api/settings", map[string]string{"refresh_interval": "10m"})
	if w3.Code != http.StatusOK {
		t.Fatalf("update interval status = %d, want 200", w3.Code)
	}
	if s.Cfg.RefreshInterval != 10*time.Minute {
		t.Errorf("RefreshInterval = %v, want 10m", s.Cfg.RefreshInterval)
	}
}

// ---------- 数据库维护（手动瘦身） ----------

func TestDbStatusEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServices(t)
	r := NewRouter(s)

	// 写入两条检测历史，状态应如实上报
	n := &model.ProxyNode{Host: "1.2.3.4", Port: 8080, Protocol: model.ProtocolHTTP}
	s.Pool.AddNodes([]*model.ProxyNode{n})
	node := s.Pool.List()[0]
	for i := 0; i < 2; i++ {
		if err := s.Store.AddCheckHistory(model.CheckHistory{ProxyID: node.ID, Success: true, Latency: 10}); err != nil {
			t.Fatalf("add history: %v", err)
		}
	}

	w := doRequest(t, r, http.MethodGet, "/api/db/status", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	resp := decodeResponse(t, w)
	if resp.Code != 0 {
		t.Errorf("resp.Code = %d, want 0", resp.Code)
	}
	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("data type = %T", resp.Data)
	}
	if int64(data["historyCount"].(float64)) != 2 {
		t.Errorf("historyCount = %v, want 2", data["historyCount"])
	}
	// 新写入的记录都未过期，可清理数应为 0
	if int64(data["purgeable"].(float64)) != 0 {
		t.Errorf("purgeable = %v, want 0", data["purgeable"])
	}
	if int64(data["retentionDays"].(float64)) != 7 {
		t.Errorf("retentionDays = %v, want 7", data["retentionDays"])
	}
}

func TestCompactDbEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServices(t)
	r := NewRouter(s)

	w := doRequest(t, r, http.MethodPost, "/api/db/compact", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	resp := decodeResponse(t, w)
	if resp.Code != 0 {
		t.Errorf("resp.Code = %d, want 0", resp.Code)
	}
	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("data type = %T", resp.Data)
	}
	if int64(data["deleted"].(float64)) != 0 {
		t.Errorf("deleted = %v, want 0 (no stale history)", data["deleted"])
	}
	if int64(data["historyCount"].(float64)) != 0 {
		t.Errorf("historyCount = %v, want 0", data["historyCount"])
	}
}

// ---------- websocket ----------

func TestWebsocketStreamsEvents(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServices(t)
	r := NewRouter(s)

	ts := httptest.NewServer(r)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// 发布一个事件，客户端应收到
	s.Bus.Info("hello from test")
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var evt map[string]any
	if err := conn.ReadJSON(&evt); err != nil {
		t.Fatalf("read event: %v", err)
	}
	if evt["message"] != "hello from test" {
		t.Errorf("event message = %v, want hello from test", evt["message"])
	}
}

func TestWebsocketInvalidToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServices(t)
	r := NewRouter(s)

	ts := httptest.NewServer(r)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws?token=wrong-token"
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Fatal("dial with wrong token should fail")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %v, want 401", resp)
	}
}

// ---------- middleware ----------

func TestCORSPreflight(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServices(t)
	r := NewRouter(s)

	req := httptest.NewRequest(http.MethodOptions, "/api/status", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status code = %d, want 204", w.Code)
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "http://localhost:5173" {
		t.Errorf("CORS origin = %q", w.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestTokenAuthPasses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServices(t)
	r := NewRouter(s)

	// token 中间件当前放行所有请求（本地开发模式）
	w := doRequest(t, r, http.MethodGet, "/api/status", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", w.Code)
	}
}
