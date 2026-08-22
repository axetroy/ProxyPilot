package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/axetroy/ProxyPilot/proxy-core/model"
)

type Store struct {
	db   *sql.DB
	Path string // 数据库文件路径（内存库为 ":memory:"）
}

func New(path string) (*Store, error) {
	// 数据库由 Electron 放在用户数据目录（appdata）下，目录可能尚不存在，
	// 这里自动创建，避免打开 SQLite 时报 "unable to open database file"。
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("create db dir: %w", err)
		}
	}
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
	s := &Store{db: db, Path: path}
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
			country TEXT NOT NULL DEFAULT '',
			province TEXT NOT NULL DEFAULT '',
			city TEXT NOT NULL DEFAULT '',
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
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL DEFAULT '',
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS proxy_chains (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			node_ids TEXT NOT NULL DEFAULT '[]',
			enabled INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			last_checked_at DATETIME,
			last_ok INTEGER NOT NULL DEFAULT 0,
			last_latency INTEGER NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL DEFAULT '',
			consecutive_failures INTEGER NOT NULL DEFAULT 0,
			auto_disabled INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_check_history_proxy ON check_history(proxy_id, id DESC)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	// 已有数据库升级：为 proxy_nodes 补充地区列（CREATE TABLE IF NOT EXISTS 不会改动旧表）。
	// SQLite 不支持 ADD COLUMN IF NOT EXISTS，需先查 PRAGMA table_info 判断列是否已存在。
	if err := s.migrateProxyNodesColumns(); err != nil {
		return err
	}
	// 已有数据库升级：为 proxy_chains 补充健康检测列（链路自动健康管理）。
	return s.migrateProxyChainsColumns()
}

// migrateProxyChainsColumns 为旧版 proxy_chains 表补齐健康检测列。
func (s *Store) migrateProxyChainsColumns() error {
	cols, err := s.tableColumns("proxy_chains")
	if err != nil {
		return err
	}
	has := func(name string) bool {
		_, ok := cols[name]
		return ok
	}
	for _, c := range []string{"last_checked_at", "last_ok", "last_latency", "last_error", "consecutive_failures", "auto_disabled"} {
		if has(c) {
			continue
		}
		// 迁移后旧行按「从未检测」处理：时间列可为空，其余列取默认值。
		stmt := fmt.Sprintf(`ALTER TABLE proxy_chains ADD COLUMN %s `, c)
		switch c {
		case "last_checked_at":
			stmt += `DATETIME`
		case "last_error":
			stmt += `TEXT NOT NULL DEFAULT ''`
		default:
			stmt += `INTEGER NOT NULL DEFAULT 0`
		}
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

// migrateProxyNodesColumns 为旧版 proxy_nodes 表补齐 GeoIP 地区列。
func (s *Store) migrateProxyNodesColumns() error {
	cols, err := s.tableColumns("proxy_nodes")
	if err != nil {
		return err
	}
	has := func(name string) bool {
		_, ok := cols[name]
		return ok
	}
	for _, c := range []string{"country", "province", "city"} {
		if has(c) {
			continue
		}
		if _, err := s.db.Exec(fmt.Sprintf(
			`ALTER TABLE proxy_nodes ADD COLUMN %s TEXT NOT NULL DEFAULT ''`, c)); err != nil {
			return err
		}
	}
	return nil
}

// tableColumns 返回指定表的所有列名（用于迁移判断列是否已存在）。
func (s *Store) tableColumns(table string) (map[string]struct{}, error) {
	rows, err := s.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string]struct{})
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt *string
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		out[name] = struct{}{}
	}
	return out, rows.Err()
}

// ---------- settings ----------

// GetSetting 读取持久化的配置项，不存在时返回空字符串。
func (s *Store) GetSetting(key string) (string, error) {
	var value string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return value, nil
}

// SetSetting 写入或更新配置项。
func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings (key, value, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		key, value, time.Now().UTC())
	return err
}

