package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Device is an app install or browser a user has signed in on.
type Device struct {
	PublicID   string
	Name       string
	Platform   string
	App        string
	AppVersion string
	CreatedAt  time.Time
	LastSeenAt time.Time
	// SignedIn is true while the user has a live session on it.
	SignedIn bool
	// Current is true for the device the asking session is on.
	Current bool
}

// Devices lists the devices a user has a session on, live or recently ended, last seen first.
func (d *DB) Devices(ctx context.Context, userID, currentSessionID int64, now time.Time) ([]Device, error) {
	rows, err := d.read.QueryContext(ctx, `
		SELECT d.public_id, d.name, ifnull(d.platform, ''), ifnull(d.app, ''), ifnull(d.app_version, ''), d.created_at, d.last_seen_at,
			max(s.revoked_at IS NULL AND s.expires_at > ?), max(s.id = ?)
		FROM devices d JOIN sessions s ON s.device_id = d.id
		WHERE s.user_id = ? AND ifnull(s.revoked_reason, '') <> 'device_forgotten' GROUP BY d.id ORDER BY d.last_seen_at DESC, d.id`, now.UnixMilli(), currentSessionID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		var v Device
		var created, seen int64
		if err := rows.Scan(&v.PublicID, &v.Name, &v.Platform, &v.App, &v.AppVersion, &created, &seen, &v.SignedIn, &v.Current); err != nil {
			return nil, err
		}
		v.CreatedAt, v.LastSeenAt = time.UnixMilli(created), time.UnixMilli(seen)
		out = append(out, v)
	}
	return out, rows.Err()
}

// userDevice finds a device on the user's list. Anyone else's device is ErrNotFound.
func userDevice(tx *sql.Tx, userID int64, publicID string) (int64, error) {
	var id int64
	err := tx.QueryRow(`SELECT d.id FROM devices d WHERE d.public_id = ?
		AND EXISTS (SELECT 1 FROM sessions s WHERE s.device_id = d.id AND s.user_id = ?
			AND ifnull(s.revoked_reason, '') <> 'device_forgotten')`, publicID, userID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return id, err
}

// RenameDevice gives one of the user's devices a name of their choosing.
func (d *DB) RenameDevice(ctx context.Context, userID int64, publicID, name string) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		id, err := userDevice(tx, userID, publicID)
		if err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE devices SET name = ? WHERE id = ?`, name, id)
		return err
	})
}

// ForgetDevice ends the user's sessions on a device and takes it off their list, in one
// commit. Signing in on it again puts it back. Forgetting a device that is not on the list
// changes nothing.
func (d *DB) ForgetDevice(ctx context.Context, userID int64, publicID string, now time.Time) error {
	return d.writeSessions(ctx, func(tx *sql.Tx, c *changes) error {
		id, err := userDevice(tx, userID, publicID)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := revoke(tx, c, now, "device_forgotten", `user_id = ? AND device_id = ?`, userID, id); err != nil {
			return err
		}
		// Sessions that had already ended are marked too, so the device leaves the list.
		_, err = tx.Exec(`UPDATE sessions SET revoked_at = ifnull(revoked_at, ?), revoked_reason = 'device_forgotten'
			WHERE user_id = ? AND device_id = ?`, now.UnixMilli(), userID, id)
		return err
	})
}
