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