// DeleteSetting 删除指定配置项（用于清理迁移后遗留的旧 key）。
func (s *Store) DeleteSetting(key string) error {
	_, err := s.db.Exec(`DELETE FROM settings WHERE key = ?`, key)
	return err
}

// ---------- proxy nodes ----------

func (s *Store) UpsertNode(n *model.ProxyNode) error {
	res, err := s.db.Exec(`INSERT INTO proxy_nodes
		(host, port, protocol, username, password, latency, score, status, country, province, city, success_count, fail_count, last_check, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(host, port, protocol) DO UPDATE SET
			username=excluded.username, password=excluded.password,
			latency=excluded.latency, score=excluded.score, status=excluded.status,
			country=excluded.country, province=excluded.province, city=excluded.city,
			success_count=excluded.success_count, fail_count=excluded.fail_count,
			last_check=excluded.last_check, updated_at=excluded.updated_at`,
		n.Host, n.Port, string(n.Protocol), n.Username, n.Password,
		n.Latency, n.Score, string(n.Status),
		n.Country, n.Province, n.City,
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
		(host, port, protocol, username, password, latency, score, status, country, province, city, success_count, fail_count, last_check, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(host, port, protocol) DO NOTHING`,
		n.Host, n.Port, string(n.Protocol), n.Username, n.Password,
		n.Latency, n.Score, string(n.Status),
		n.Country, n.Province, n.City,
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
		// 节点已存在（host:port:protocol 相同）：更新凭据，避免订阅密码变更不生效。
		if err := s.updateNodeCredentials(n); err != nil {
			return false, err
		}
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

// updateNodeCredentials 节点已存在（key 冲突）时更新其凭据与更新时间，
// 保证订阅源密码变更后网关能使用新凭据。
func (s *Store) updateNodeCredentials(n *model.ProxyNode) error {
	_, err := s.db.Exec(`UPDATE proxy_nodes SET username=?, password=?, updated_at=? WHERE host=? AND port=? AND protocol=?`,
		n.Username, n.Password, time.Now().UTC(), n.Host, n.Port, string(n.Protocol))
	return err
}

// ListNodesBySubscription 返回订阅关联的所有节点。
func (s *Store) ListNodesBySubscription(subscriptionID int64) ([]*model.ProxyNode, error) {
	rows, err := s.db.Query(`SELECT p.id, p.host, p.port, p.protocol, p.username, p.password,
		p.latency, p.score, p.status, p.country, p.province, p.city, p.success_count, p.fail_count, p.last_check, p.created_at, p.updated_at,
		sn.subscription_id
		FROM proxy_nodes p
		JOIN subscription_nodes sn ON sn.proxy_id = p.id
		WHERE sn.subscription_id = ?`, subscriptionID)
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

// DetachNodeFromSubscription 解除节点与订阅的关联。
func (s *Store) DetachNodeFromSubscription(proxyID, subscriptionID int64) error {
	_, err := s.db.Exec(`DELETE FROM subscription_nodes WHERE proxy_id=? AND subscription_id=?`, proxyID, subscriptionID)
	return err
}

// CountSubscriptionRefs 返回节点被多少个订阅引用。
func (s *Store) CountSubscriptionRefs(proxyID int64) (int, error) {
	var c int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM subscription_nodes WHERE proxy_id=?`, proxyID).Scan(&c)
	return c, err
}

// BatchDetachNodesFromSubscription 批量解除多个节点与订阅的关联。
// 单条 SQL 完成，避免循环调用 DetachNodeFromSubscription 产生大量往返。
func (s *Store) BatchDetachNodesFromSubscription(proxyIDs []int64, subscriptionID int64) error {
	if len(proxyIDs) == 0 {
		return nil
	}
	// 构建 IN 子句占位符
	placeholders := make([]string, len(proxyIDs))
	args := make([]any, len(proxyIDs)+1)
	for i, id := range proxyIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	args[len(proxyIDs)] = subscriptionID
	query := fmt.Sprintf(`DELETE FROM subscription_nodes WHERE proxy_id IN (%s) AND subscription_id=?`, strings.Join(placeholders, ","))
	_, err := s.db.Exec(query, args...)
	return err
}

// BatchCountSubscriptionRefs 批量查询多个节点被引用的订阅数。
// 单次查询返回 map[proxyID]count，避免循环调用 CountSubscriptionRefs。
func (s *Store) BatchCountSubscriptionRefs(proxyIDs []int64) (map[int64]int, error) {
	if len(proxyIDs) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(proxyIDs))
	args := make([]any, len(proxyIDs))
	for i, id := range proxyIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	query := fmt.Sprintf(`SELECT proxy_id, COUNT(*) FROM subscription_nodes WHERE proxy_id IN (%s) GROUP BY proxy_id`, strings.Join(placeholders, ","))
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make(map[int64]int, len(proxyIDs))
	for rows.Next() {
		var pid, cnt int64
		if err := rows.Scan(&pid, &cnt); err != nil {
			return nil, err
		}
		result[pid] = int(cnt)
	}
	return result, rows.Err()
}

