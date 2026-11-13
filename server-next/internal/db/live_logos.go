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
