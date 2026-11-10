package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Setting returns the stored value for key, and whether it exists.
func (d *DB) Setting(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := d.read.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return value, err == nil, err
}

// SettingOrCreate returns the stored value for key. If there is none it stores what make
// returns, so a value that must never change (the server's id, its signing key) is made once.
func (d *DB) SettingOrCreate(ctx context.Context, key string, make func() string) (string, error) {
	if value, ok, err := d.Setting(ctx, key); err != nil || ok {
		return value, err
	}
	var value string
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?) ON CONFLICT (key) DO NOTHING`,
			key, make(), time.Now().UnixMilli()); err != nil {
			return err
		}
		return tx.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	})
	return value, err
}

// SaveSetting stores the value for key, replacing what was there.
func (d *DB) SaveSetting(ctx context.Context, key, value string, now time.Time) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
			ON CONFLICT (key) DO UPDATE SET value = excluded.value, revision = revision + 1, updated_at = excluded.updated_at`,
			key, value, now.UnixMilli())
		return err
	})
}

// HasUsers reports whether any account exists. Until one does, the server is in first-run setup.
func (d *DB) HasUsers(ctx context.Context) (bool, error) {
	var has bool
	err := d.read.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM users)`).Scan(&has)
	return has, err
}

// Ping reports whether the database answers a read.
func (d *DB) Ping(ctx context.Context) error {
	var one int
	return d.read.QueryRowContext(ctx, `SELECT 1`).Scan(&one)
}

// ErrStale: the document was saved by someone else since the caller read it.
var ErrStale = errors.New("the document has changed")

// SettingDocument returns one of the owner's settings documents and the number of times it
// has been saved, which is 0 for a document still at its defaults.
func (d *DB) SettingDocument(ctx context.Context, key string) (value string, revision int64, err error) {
	err = d.read.QueryRowContext(ctx, `SELECT value, revision FROM settings WHERE key = ?`, key).Scan(&value, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, nil
	}
	return value, revision, err
}

// SaveSettingDocument stores a settings document and records who changed it. match is the
// revision the caller read, or -1 to save whatever is there now; a document that has moved
// on since is ErrStale. It returns the new revision.
func (d *DB) SaveSettingDocument(ctx context.Context, actorID int64, key, value string, match int64, now time.Time) (int64, error) {
	var revision int64
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		err := tx.QueryRow(`SELECT revision FROM settings WHERE key = ?`, key).Scan(&revision)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if match >= 0 && match != revision {
			return ErrStale
		}
		revision++
		if _, err := tx.Exec(`INSERT INTO settings (key, value, revision, updated_at) VALUES (?, ?, ?, ?)
			ON CONFLICT (key) DO UPDATE SET value = excluded.value, revision = excluded.revision, updated_at = excluded.updated_at`,
			key, value, revision, now.UnixMilli()); err != nil {
			return err
		}
		return audit(tx, actorID, "settings.update", key, "", now)
	})
	return revision, err
}