// UpdateNodeCheck 在检测完成后更新节点状态与评分，同时回填 GeoIP 地区字段。
// country/province/city 为空表表示未命中离线定位，保持原有值置空。
func (s *Store) UpdateNodeCheck(id int64, status model.ProxyStatus, latency, score int64, ok bool, country, province, city string) error {
	sqlStmt := `UPDATE proxy_nodes SET
		status=?, latency=?, score=?,
		country=?, province=?, city=?,
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
		string(status), latency, score, country, province, city, successInc, failInc, nullTime(now), now, id)
	return err
}

// RecordCheckResult 在一次事务内完成节点状态更新与检测历史写入，
// 避免单节点检测产生两次独立提交（检测高频并发时减少 SQLite 写放大）。
func (s *Store) RecordCheckResult(id int64, status model.ProxyStatus, latency, score int64, ok bool,
	country, province, city string, historyErr string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	sqlStmt := `UPDATE proxy_nodes SET
		status=?, latency=?, score=?,
		country=?, province=?, city=?,
		success_count=success_count+?, fail_count=fail_count+?,
		last_check=?, updated_at=? WHERE id=?`
	successInc, failInc := 0, 0
	if ok {
		successInc = 1
	} else {
		failInc = 1
	}
	now := time.Now().UTC()
	if _, err := tx.Exec(sqlStmt,
		string(status), latency, score, country, province, city, successInc, failInc, nullTime(now), now, id); err != nil {
		return err
	}

	historyOK := 0
	if ok {
		historyOK = 1
	}
	if _, err := tx.Exec(`INSERT INTO check_history (proxy_id, success, latency, error, created_at)
		VALUES (?, ?, ?, ?, ?)`, id, historyOK, latency, historyErr, now); err != nil {
		return err
	}
	return tx.Commit()
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
		latency, score, status, country, province, city, success_count, fail_count, last_check, created_at, updated_at,
		(SELECT subscription_id FROM subscription_nodes WHERE proxy_id = proxy_nodes.id LIMIT 1)
		FROM proxy_nodes WHERE id=?`, id)
	return scanNode(row)
}

