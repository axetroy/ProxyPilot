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
	var lastCheck, createdAt, updatedAt, mitmAt, speedAt sql.NullTime
	var subscriptionID sql.NullInt64
	var mitmDetected int
	err := sc.Scan(
		&n.ID, &n.Host, &n.Port, &n.Protocol, &n.Username, &n.Password,
		&n.Latency, &n.Score, &n.Status,
		&n.Country, &n.Province, &n.City,
		&n.SuccessCount, &n.FailCount,
		&lastCheck,
		&mitmDetected, &mitmAt, &n.Speed, &speedAt,
		&createdAt, &updatedAt, &subscriptionID,
	)
	if err != nil {
		return nil, err
	}
	if lastCheck.Valid {
		n.LastCheck = lastCheck.Time
	}
	n.MitmDetected = mitmDetected != 0
	if mitmAt.Valid {
		n.MitmAt = mitmAt.Time
	}
	if speedAt.Valid {
		n.SpeedAt = speedAt.Time
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
