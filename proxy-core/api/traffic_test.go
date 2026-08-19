package api

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestTrafficEndpoint 验证流量统计接口返回完整结构（初始全零）。
func TestTrafficEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServices(t)
	r := NewRouter(s)

	w := doRequest(t, r, http.MethodGet, "/api/traffic", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", w.Code)
	}
	resp := decodeResponse(t, w)
	if resp.Code != 0 {
		t.Errorf("resp.Code = %d, want 0", resp.Code)
	}
	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("data type = %T, want map", resp.Data)
	}
	for _, key := range []string{"total", "direct", "byNode", "byChain"} {
		if _, exists := data[key]; !exists {
			t.Errorf("data[%q] missing", key)
		}
	}
	// total 初始全零
	total, _ := data["total"].(map[string]any)
	if total == nil {
		t.Fatal("total missing")
	}
	for _, k := range []string{"upload", "download", "connections"} {
		if v, _ := total[k].(float64); v != 0 {
			t.Errorf("total.%s = %v, want 0", k, v)
		}
	}
	// byNode/byChain 初始为空数组（非 null），前端用空数组遍历不会报错。
	if byNode, _ := data["byNode"].([]any); byNode == nil {
		t.Errorf("byNode = %v, want empty array", data["byNode"])
	}
	if byChain, _ := data["byChain"].([]any); byChain == nil {
		t.Errorf("byChain = %v, want empty array", data["byChain"])
	}
}
