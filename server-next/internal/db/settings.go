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
