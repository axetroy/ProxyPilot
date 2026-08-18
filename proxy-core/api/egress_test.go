package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/axetroy/ProxyPilot/proxy-core/config"
	"github.com/axetroy/ProxyPilot/proxy-core/model"
	"github.com/axetroy/ProxyPilot/proxy-core/scheduler"
)

// TestEgressEndpoint GET 返回默认策略与策略列表，PUT 切换并持久化。
func TestEgressEndpoint(t *testing.T) {
	s := newTestServices(t)
	r := NewRouter(s)

	// 注入一个存活节点，验证 aliveCount
	if s.Pool.AddNodes([]*model.ProxyNode{{Host: "1.2.3.4", Port: 8080, Protocol: model.ProtocolHTTP, Score: 90, Status: model.StatusAlive}}) != 1 {
		t.Fatal("failed to add node")
	}

	// GET：默认加权策略
	w := doRequest(t, r, http.MethodGet, "/api/egress", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET status = %d", w.Code)
	}
	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Strategy   string `json:"strategy"`
			AliveCount int    `json:"aliveCount"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Code != 0 || resp.Data.Strategy != string(scheduler.StrategyWeighted) {
		t.Fatalf("GET = %+v", resp)
	}
	if resp.Data.AliveCount != 1 {
		t.Fatalf("aliveCount = %d, want 1", resp.Data.AliveCount)
	}

	// PUT：切换为轮询策略
	w = doRequest(t, r, http.MethodPut, "/api/egress", map[string]any{"strategy": "round-robin"})
	if w.Code != http.StatusOK {
		t.Fatalf("PUT status = %d", w.Code)
	}
	if s.Selector.Strategy() != scheduler.StrategyRoundRobin {
		t.Fatalf("selector strategy = %s, want round-robin", s.Selector.Strategy())
	}
	// 持久化验证
	if v, _ := s.Store.GetSetting(config.KeyEgressStrategy); v != "round-robin" {
		t.Fatalf("persisted egress_strategy = %q", v)
	}

	// PUT 非法策略：400 且策略不变
	w = doRequest(t, r, http.MethodPut, "/api/egress", map[string]any{"strategy": "nonsense"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid strategy status = %d, want 400", w.Code)
	}
	if s.Selector.Strategy() != scheduler.StrategyRoundRobin {
		t.Fatalf("invalid strategy changed selector to %s", s.Selector.Strategy())
	}
}

// TestEgressFixedWithPinID PUT 固定策略可同时指定节点。
func TestEgressFixedWithPinID(t *testing.T) {
	s := newTestServices(t)
	r := NewRouter(s)

	if s.Pool.AddNodes([]*model.ProxyNode{{Host: "1.2.3.4", Port: 8080, Protocol: model.ProtocolHTTP, Score: 90, Status: model.StatusAlive}}) != 1 {
		t.Fatal("failed to add node")
	}
	nodeID := s.Pool.List()[0].ID

	w := doRequest(t, r, http.MethodPut, "/api/egress", map[string]any{"strategy": "fixed", "pinId": nodeID})
	if w.Code != http.StatusOK {
		t.Fatalf("PUT status = %d", w.Code)
	}
	if s.Selector.Strategy() != scheduler.StrategyFixed {
		t.Fatalf("strategy = %s, want fixed", s.Selector.Strategy())
	}
	if s.Selector.PinnedID() != nodeID {
		t.Fatalf("pinnedID = %d, want %d", s.Selector.PinnedID(), nodeID)
	}
	// 固定策略下 Next 应返回指定节点
	if got := s.Selector.Next(); got == nil || got.ID != nodeID {
		t.Fatalf("Next = %+v, want pinned node", got)
	}
	// 持久化验证
	if v, _ := s.Store.GetSetting(config.KeyPinnedProxy); v == "" {
		t.Fatal("pinned_proxy_id not persisted")
	}
	if v, _ := s.Store.GetSetting(config.KeyEgressStrategy); v != "fixed" {
		t.Fatalf("persisted egress_strategy = %q", v)
	}
}

// TestEgressAutoChain PUT 切换到 auto-chain 并保存层数/每层选择策略。
func TestEgressAutoChain(t *testing.T) {
	s := newTestServices(t)
	r := NewRouter(s)

	// 注入存活节点（auto-chain 需要节点池非空才能出口）
	if s.Pool.AddNodes([]*model.ProxyNode{{Host: "1.2.3.4", Port: 8080, Protocol: model.ProtocolHTTP, Score: 90, Status: model.StatusAlive}}) != 1 {
		t.Fatal("failed to add node")
	}

	// PUT：切换到 auto-chain，指定 3 层 / random
	w := doRequest(t, r, http.MethodPut, "/api/egress", map[string]any{"strategy": "auto-chain", "chainHops": 3, "chainSelection": "random"})
	if w.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body = %s", w.Code, w.Body.String())
	}
	if s.Selector.Strategy() != scheduler.StrategyAutoChain {
		t.Fatalf("selector strategy = %s, want auto-chain", s.Selector.Strategy())
	}
	if s.Cfg.ChainHops != 3 {
		t.Fatalf("cfg.ChainHops = %d, want 3", s.Cfg.ChainHops)
	}
	if s.Cfg.ChainSelection != "random" {
		t.Fatalf("cfg.ChainSelection = %q, want random", s.Cfg.ChainSelection)
	}
	// 持久化验证
	if v, _ := s.Store.GetSetting(config.KeyChainHops); v != "3" {
		t.Fatalf("persisted chain_hops = %q, want 3", v)
	}
	if v, _ := s.Store.GetSetting(config.KeyChainSelection); v != "random" {
		t.Fatalf("persisted chain_selection = %q, want random", v)
	}
	// 响应中应回传配置
	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Strategy       string `json:"strategy"`
			ChainHops      int    `json:"chainHops"`
			ChainSelection string `json:"chainSelection"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data.Strategy != "auto-chain" || resp.Data.ChainHops != 3 || resp.Data.ChainSelection != "random" {
		t.Fatalf("response data = %+v", resp.Data)
	}

	// PUT 不带层数/策略时沿用当前已保存配置（不重置）
	w = doRequest(t, r, http.MethodPut, "/api/egress", map[string]any{"strategy": "auto-chain"})
	if w.Code != http.StatusOK {
		t.Fatalf("PUT default status = %d", w.Code)
	}
	if s.Cfg.ChainHops != 3 || s.Cfg.ChainSelection != "random" {
		t.Fatalf("existing config not kept: hops=%d selection=%q", s.Cfg.ChainHops, s.Cfg.ChainSelection)
	}

	// PUT 非法选择策略：400 且策略不变（先校验后应用）
	w = doRequest(t, r, http.MethodPut, "/api/egress", map[string]any{"strategy": "auto-chain", "chainSelection": "nonsense"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid chainSelection status = %d, want 400", w.Code)
	}
	if s.Selector.Strategy() != scheduler.StrategyAutoChain {
		t.Fatalf("invalid chainSelection changed strategy to %s", s.Selector.Strategy())
	}
	if s.Cfg.ChainSelection != "random" {
		t.Fatalf("invalid chainSelection changed ChainSelection to %q", s.Cfg.ChainSelection)
	}

	// PUT 层数超限：400 且配置不变
	w = doRequest(t, r, http.MethodPut, "/api/egress", map[string]any{"strategy": "auto-chain", "chainHops": 99})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("chainHops over limit status = %d, want 400", w.Code)
	}
	if s.Cfg.ChainHops != 3 {
		t.Fatalf("chainHops over limit changed ChainHops to %d", s.Cfg.ChainHops)
	}
}

