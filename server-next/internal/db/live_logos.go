package db

import (
	"context"
	"database/sql"
	"time"
)

// SourceLogoHashes keeps the URL that actually supplied a held logo. Importing
// new channel facts leaves that pair together until its replacement succeeds.
func (d *DB) SourceLogoHashes(ctx context.Context, sourceID int64) (map[string]string, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT logo_url,logo_hash FROM channels WHERE source_id=? AND logo_url IS NOT NULL AND logo_hash IS NOT NULL`, sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var address, hash string
		if err := rows.Scan(&address, &hash); err != nil {
			return nil, err
		}
		out[address] = hash
	}
	return out, rows.Err()
}

func (d *DB) SourceChannelLogo(ctx context.Context, sourceID int64, key, address, hash string, now time.Time) error {
	return d.write(ctx, Background, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE channels SET logo_url=?,logo_hash=?,updated_at=? WHERE source_id=? AND source_key=?`, address, hash, now.UnixMilli(), sourceID, key)
		return err
	})
}

// LiveSourceLogoTarget remains server-only: provider URLs are never sent to an app.
type LiveSourceLogoTarget struct{ Key, Address, Hash string }

func (d *DB) LiveSourceLogoTargets(ctx context.Context, sourceID int64) ([]LiveSourceLogoTarget, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT source_key,logo_url,ifnull(logo_hash,'') FROM channels
	 WHERE source_id=? AND state<>? AND missing_since_at IS NULL AND logo_url IS NOT NULL ORDER BY id`, sourceID, ChannelNotIn)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LiveSourceLogoTarget{}
	for rows.Next() {
		var row LiveSourceLogoTarget
		if err := rows.Scan(&row.Key, &row.Address, &row.Hash); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
