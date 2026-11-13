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

// SetAccountStreamLimits changes only the named accounts, atomically in the settings row.
// A null entry removes that override. JSON merge-patch preserves the other accounts and all
// server-wide limits.
func (d *DB) SetAccountStreamLimits(ctx context.Context, people map[string]*int, now time.Time) (string, error) {
	if len(people) == 0 {
		value, _, err := d.Setting(ctx, StreamLimitsSetting)
		if value == "" {
			value = "{}"
		}
		return value, err
	}
	patch, err := json.Marshal(map[string]any{"people": people})
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