func (s *Store) ListNode() ([]*model.ProxyNode, error) {
	rows, err := s.db.Query(`SELECT id, host, port, protocol, username, password,
		latency, score, status, country, province, city, success_count, fail_count, last_check, created_at, updated_at,
		(SELECT subscription_id FROM subscription_nodes WHERE proxy_id = proxy_nodes.id LIMIT 1)
		FROM proxy_nodes ORDER BY CASE WHEN status='alive' THEN 0 ELSE 1 END,
		score DESC, latency ASC, id ASC, host ASC`)
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
		latency, score, status, country, province, city, success_count, fail_count, last_check, created_at, updated_at,
		(SELECT subscription_id FROM subscription_nodes WHERE proxy_id = proxy_nodes.id LIMIT 1)
		FROM proxy_nodes WHERE status=? ORDER BY score DESC, latency ASC, id ASC, host ASC`, string(status))
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

// GetSubscription 按 ID 查询单个订阅（返回 nil 表示不存在）。
func (s *Store) GetSubscription(id int64) (*model.Subscription, error) {
	row := s.db.QueryRow(`SELECT s.id, s.name, s.url, s.interval, s.enabled, s.last_fetch, s.created_at, 
		(SELECT COUNT(*) FROM subscription_nodes sn WHERE sn.subscription_id = s.id) as proxy_count
		FROM subscriptions s WHERE s.id = ?`, id)
	var sub model.Subscription
	var enabled int
	var lastFetch, createdAt sql.NullTime
	var proxyCount int
	if err := row.Scan(&sub.ID, &sub.Name, &sub.URL, &sub.Interval, &enabled, &lastFetch, &createdAt, &proxyCount); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
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
	return &sub, nil
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

	// 先解除该订阅与节点的关联，再对每个节点判断是否还被其他订阅引用；
	// 仅删除不再被任何订阅引用的节点，避免共享节点被连带删除。
	if _, err := s.db.Exec(`DELETE FROM subscription_nodes WHERE subscription_id=?`, id); err != nil {
		return nil, err
	}
	var removed []int64
	if len(proxyIDs) > 0 {
		for _, proxyID := range proxyIDs {
			refs, err := s.CountSubscriptionRefs(proxyID)
			if err != nil {
				return nil, err
			}
			if refs > 0 {
				continue
			}
			if _, err := s.db.Exec(`DELETE FROM check_history WHERE proxy_id=?`, proxyID); err != nil {
				return nil, err
			}
			if _, err := s.db.Exec(`DELETE FROM proxy_nodes WHERE id=?`, proxyID); err != nil {
				return nil, err
			}
			removed = append(removed, proxyID)
		}
	}
	_, err = s.db.Exec(`DELETE FROM subscriptions WHERE id=?`, id)
	return removed, err
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

// ---------- 数据库维护（手动瘦身） ----------

// HistoryCount 返回检测历史的总条数。
func (s *Store) HistoryCount() (int64, error) {
	var c int64
	err := s.db.QueryRow(`SELECT COUNT(*) FROM check_history`).Scan(&c)
	return c, err
}

// HistoryCountOlderThan 返回 created_at 早于 before 的检测历史条数（即当前可清理的条数）。
func (s *Store) HistoryCountOlderThan(before time.Time) (int64, error) {
	var c int64
	err := s.db.QueryRow(`SELECT COUNT(*) FROM check_history WHERE created_at < ?`, before.UTC()).Scan(&c)
	return c, err
}

// PurgeHistoryBefore 删除 created_at 早于 before 的检测历史，返回删除条数。
func (s *Store) PurgeHistoryBefore(before time.Time) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM check_history WHERE created_at < ?`, before.UTC())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DBFileSize 返回数据库文件大小（字节）；内存库或文件不存在时返回 0。
func (s *Store) DBFileSize() int64 {
	if s.Path == "" || s.Path == ":memory:" {
		return 0
	}
	info, err := os.Stat(s.Path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// Compact 收缩数据库物理文件：先 WAL checkpoint 截断 WAL，再 VACUUM 重写主库，
// 让已删除的行真正从文件中移除。调用前应保证没有其它长时间事务在运行。
func (s *Store) Compact() error {
	// WAL 模式下先 checkpoint，否则 VACUUM 无法把已删除页从主库中彻底移除。
	rows, err := s.db.Query(`PRAGMA wal_checkpoint(TRUNCATE)`)
	if err == nil {
		for rows.Next() {
			// 读取结果行确保 checkpoint 完整执行（busy, log, checkpointed）
		}
		_ = rows.Close()
	}
	_, err = s.db.Exec(`VACUUM`)
	return err
}
