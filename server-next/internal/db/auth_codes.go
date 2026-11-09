package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Code is a short-lived secret that works once: a TV sign-in code, a password reset, a
// two-step challenge. Only its hash is stored.
type Code struct {
	Kind string
	Hash string
	// UserID is 0 for a code not yet tied to an account.
	UserID    int64
	Payload   string
	ExpiresAt time.Time
}

// CreateCode stores a new single-use code and drops codes that expired over a day ago.
func (d *DB) CreateCode(ctx context.Context, c Code, now time.Time) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM auth_codes WHERE expires_at < ?`, now.Add(-24*time.Hour).UnixMilli()); err != nil {
			return err
		}
		return insertCode(tx, c, now)
	})
}

func insertCode(tx *sql.Tx, c Code, now time.Time) error {
	_, err := tx.Exec(`INSERT INTO auth_codes (code_hash, kind, user_id, payload, created_at, expires_at) VALUES (?, ?, nullif(?, 0), nullif(?, ''), ?, ?)`,
		c.Hash, c.Kind, c.UserID, c.Payload, now.UnixMilli(), c.ExpiresAt.UnixMilli())
	return err
}

// redeemCode uses a code up inside the transaction that acts on it. However many callers
// present the same code at once, exactly one is given it; the rest, like callers with a
// wrong, used or expired code, get ErrBadCredentials.
func redeemCode(tx *sql.Tx, kind, hash string, now time.Time) (Code, error) {
	c := Code{Kind: kind, Hash: hash}
	var expires int64
	err := tx.QueryRow(`UPDATE auth_codes SET used_at = ? WHERE code_hash = ? AND kind = ? AND used_at IS NULL AND expires_at > ?
		RETURNING ifnull(user_id, 0), ifnull(payload, ''), expires_at`, now.UnixMilli(), hash, kind, now.UnixMilli()).Scan(&c.UserID, &c.Payload, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrBadCredentials
	}
	c.ExpiresAt = time.UnixMilli(expires)
	return c, err
}

// RedeemCode uses a code up on its own. Flows that must do more in the same commit call
// redeemCode inside their transaction.
func (d *DB) RedeemCode(ctx context.Context, kind, hash string, now time.Time) (Code, error) {
	var out Code
	err := d.write(ctx, User, func(tx *sql.Tx) (err error) {
		out, err = redeemCode(tx, kind, hash, now)
		return err
	})
	return out, err
}
