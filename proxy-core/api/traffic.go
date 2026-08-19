package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// traffic 返回网关流量统计（本次启动累计）：总量 + 按节点/链路/直连分桶。
func (s *Services) traffic(c *gin.Context) {
	c.JSON(http.StatusOK, ok(s.Gateway.Traffic()))
}
