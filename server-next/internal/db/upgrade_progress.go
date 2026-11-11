package db

import (
	"context"
	"database/sql"
)

// UpgradeLeft is the oldest upgrade step whose background part is not finished, and how far it
// has got (the position its next batch starts after): the same fact the upgrade writes to the
// log. ok is false when no upgrade work is left. running is false when the work stopped on an
// error and waits for the next start (the log says why).
func (d *DB) UpgradeLeft(ctx context.Context) (version int, reached int64, running, ok bool, err error) {
	var v sql.NullInt64
	var r sql.NullInt64
	err = d.read.QueryRowContext(ctx, `SELECT version, background_after FROM schema_migrations WHERE background_after IS NOT NULL
		ORDER BY version LIMIT 1`).Scan(&v, &r)
	if err == sql.ErrNoRows {
		return 0, 0, false, false, nil
	}
	if err != nil || !v.Valid {
		return 0, 0, false, false, err
	}
	select {
	case <-d.upgradesDone:
	default:
		running = true
	}
	return int(v.Int64), r.Int64, running, true, nil
}
