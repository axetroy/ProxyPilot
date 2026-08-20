package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// dbStatus 数据库状态信息，供前端「数据管理」展示。
type dbStatus struct {
	DBSize        int64 `json:"dbSize"`        // 数据库文件大小（字节）
	HistoryCount  int64 `json:"historyCount"`  // 检测历史总条数
	Purgeable     int64 `json:"purgeable"`     // 早于保留天数、可被清理的检测历史条数
	RetentionDays int   `json:"retentionDays"` // 检测历史保留天数
}

// compactResult 手动瘦身数据库的结果。
type compactResult struct {
	Deleted      int64 `json:"deleted"`      // 清理的检测历史条数
	SizeBefore   int64 `json:"sizeBefore"`   // 瘦身前数据库文件大小（字节）
	SizeAfter    int64 `json:"sizeAfter"`    // 瘦身后数据库文件大小（字节）
	HistoryCount int64 `json:"historyCount"` // 瘦身后检测历史剩余条数
}

// retentionCutoff 返回基于保留天数的清理截止时间。
func (s *Services) retentionCutoff() time.Time {
	days := s.Cfg.RuntimeSnapshot().HistoryRetentionDays
	if days <= 0 {
		days = 7
	}
	return time.Now().Add(-time.Duration(days) * 24 * time.Hour)
}

// dbStatus 返回当前数据库的状态（大小、检测历史条数与可清理条数）。
func (s *Services) dbStatus(c *gin.Context) {
	historyCount, err := s.Store.HistoryCount()
	if err != nil {
		c.JSON(http.StatusInternalServerError, fail(1, err.Error()))
		return
	}
	purgeable, err := s.Store.HistoryCountOlderThan(s.retentionCutoff())
	if err != nil {
		c.JSON(http.StatusInternalServerError, fail(1, err.Error()))
		return
	}
	c.JSON(http.StatusOK, ok(dbStatus{
		DBSize:        s.Store.DBFileSize(),
		HistoryCount:  historyCount,
		Purgeable:     purgeable,
		RetentionDays: s.Cfg.RuntimeSnapshot().HistoryRetentionDays,
	}))
}

// compactDb 手动瘦身数据库：删除早于保留天数的检测历史，并 VACUUM 收缩文件。
// 仅影响检测历史，节点 / 订阅 / 设置均不受影响。
func (s *Services) compactDb(c *gin.Context) {
	cutoff := s.retentionCutoff()
	sizeBefore := s.Store.DBFileSize()

	deleted, err := s.Store.PurgeHistoryBefore(cutoff)
	if err != nil {
		c.JSON(http.StatusInternalServerError, fail(1, err.Error()))
		return
	}
	if err := s.Store.Compact(); err != nil {
		c.JSON(http.StatusInternalServerError, fail(1, err.Error()))
		return
	}
	sizeAfter := s.Store.DBFileSize()
	historyCount, err := s.Store.HistoryCount()
	if err != nil {
		c.JSON(http.StatusInternalServerError, fail(1, err.Error()))
		return
	}
	s.Bus.Info(fmt.Sprintf("database compacted: deleted %d history rows, size %d -> %d bytes",
		deleted, sizeBefore, sizeAfter))
	c.JSON(http.StatusOK, ok(compactResult{
		Deleted:      deleted,
		SizeBefore:   sizeBefore,
		SizeAfter:    sizeAfter,
		HistoryCount: historyCount,
	}))
}