// TestEgressAutoChainDefaults 全新配置下切换 auto-chain 不带参数使用默认值（2 层 / weighted）。
func TestEgressAutoChainDefaults(t *testing.T) {
	s := newTestServices(t)
	r := NewRouter(s)

	if s.Pool.AddNodes([]*model.ProxyNode{{Host: "1.2.3.4", Port: 8080, Protocol: model.ProtocolHTTP, Score: 90, Status: model.StatusAlive}}) != 1 {
		t.Fatal("failed to add node")
	}

	// 全新 services：cfg 为默认值（2 / weighted）
	w := doRequest(t, r, http.MethodPut, "/api/egress", map[string]any{"strategy": "auto-chain"})
	if w.Code != http.StatusOK {
		t.Fatalf("PUT default status = %d", w.Code)
	}
	if s.Cfg.ChainHops != 2 || s.Cfg.ChainSelection != "weighted" {
		t.Fatalf("defaults not applied: hops=%d selection=%q", s.Cfg.ChainHops, s.Cfg.ChainSelection)
	}
}

// TestEgressAutoChainPersistence 重启（新 Services）后自动链路配置恢复。
func TestEgressAutoChainPersistence(t *testing.T) {
	s := newTestServices(t)
	r := NewRouter(s)

	if s.Pool.AddNodes([]*model.ProxyNode{{Host: "1.2.3.4", Port: 8080, Protocol: model.ProtocolHTTP, Score: 90, Status: model.StatusAlive}}) != 1 {
		t.Fatal("failed to add node")
	}

	w := doRequest(t, r, http.MethodPut, "/api/egress", map[string]any{"strategy": "auto-chain", "chainHops": 4, "chainSelection": "best"})
	if w.Code != http.StatusOK {
		t.Fatalf("PUT status = %d", w.Code)
	}

	// 模拟重启：从同一 Store 重新加载配置（LoadOverrides 读取持久化的 chain 配置）
	cfg := config.New()
	cfg.LoadOverrides(s.Store)
	if cfg.ChainHops != 4 {
		t.Fatalf("restored ChainHops = %d, want 4", cfg.ChainHops)
	}
	if cfg.ChainSelection != "best" {
		t.Fatalf("restored ChainSelection = %q, want best", cfg.ChainSelection)
	}
}

