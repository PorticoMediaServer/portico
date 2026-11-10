package db

import (
	"context"
	"database/sql"
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

// SessionDevice returns the device a session is signed in on.
func (d *DB) SessionDevice(ctx context.Context, sessionID int64) (int64, error) {
	var id int64
	err := d.read.QueryRowContext(ctx, `SELECT device_id FROM sessions WHERE id = ?`, sessionID).Scan(&id)
	if err == sql.ErrNoRows {
		err = ErrNotFound
	}
	return id, err
}

// Preferences returns what a profile saved that applies on one device: its settings for
// everywhere, for this kind of device, and for this device. deviceID is 0 for a caller with
// no device, such as a tool with a key.
func (d *DB) Preferences(ctx context.Context, profileID int64, deviceClass string, deviceID int64) ([]StoredPreference, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT device_class, device_id IS NOT NULL, key, value FROM preferences
		WHERE profile_id = ? AND (device_id = ? OR device_id IS NULL AND device_class IN ('', ?))`, profileID, deviceID, deviceClass)
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

// SetPreferences saves settings for a profile at one width: everywhere (deviceClass "" and
// deviceID 0), one kind of device, or one device. A key in clear goes back to whatever the
// next wider setting or the default says. The profile's other open apps are told.
func (d *DB) SetPreferences(ctx context.Context, profileID int64, deviceClass string, deviceID int64, set map[string]string, clear []string, now time.Time) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		at := now.UnixMilli()
		for key, value := range set {
			if _, err := tx.Exec(`INSERT INTO preferences (profile_id, device_class, device_id, key, value, updated_at) VALUES (?, ?, nullif(?, 0), ?, ?, ?)
				ON CONFLICT (profile_id, device_class, ifnull(device_id, 0), key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
				profileID, deviceClass, deviceID, key, value, at); err != nil {
				return err
			}
		}
		for _, key := range clear {
			if _, err := tx.Exec(`DELETE FROM preferences WHERE profile_id = ? AND device_class = ? AND ifnull(device_id, 0) = ? AND key = ?`,
				profileID, deviceClass, deviceID, key); err != nil {
				return err
			}
		}
		return appendChange(tx, Change{At: at, Topic: TopicPreferences, Op: OpUpdated, RefID: profileID, ProfileID: profileID})
	})
}
