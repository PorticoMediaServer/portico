package db

import (
	"context"
	"database/sql"
	"time"
)

// CachedPrepared pages ready, reusable copies.
func (d *DB) CachedPrepared(ctx context.Context, after int64) ([]Prepared, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT `+preparedColumns+` FROM prepared_media
		WHERE state='ready' AND id > ? ORDER BY id LIMIT 100`, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Prepared
	for rows.Next() {
		p, err := scanPrepared(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// RemoveCachedPrepared leaves a copy alone if a fetch or request has used it since the read.
func (d *DB) RemoveCachedPrepared(ctx context.Context, id int64, lastUsed time.Time) (removed bool, err error) {
	err = d.write(ctx, User, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM prepared_media WHERE id=? AND state='ready' AND last_used_at = ?`, id, lastUsed.UnixMilli())
		if err == nil {
			n, _ := res.RowsAffected()
			removed = n != 0
		}
		return err
	})
	return removed, err
}
