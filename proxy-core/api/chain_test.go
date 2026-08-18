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

// jsonInt 把整数转成字符串（拼接 URL 用）。
func jsonInt(v int64) string {
	return strconv.FormatInt(v, 10)
}
