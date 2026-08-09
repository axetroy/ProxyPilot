package storage

import (
	"database/sql"
	"time"

	_ "modernc.org/sqlite"

	"github.com/axetroy/ProxyPilot/proxy-core/model"
)

type Store struct {
	db *sql.DB
}

func New(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// SQLite 只允许一个写者，多个并发连接同时写会报 "database is busy"。
	// 限制为单连接 + busy_timeout，让所有读写串行化并等待锁释放。
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		return nil, err
	}
	// WAL 模式下读不阻塞写，进一步降低锁冲突概率。
	if _, err := db.Exec(`PRAGMA journal_mode = WAL`); err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS proxy_nodes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			host TEXT NOT NULL,
			port INTEGER NOT NULL,
			protocol TEXT NOT NULL,
			username TEXT NOT NULL DEFAULT '',
			password TEXT NOT NULL DEFAULT '',
			latency INTEGER NOT NULL DEFAULT 0,
			score INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'new',
			success_count INTEGER NOT NULL DEFAULT 0,
			fail_count INTEGER NOT NULL DEFAULT 0,
			last_check DATETIME,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			UNIQUE(host, port, protocol)
		)`,
		`CREATE TABLE IF NOT EXISTS subscriptions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			url TEXT NOT NULL,
			interval INTEGER NOT NULL DEFAULT 3600,
			enabled INTEGER NOT NULL DEFAULT 1,
			last_fetch DATETIME,
			created_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS check_history (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			proxy_id INTEGER NOT NULL,
			success INTEGER NOT NULL DEFAULT 0,
			latency INTEGER NOT NULL DEFAULT 0,
			error TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS subscription_nodes (
			subscription_id INTEGER NOT NULL,
			proxy_id INTEGER NOT NULL,
			PRIMARY KEY (subscription_id, proxy_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_check_history_proxy ON check_history(proxy_id, id DESC)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

// ---------- proxy nodes ----------

func (s *Store) UpsertNode(n *model.ProxyNode) error {
	res, err := s.db.Exec(`INSERT INTO proxy_nodes
		(host, port, protocol, username, password, latency, score, status, success_count, fail_count, last_check, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(host, port, protocol) DO UPDATE SET
			username=excluded.username, password=excluded.password,
			latency=excluded.latency, score=excluded.score, status=excluded.status,
			success_count=excluded.success_count, fail_count=excluded.fail_count,
			last_check=excluded.last_check, updated_at=excluded.updated_at`,
		n.Host, n.Port, string(n.Protocol), n.Username, n.Password,
		n.Latency, n.Score, string(n.Status),
		n.SuccessCount, n.FailCount,
		nullTime(n.LastCheck), n.CreatedAt, n.UpdatedAt)
	if err != nil {
		return err
	}
	if n.ID == 0 {
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		n.ID = id
	}
	if n.SubscriptionID > 0 {
		if err := s.AttachNodeToSubscription(n.ID, n.SubscriptionID); err != nil {
			return err
		}
	}
	return nil
}

// SaveNode inserts a brand new node (addition path), dedup by key if already exists.
func (s *Store) SaveNode(n *model.ProxyNode) (bool, error) {
	now := time.Now().UTC()
	if n.CreatedAt.IsZero() {
		n.CreatedAt = now
	}
	if n.UpdatedAt.IsZero() {
		n.UpdatedAt = now
	}
	res, err := s.db.Exec(`INSERT INTO proxy_nodes
		(host, port, protocol, username, password, latency, score, status, success_count, fail_count, last_check, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(host, port, protocol) DO NOTHING`,
		n.Host, n.Port, string(n.Protocol), n.Username, n.Password,
		n.Latency, n.Score, string(n.Status),
		n.SuccessCount, n.FailCount,
		nullTime(n.LastCheck), n.CreatedAt, n.UpdatedAt)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected == 0 {
		if n.SubscriptionID > 0 {
			var existingID int64
			err := s.db.QueryRow(`SELECT id FROM proxy_nodes WHERE host=? AND port=? AND protocol=?`, n.Host, n.Port, string(n.Protocol)).Scan(&existingID)
			if err != nil {
				if err == sql.ErrNoRows {
					return false, nil
				}
				return false, err
			}
			n.ID = existingID
			if err := s.AttachNodeToSubscription(n.ID, n.SubscriptionID); err != nil {
				return false, err
			}
		}
		return false, nil
	}
	id, err := res.LastInsertId()
	if err != nil {
		return false, err
	}
	n.ID = id
	if n.SubscriptionID > 0 {
		if err := s.AttachNodeToSubscription(n.ID, n.SubscriptionID); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (s *Store) AttachNodeToSubscription(proxyID, subscriptionID int64) error {
	if proxyID == 0 || subscriptionID == 0 {
		return nil
	}
	_, err := s.db.Exec(`INSERT OR IGNORE INTO subscription_nodes (subscription_id, proxy_id) VALUES (?, ?)`, subscriptionID, proxyID)
	return err
}

func (s *Store) UpdateNodeCheck(id int64, status model.ProxyStatus, latency, score int64, ok bool) error {
	sqlStmt := `UPDATE proxy_nodes SET
		status=?, latency=?, score=?,
		success_count=success_count+?, fail_count=fail_count+?,
		last_check=?, updated_at=? WHERE id=?`
	successInc, failInc := 0, 0
	if ok {
		successInc = 1
	} else {
		failInc = 1
	}
	now := time.Now().UTC()
	_, err := s.db.Exec(sqlStmt,
		string(status), latency, score, successInc, failInc, nullTime(now), now, id)
	return err
}

func (s *Store) DeleteNode(id int64) error {
	if _, err := s.db.Exec(`DELETE FROM proxy_nodes WHERE id=?`, id); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM check_history WHERE proxy_id=?`, id)
	return err
}

func (s *Store) GetNode(id int64) (*model.ProxyNode, error) {
	row := s.db.QueryRow(`SELECT id, host, port, protocol, username, password,
		latency, score, status, success_count, fail_count, last_check, created_at, updated_at
		FROM proxy_nodes WHERE id=?`, id)
	return scanNode(row)
}

func (s *Store) ListNode() ([]*model.ProxyNode, error) {
	rows, err := s.db.Query(`SELECT id, host, port, protocol, username, password,
		latency, score, status, success_count, fail_count, last_check, created_at, updated_at
		FROM proxy_nodes ORDER BY score DESC, latency ASC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*model.ProxyNode
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) ListNodesByStatus(status model.ProxyStatus) ([]*model.ProxyNode, error) {
	rows, err := s.db.Query(`SELECT id, host, port, protocol, username, password,
		latency, score, status, success_count, fail_count, last_check, created_at, updated_at
		FROM proxy_nodes WHERE status=? ORDER BY score DESC`, string(status))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*model.ProxyNode
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) CountNodes() (int, error) {
	var c int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM proxy_nodes`).Scan(&c)
	return c, err
}

func (s *Store) CountNodesByStatus(status model.ProxyStatus) (int, error) {
	var c int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM proxy_nodes WHERE status=?`, string(status)).Scan(&c)
	return c, err
}

// ---------- subscriptions ----------

func (s *Store) UpsertSubscription(sub *model.Subscription) error {
	now := time.Now().UTC()
	if sub.CreatedAt.IsZero() {
		sub.CreatedAt = now
	}
	enabled := 0
	if sub.Enabled {
		enabled = 1
	}
	// ID == 0 表示新增：传 NULL 让 AUTOINCREMENT 分配新 id；
	// ID != 0 表示更新：传具体 id 触发 ON CONFLICT(id) 走 UPDATE。
	var idArg any
	if sub.ID == 0 {
		idArg = nil
	} else {
		idArg = sub.ID
	}
	res, err := s.db.Exec(`INSERT INTO subscriptions (id, name, url, interval, enabled, last_fetch, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, url=excluded.url,
			interval=excluded.interval, enabled=excluded.enabled,
			last_fetch=COALESCE(excluded.last_fetch, subscriptions.last_fetch)`,
		idArg, sub.Name, sub.URL, sub.Interval, enabled, nullTime(sub.LastFetch), sub.CreatedAt)
	if err != nil {
		return err
	}
	if sub.ID == 0 {
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		sub.ID = id
	}
	return nil
}

func (s *Store) ListSubscriptions() ([]*model.Subscription, error) {
	rows, err := s.db.Query(`SELECT s.id, s.name, s.url, s.interval, s.enabled, s.last_fetch, s.created_at, 
		(SELECT COUNT(*) FROM subscription_nodes sn WHERE sn.subscription_id = s.id) as proxy_count
		FROM subscriptions s ORDER BY s.id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*model.Subscription
	for rows.Next() {
		var sub model.Subscription
		var enabled int
		var lastFetch, createdAt sql.NullTime
		var proxyCount int
		if err := rows.Scan(&sub.ID, &sub.Name, &sub.URL, &sub.Interval, &enabled, &lastFetch, &createdAt, &proxyCount); err != nil {
			return nil, err
		}
		if lastFetch.Valid {
			sub.LastFetch = lastFetch.Time
		}
		if createdAt.Valid {
			sub.CreatedAt = createdAt.Time
		}
		sub.Enabled = enabled == 1
		sub.ProxyCount = proxyCount
		out = append(out, &sub)
	}
	return out, rows.Err()
}

func (s *Store) UpdateSubscriptionFetch(id int64, t time.Time) error {
	_, err := s.db.Exec(`UPDATE subscriptions SET last_fetch=? WHERE id=?`, t, id)
	return err
}

func (s *Store) DeleteSubscription(id int64) ([]int64, error) {
	var proxyIDs []int64
	rows, err := s.db.Query(`SELECT proxy_id FROM subscription_nodes WHERE subscription_id=?`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var proxyID int64
		if err := rows.Scan(&proxyID); err != nil {
			return nil, err
		}
		proxyIDs = append(proxyIDs, proxyID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(proxyIDs) > 0 {
		for _, proxyID := range proxyIDs {
			if _, err := s.db.Exec(`DELETE FROM check_history WHERE proxy_id=?`, proxyID); err != nil {
				return nil, err
			}
			if _, err := s.db.Exec(`DELETE FROM proxy_nodes WHERE id=?`, proxyID); err != nil {
				return nil, err
			}
		}
	}
	if _, err := s.db.Exec(`DELETE FROM subscription_nodes WHERE subscription_id=?`, id); err != nil {
		return nil, err
	}
	_, err = s.db.Exec(`DELETE FROM subscriptions WHERE id=?`, id)
	return proxyIDs, err
}

// ---------- check history ----------

func (s *Store) AddCheckHistory(h model.CheckHistory) error {
	now := time.Now().UTC()
	ok := 0
	if h.Success {
		ok = 1
	}
	_, err := s.db.Exec(`INSERT INTO check_history (proxy_id, success, latency, error, created_at)
		VALUES (?, ?, ?, ?, ?)`, h.ProxyID, ok, h.Latency, h.Error, now)
	return err
}

func (s *Store) RecentHistory(proxyID int64, limit int) ([]model.CheckHistory, error) {
	rows, err := s.db.Query(`SELECT id, proxy_id, success, latency, error, created_at
		FROM check_history WHERE proxy_id=? ORDER BY id DESC LIMIT ?`, proxyID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.CheckHistory
	for rows.Next() {
		var h model.CheckHistory
		var ok int
		if err := rows.Scan(&h.ID, &h.ProxyID, &ok, &h.Latency, &h.Error, &h.CreatedAt); err != nil {
			return nil, err
		}
		h.Success = ok == 1
		out = append(out, h)
	}
	return out, rows.Err()
}
