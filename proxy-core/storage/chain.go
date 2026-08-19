package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/axetroy/ProxyPilot/proxy-core/model"
)

const chainColumns = `id, name, node_ids, enabled, created_at, updated_at, last_checked_at, last_ok, last_latency, last_error, consecutive_failures, auto_disabled`

func scanChain(sc scanner) (*model.ProxyChain, error) {
	var c model.ProxyChain
	var nodeIDs string
	var enabled int
	var createdAt, updatedAt, lastCheckedAt sql.NullTime
	var lastOK, autoDisabled int
	if err := sc.Scan(&c.ID, &c.Name, &nodeIDs, &enabled, &createdAt, &updatedAt,
		&lastCheckedAt, &lastOK, &c.LastLatency, &c.LastError, &c.ConsecutiveFailures, &autoDisabled); err != nil {
		return nil, err
	}
	c.Enabled = enabled == 1
	if nodeIDs != "" {
		if err := json.Unmarshal([]byte(nodeIDs), &c.NodeIDs); err != nil {
			// 数据损坏时按空链处理，不阻断列表读取
			c.NodeIDs = nil
		}
	}
	if createdAt.Valid {
		c.CreatedAt = createdAt.Time
	}
	if updatedAt.Valid {
		c.UpdatedAt = updatedAt.Time
	}
	if lastCheckedAt.Valid {
		t := lastCheckedAt.Time
		c.LastCheckedAt = &t
	}
	c.LastOK = lastOK == 1
	c.AutoDisabled = autoDisabled == 1
	return &c, nil
}

// ListChains 返回全部代理链路（按创建时间升序）。
func (s *Store) ListChains() ([]model.ProxyChain, error) {
	rows, err := s.db.Query(`SELECT ` + chainColumns + ` FROM proxy_chains ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out = make([]model.ProxyChain, 0)
	for rows.Next() {
		c, err := scanChain(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// GetChain 返回指定链路，不存在时返回 (nil, nil)。
func (s *Store) GetChain(id int64) (*model.ProxyChain, error) {
	row := s.db.QueryRow(`SELECT `+chainColumns+` FROM proxy_chains WHERE id = ?`, id)
	c, err := scanChain(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return c, nil
}

// CreateChain 新建代理链路并返回含 ID 的完整记录。
func (s *Store) CreateChain(name string, nodeIDs []int64) (*model.ProxyChain, error) {
	now := time.Now()
	raw, err := json.Marshal(nodeIDs)
	if err != nil {
		return nil, err
	}
	res, err := s.db.Exec(
		`INSERT INTO proxy_chains (name, node_ids, enabled, created_at, updated_at) VALUES (?, ?, 0, ?, ?)`,
		name, string(raw), now, now,
	)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetChain(id)
}

// UpdateChain 更新链路的名称、节点列表与启用状态；字段为空时跳过对应更新。
func (s *Store) UpdateChain(id int64, name string, nodeIDs []int64, enabled *bool) error {
	now := time.Now()
	if name != "" {
		if _, err := s.db.Exec(`UPDATE proxy_chains SET name = ?, updated_at = ? WHERE id = ?`, name, now, id); err != nil {
			return err
		}
	}
	if nodeIDs != nil {
		raw, err := json.Marshal(nodeIDs)
		if err != nil {
			return err
		}
		if _, err := s.db.Exec(`UPDATE proxy_chains SET node_ids = ?, updated_at = ? WHERE id = ?`, string(raw), now, id); err != nil {
			return err
		}
	}
	if enabled != nil {
		v := 0
		if *enabled {
			v = 1
		}
		// 手动启用时重置健康状态：清掉自动停用标记与连续失败计数，
		// 避免链路恢复后沿用旧统计立即再次被停用。
		if _, err := s.db.Exec(
			`UPDATE proxy_chains SET enabled = ?, auto_disabled = 0, consecutive_failures = 0, updated_at = ? WHERE id = ?`,
			v, now, id); err != nil {
			return err
		}
	}
	return nil
}

// DeleteChain 删除指定链路。
func (s *Store) DeleteChain(id int64) error {
	res, err := s.db.Exec(`DELETE FROM proxy_chains WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("chain %d not found", id)
	}
	return nil
}

// SetChainEnabled 开关指定链路（启用/停用）。
func (s *Store) SetChainEnabled(id int64, enabled bool) error {
	v := 0
	if enabled {
		v = 1
	}
	// 手动启用时同时重置健康状态（见 UpdateChain 说明）。
	_, err := s.db.Exec(
		`UPDATE proxy_chains SET enabled = ?, auto_disabled = 0, consecutive_failures = 0, updated_at = ? WHERE id = ?`,
		v, time.Now(), id)
	return err
}

// RecordChainCheck 记录一次链路检测的展示结果（手动测试或单次探测），
// 只更新最近检测状态，不碰连续失败计数——连续失败计数专属于自动健康管理的停用判定。
func (s *Store) RecordChainCheck(id int64, ok bool, latency int64, errMsg string) error {
	lastOK := 0
	if ok {
		lastOK = 1
	}
	_, err := s.db.Exec(
		`UPDATE proxy_chains SET last_checked_at = ?, last_ok = ?, last_latency = ?, last_error = ? WHERE id = ?`,
		time.Now(), lastOK, latency, errMsg, id)
	return err
}

// UpdateChainHealth 记录一次链路健康检测的结果。
// ok 为 true 时清空失败原因与连续失败计数；失败时写入错误并累加连续失败次数。
func (s *Store) UpdateChainHealth(id int64, ok bool, latency int64, errMsg string, consecutive int) error {
	lastOK := 0
	if ok {
		lastOK = 1
	}
	_, err := s.db.Exec(
		`UPDATE proxy_chains SET last_checked_at = ?, last_ok = ?, last_latency = ?, last_error = ?, consecutive_failures = ? WHERE id = ?`,
		time.Now(), lastOK, latency, errMsg, consecutive, id)
	return err
}

// SetChainAutoDisabled 自动停用指定链路：标记为「被健康管理停用」并清空连续失败计数，
// 保留最近一次失败原因（last_error）供界面展示停用原因。
func (s *Store) SetChainAutoDisabled(id int64) error {
	_, err := s.db.Exec(
		`UPDATE proxy_chains SET enabled = 0, auto_disabled = 1, consecutive_failures = 0, updated_at = ? WHERE id = ?`,
		time.Now(), id)
	return err
}
