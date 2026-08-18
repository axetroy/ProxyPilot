package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/axetroy/ProxyPilot/proxy-core/model"
)

const chainColumns = `id, name, node_ids, enabled, created_at, updated_at`

func scanChain(sc scanner) (*model.ProxyChain, error) {
	var c model.ProxyChain
	var nodeIDs string
	var enabled int
	var createdAt, updatedAt sql.NullTime
	if err := sc.Scan(&c.ID, &c.Name, &nodeIDs, &enabled, &createdAt, &updatedAt); err != nil {
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
		if _, err := s.db.Exec(`UPDATE proxy_chains SET enabled = ?, updated_at = ? WHERE id = ?`, v, now, id); err != nil {
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
	_, err := s.db.Exec(`UPDATE proxy_chains SET enabled = ?, updated_at = ? WHERE id = ?`, v, time.Now(), id)
	return err
}
