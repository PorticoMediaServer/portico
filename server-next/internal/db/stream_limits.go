package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// StreamLimitsSetting is the existing settings row shared by the whole-server limit and
// accounts' overrides. An absent account inherits; zero means unlimited.
const StreamLimitsSetting = "playback.streams"

// PatchStreamLimits changes only the named fields and accounts, atomically in the settings
// row. A null account removes that override; other accounts and fields are preserved.
func (d *DB) PatchStreamLimits(ctx context.Context, fields map[string]any, now time.Time) (string, error) {
	if len(fields) == 0 {
		value, _, err := d.Setting(ctx, StreamLimitsSetting)
		if value == "" {
			value = "{}"
		}
		return value, err
	}
	patch, err := json.Marshal(fields)
	if err != nil {
		return "", err
	}
	var value string
	err = d.write(ctx, User, func(tx *sql.Tx) error {
		return tx.QueryRow(`INSERT INTO settings (key, value, updated_at) VALUES (?, json_patch('{}', ?), ?)
   ON CONFLICT (key) DO UPDATE SET value = json_patch(settings.value, ?), revision = settings.revision + 1, updated_at = excluded.updated_at
   RETURNING value`, StreamLimitsSetting, string(patch), now.UnixMilli(), string(patch)).Scan(&value)
	})
	return value, err
}
