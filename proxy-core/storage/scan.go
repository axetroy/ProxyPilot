package storage

import (
	"database/sql"
	"time"

	"github.com/axetroy/ProxyPilot/proxy-core/model"
)

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

type scanner interface {
	Scan(dest ...any) error
}

func scanNode(sc scanner) (*model.ProxyNode, error) {
	var n model.ProxyNode
	var lastCheck, createdAt, updatedAt sql.NullTime
	var subscriptionID sql.NullInt64
	err := sc.Scan(
		&n.ID, &n.Host, &n.Port, &n.Protocol, &n.Username, &n.Password,
		&n.Latency, &n.Score, &n.Status,
		&n.Country, &n.Province, &n.City,
		&n.SuccessCount, &n.FailCount,
		&lastCheck, &createdAt, &updatedAt, &subscriptionID,
	)
	if err != nil {
		return nil, err
	}
	if lastCheck.Valid {
		n.LastCheck = lastCheck.Time
	}
	if createdAt.Valid {
		n.CreatedAt = createdAt.Time
	}
	if updatedAt.Valid {
		n.UpdatedAt = updatedAt.Time
	}
	if subscriptionID.Valid {
		n.SubscriptionID = subscriptionID.Int64
	}
	return &n, nil
}