// TestTestAutoChainEndpoint 自动链路测试端点：按配置挑选节点逐跳测试，返回结果。
func TestTestAutoChainEndpoint(t *testing.T) {
	s := newTestServices(t)
	r := NewRouter(s)

	// 无存活节点：400
	w := doRequest(t, r, http.MethodPost, "/api/egress/auto-chain/test", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("no alive node status = %d, want 400", w.Code)
	}

	// 注入一个不可达的存活节点（127.0.0.1:1 会握手失败，但结果结构完整）
	s.Pool.AddNodes([]*model.ProxyNode{
		{Host: "127.0.0.1", Port: 1, Protocol: model.ProtocolHTTP, Score: 90, Status: model.StatusAlive},
	})

	// 配置 auto-chain 2 层（存活不足自动降为 1）
	w = doRequest(t, r, http.MethodPut, "/api/egress", map[string]any{"strategy": "auto-chain", "chainHops": 2, "chainSelection": "best"})
	if w.Code != http.StatusOK {
		t.Fatalf("PUT status = %d", w.Code)
	}

	w = doRequest(t, r, http.MethodPost, "/api/egress/auto-chain/test", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("test status = %d, body %s", w.Code, w.Body.String())
	}
	resp := decodeResponse(t, w)
	if resp.Code != 0 {
		t.Fatalf("resp.Code = %d, body %s", resp.Code, w.Body.String())
	}
	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("data type = %T", resp.Data)
	}
	hops, ok := data["hops"].([]any)
	if !ok || len(hops) == 0 {
		t.Fatalf("hops = %+v, want non-empty", data["hops"])
	}
	// 存活节点不足层数时按实际数量建链（1 跳）
	first := hops[0].(map[string]any)
	if first["key"] == "" {
		t.Errorf("hop key empty, want protocol://host:port")
	}
	if first["ok"] == true {
		t.Errorf("first hop = %+v, want failed (unreachable node)", first)
	}
	if first["error"] == "" {
		t.Errorf("first hop error empty, want readable failure")
	}
}

// TestTestAutoChainEndpointNoNodes 自动链路测试在无存活节点时返回 400。
func TestTestAutoChainEndpointNoNodes(t *testing.T) {
	s := newTestServices(t)
	r := NewRouter(s)

	// 不配置 auto-chain（默认 weighted），但测试端点按配置挑选节点
	w := doRequest(t, r, http.MethodPost, "/api/egress/auto-chain/test", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("no alive node status = %d, want 400", w.Code)
	}
}
