package api

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/axetroy/ProxyPilot/proxy-core/model"
	"github.com/axetroy/ProxyPilot/proxy-core/validator"
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

// testChain 测试指定链路：逐跳连接，测量每一跳的延迟与连通性。
// 目标地址取配置的检测目标（CheckTarget）的 host:port。
func (s *Services) testChain(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, fail(400, "bad id"))
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

	nodes := make([]*model.ProxyNode, 0, len(chain.NodeIDs))
	for _, nid := range chain.NodeIDs {
		n := s.Pool.Get(nid)
		if n == nil {
			c.JSON(http.StatusNotFound, fail(404, fmt.Sprintf("节点 %d 不存在", nid)))
			return
		}
		nodes = append(nodes, n)
	}
	if len(nodes) == 0 {
		c.JSON(http.StatusBadRequest, fail(400, "链路为空"))
		return
	}

	target, err := hostPortFromTarget(s.Cfg.CheckTarget)
	if err != nil {
		c.JSON(http.StatusBadRequest, fail(400, err.Error()))
		return
	}
	timeout := s.Cfg.CheckTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	result := validator.TestChain(nodes, target, timeout)
	c.JSON(http.StatusOK, ok(result))
}

// hostPortFromTarget 从检测目标（URL 或裸 host:port）解析出 host:port。
// URL 不带端口时按 scheme 推断默认端口（https=443，其他=80）。
func hostPortFromTarget(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("检测目标为空")
	}
	// 带 scheme 的 URL：按 URL 解析提取 host:port。
	// 注意不能先裸调 net.SplitHostPort：对 "https://example.com/path" 这种
	// 只有一个冒号的字符串，SplitHostPort 会把 https 当 host、//… 当 port 返回 nil error，
	// 导致整个 URL 被误当成 target 传入握手。
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" {
			return "", fmt.Errorf("无效的检测目标 %q", raw)
		}
		port := u.Port()
		if port == "" {
			if u.Scheme == "https" {
				port = "443"
			} else {
				port = "80"
			}
		}
		return net.JoinHostPort(u.Hostname(), port), nil
	}
	// 裸 host:port：校验端口为数字，避免非 host:port 形式被误判为合法目标。
	host, port, err := net.SplitHostPort(raw)
	if err != nil {
		return "", fmt.Errorf("无效的检测目标 %q", raw)
	}
	if _, err := strconv.Atoi(port); err != nil {
		return "", fmt.Errorf("无效的检测目标 %q：端口 %q 不是数字", raw, port)
	}
	return net.JoinHostPort(host, port), nil
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
