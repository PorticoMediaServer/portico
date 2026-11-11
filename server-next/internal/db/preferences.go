package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// StoredPreference is one setting a profile saved. DeviceClass and Device say how widely it
// applies: both unset, everywhere; DeviceClass, that kind of device; Device, that device alone.
type StoredPreference struct {
	DeviceClass string
	Device      bool
	Key         string
	// Value is JSON.
	Value string
}

// StoredPreferences returns what a profile saved that applies on one device, each with how
// widely it applies: its settings for everywhere, for this kind of device, and for this
// device. deviceID is 0 for a caller with no device, such as a tool with a key.
func (d *DB) StoredPreferences(ctx context.Context, profileID int64, deviceClass string, deviceID int64) ([]StoredPreference, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT device_class, device_id IS NOT NULL, key, value FROM preferences
		WHERE profile_id = ? AND (device_id = ? OR device_id IS NULL AND device_class IN ('', ?)) AND value <> '`+clearedValue+`'`,
		profileID, deviceID, deviceClass)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StoredPreference
	for rows.Next() {
		var p StoredPreference
		if err := rows.Scan(&p.DeviceClass, &p.Device, &p.Key, &p.Value); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// clearedValue is what a cleared setting keeps instead of a value: the row stays so that it
// keeps the time it was cleared, and every read passes it over.
const clearedValue = "null"

// SetPreferences saves settings for a profile at one width: everywhere (deviceClass "" and
// deviceID 0), one kind of device, or one device. A key in clear goes back to whatever the
// next wider setting or the default says. madeAt is when the person made the change; a change
// to a setting that was changed later already, on another device, is passed over, so two
// devices that sync in either order end the same. The profile's other open apps are told.
func (d *DB) SetPreferences(ctx context.Context, profileID int64, deviceClass string, deviceID int64, set map[string]string, clear []string,
	madeAt time.Time) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		save := func(key, value string) error {
			_, err := tx.Exec(`INSERT INTO preferences (profile_id, device_class, device_id, key, value, updated_at) VALUES (?, ?, nullif(?, 0), ?, ?, ?)
				ON CONFLICT (profile_id, device_class, ifnull(device_id, 0), key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
				WHERE preferences.updated_at <= excluded.updated_at`,
				profileID, deviceClass, deviceID, key, value, madeAt.UnixMilli())
			return err
		}
		for key, value := range set {
			if err := save(key, value); err != nil {
				return err
			}
		}
		for _, key := range clear {
			if err := save(key, clearedValue); err != nil {
				return err
			}
		}
		return appendChange(tx, Change{At: time.Now().UnixMilli(), Topic: TopicPreferences, Op: OpUpdated, RefID: profileID, ProfileID: profileID})
	})
}

// ProfilePreference returns the JSON value a profile saved for one setting everywhere, or
// "" if it saved none.
func (d *DB) ProfilePreference(ctx context.Context, profileID int64, key string) (string, error) {
	var value string
	err := d.read.QueryRowContext(ctx, `SELECT value FROM preferences WHERE profile_id = ? AND device_class = '' AND device_id IS NULL AND key = ?`,
		profileID, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}
