package db

import (
	"context"
	"database/sql"
	"time"
)

// SaveSettings stores several settings in one transaction, so a form saved together is never
// half saved. An empty value removes the setting.
func (d *DB) SaveSettings(ctx context.Context, values map[string]string, now time.Time) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		for key, value := range values {
			var err error
			if value == "" {
				_, err = tx.Exec(`DELETE FROM settings WHERE key = ?`, key)
			} else {
				_, err = tx.Exec(`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
					ON CONFLICT (key) DO UPDATE SET value = excluded.value, revision = revision + 1, updated_at = excluded.updated_at`,
					key, value, now.UnixMilli())
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
}
