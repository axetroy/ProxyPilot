package api

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// chainPayload 创建/更新代理链路的请求体。
type chainPayload struct {
	Name    string  `json:"name" binding:"required"`
	NodeIDs []int64 `json:"nodeIds" binding:"required"`
	// Enabled 仅更新时使用（*bool 区分「未传」与「显式 false」）。
	Enabled *bool `json:"enabled"`
}

// listChains 返回全部代理链路。
func (s *Services) listChains(c *gin.Context) {
	chains, err := s.Store.ListChains()
	if err != nil {
		c.JSON(http.StatusInternalServerError, fail(1, err.Error()))
		return
	}
	c.JSON(http.StatusOK, ok(chains))
}

// createChain 新建代理链路（默认停用，需在界面显式启用）。
func (s *Services) createChain(c *gin.Context) {
	var payload chainPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, fail(400, "请求体格式错误"))
		return
	}
	ids, err := s.validateChainNodes(payload.NodeIDs)
	if err != nil {
		c.JSON(http.StatusBadRequest, fail(400, err.Error()))
		return
	}
	chain, err := s.Store.CreateChain(payload.Name, ids)
	if err != nil {
		c.JSON(http.StatusInternalServerError, fail(1, err.Error()))
		return
	}
	s.Bus.Info(fmt.Sprintf("created proxy chain %q with %d nodes", chain.Name, len(ids)))
	c.JSON(http.StatusOK, ok(chain))
}

// updateChain 更新链路名称、节点列表与启用状态。
func (s *Services) updateChain(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, fail(400, "bad id"))
		return
	}
	var payload chainPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, fail(400, "请求体格式错误"))
		return
	}
	ids, err := s.validateChainNodes(payload.NodeIDs)
	if err != nil {
		c.JSON(http.StatusBadRequest, fail(400, err.Error()))
		return
	}
	if err := s.Store.UpdateChain(id, payload.Name, ids, payload.Enabled); err != nil {
		c.JSON(http.StatusInternalServerError, fail(1, err.Error()))
		return
	}
	chain, err := s.Store.GetChain(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, fail(1, err.Error()))
		return
	}
	if chain == nil {
		c.JSON(http.StatusNotFound, fail(404, "链路不存在"))
		return
	}
	s.Bus.Info(fmt.Sprintf("updated proxy chain %q", chain.Name))
	c.JSON(http.StatusOK, ok(chain))
}

// deleteChain 删除指定链路。删除前如该链为当前 chain 策略依赖，选择会自然跳过。
func (s *Services) deleteChain(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, fail(400, "bad id"))
		return
	}
	if err := s.Store.DeleteChain(id); err != nil {
		c.JSON(http.StatusNotFound, fail(404, err.Error()))
		return
	}
	s.Bus.Info(fmt.Sprintf("deleted proxy chain id=%d", id))
	c.JSON(http.StatusOK, ok(nil))
}

// validateChainNodes 校验链路节点列表：全部存在、去重保序。
// 节点允许处于 dead 状态（链仅在全部节点存活时可用，编辑时池可能尚未刷新）。
func (s *Services) validateChainNodes(ids []int64) ([]int64, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("链路至少需要一个节点")
	}
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return nil, fmt.Errorf("非法节点 ID %d", id)
		}
		if _, ok := seen[id]; ok {
			continue
		}
		if s.Pool.Get(id) == nil {
			return nil, fmt.Errorf("节点 %d 不存在", id)
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out, nil
}
