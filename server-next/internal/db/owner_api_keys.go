package db

import (
	"context"
	"strings"
	"time"
)

// OwnerAPIKeyRecord includes ended keys for the owner's history, without any hash or secret.
type OwnerAPIKeyRecord struct {
	Key       APIKey
	RevokedAt time.Time
}

// OwnerAPIKeyHistory keeps the existing live-key reader unchanged for its other callers.
func (d *DB) OwnerAPIKeyHistory(ctx context.Context, userID, before int64, limit int) ([]OwnerAPIKeyRecord, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT `+keyColumns+`, ifnull(revoked_at, 0) FROM api_keys
  WHERE user_id = ? AND kind = 'api_key' AND (? = 0 OR id < ?) ORDER BY id DESC LIMIT ?`, userID, before, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OwnerAPIKeyRecord
	for rows.Next() {
		var row OwnerAPIKeyRecord
		var scopes string
		var created, used, expires, revoked int64
		k := &row.Key
		if err := rows.Scan(&k.ID, &k.PublicID, &k.Kind, &k.Name, &scopes, &created, &used, &expires, &revoked); err != nil {
			return nil, err
		}
		k.Scopes, k.CreatedAt = strings.Fields(scopes), time.UnixMilli(created)
		if used != 0 {
			k.LastUsedAt = time.UnixMilli(used)
		}
		if expires != 0 {
			k.ExpiresAt = time.UnixMilli(expires)
		}
		if revoked != 0 {
			row.RevokedAt = time.UnixMilli(revoked)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
