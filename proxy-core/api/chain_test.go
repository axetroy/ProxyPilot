package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/axetroy/ProxyPilot/proxy-core/model"
)

// TestChainEndpoints 链路 CRUD 端点完整流程。
func TestChainEndpoints(t *testing.T) {
	s := newTestServices(t)
	r := NewRouter(s)

	// 注入两个节点用于组链
	if s.Pool.AddNodes([]*model.ProxyNode{
		{Host: "1.2.3.4", Port: 8080, Protocol: model.ProtocolHTTP, Score: 90, Status: model.StatusAlive},
		{Host: "2.3.4.5", Port: 1080, Protocol: model.ProtocolSOCKS5, Score: 80, Status: model.StatusAlive},
	}) != 2 {
		t.Fatal("failed to add nodes")
	}
	ids := make([]int64, 0, 2)
	for _, n := range s.Pool.List() {
		ids = append(ids, n.ID)
	}

	// 创建链路
	w := doRequest(t, r, http.MethodPost, "/api/chain", map[string]any{"name": "双层链路", "nodeIds": ids})
	if w.Code != http.StatusOK {
		t.Fatalf("create status = %d, body %s", w.Code, w.Body.String())
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			ID      int64   `json:"id"`
			Name    string  `json:"name"`
			NodeIDs []int64 `json:"nodeIds"`
			Enabled bool    `json:"enabled"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Code != 0 || resp.Data.Name != "双层链路" || len(resp.Data.NodeIDs) != 2 || resp.Data.Enabled {
		t.Fatalf("created = %+v", resp.Data)
	}
	chainID := resp.Data.ID

	// 列表
	w = doRequest(t, r, http.MethodGet, "/api/chains", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d", w.Code)
	}

	// 更新：启用 + 改名单节点（去重保序）
	enabled := true
	only := []int64{ids[0]}
	w = doRequest(t, r, http.MethodPut, "/api/chain/"+jsonInt(chainID), map[string]any{"name": "单跳", "nodeIds": only, "enabled": enabled})
	if w.Code != http.StatusOK {
		t.Fatalf("update status = %d, body %s", w.Code, w.Body.String())
	}
	got, err := s.Store.GetChain(chainID)
	if err != nil {
		t.Fatalf("get chain: %v", err)
	}
	if got == nil || got.Name != "单跳" || len(got.NodeIDs) != 1 || !got.Enabled {
		t.Fatalf("updated chain = %+v", got)
	}

	// 非法节点：400
	w = doRequest(t, r, http.MethodPost, "/api/chain", map[string]any{"name": "bad", "nodeIds": []int64{99999}})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid node status = %d, want 400", w.Code)
	}

	// 空节点：400
	w = doRequest(t, r, http.MethodPost, "/api/chain", map[string]any{"name": "empty", "nodeIds": []int64{}})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty node status = %d, want 400", w.Code)
	}

	// 删除
	w = doRequest(t, r, http.MethodDelete, "/api/chain/"+jsonInt(chainID), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body %s", w.Code, w.Body.String())
	}
	if got, _ := s.Store.GetChain(chainID); got != nil {
		t.Fatalf("chain still exists after delete: %+v", got)
	}

	// 删除不存在的链路：404
	w = doRequest(t, r, http.MethodDelete, "/api/chain/"+jsonInt(chainID), nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("delete missing status = %d, want 404", w.Code)
	}
}

// TestEgressChainStrategy PUT 支持切换为 chain 策略并持久化。
func TestEgressChainStrategy(t *testing.T) {
	s := newTestServices(t)
	r := NewRouter(s)

	w := doRequest(t, r, http.MethodPut, "/api/egress", map[string]any{"strategy": "chain"})
	if w.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body %s", w.Code, w.Body.String())
	}
	if s.Selector.Strategy() != "chain" {
		t.Fatalf("strategy = %s, want chain", s.Selector.Strategy())
	}
	if v, _ := s.Store.GetSetting("egress_strategy"); v != "chain" {
		t.Fatalf("persisted egress_strategy = %q", v)
	}
}

// TestTestChainEndpoint 测试链路端点：返回每跳结果，链路为空时报错，链路不存在时 404。
func TestTestChainEndpoint(t *testing.T) {
	s := newTestServices(t)
	r := NewRouter(s)

	// 空链路：400
	chain, err := s.Store.CreateChain("空链", []int64{})
	if err != nil {
		t.Fatalf("create chain: %v", err)
	}
	w := doRequest(t, r, http.MethodPost, "/api/chain/"+jsonInt(chain.ID)+"/test", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty chain status = %d, body %s", w.Code, w.Body.String())
	}

	// 不存在的链路：404
	w = doRequest(t, r, http.MethodPost, "/api/chain/999999/test", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing chain status = %d, want 404", w.Code)
	}

	// 有节点但不可达：返回每跳结果，失败跳 OK=false
	s.Pool.AddNodes([]*model.ProxyNode{
		{Host: "127.0.0.1", Port: 1, Protocol: model.ProtocolHTTP, Score: 90, Status: model.StatusAlive},
	})
	var nodeIDs []int64
	for _, n := range s.Pool.List() {
		nodeIDs = append(nodeIDs, n.ID)
	}
	chain2, err := s.Store.CreateChain("不可达链", nodeIDs)
	if err != nil {
		t.Fatalf("create chain 2: %v", err)
	}
	w = doRequest(t, r, http.MethodPost, "/api/chain/"+jsonInt(chain2.ID)+"/test", nil)
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
	first := hops[0].(map[string]any)
	if first["ok"] == true {
		t.Errorf("first hop = %+v, want failed (unreachable node)", first)
	}
	if first["error"] == "" {
		t.Errorf("first hop error empty, want readable failure")
	}

	// 手动测试结果同步到链路的健康状态字段（last_ok=false + 失败原因）。
	got, err := s.Store.GetChain(chain2.ID)
	if err != nil {
		t.Fatalf("get chain: %v", err)
	}
	if got == nil || got.LastOK || got.LastCheckedAt == nil || got.LastError == "" {
		t.Fatalf("chain health after failed manual test = %+v, want lastOk=false with error", got)
	}
}

// jsonInt 把整数转成字符串（拼接 URL 用）。
func jsonInt(v int64) string {
	return strconv.FormatInt(v, 10)
}
