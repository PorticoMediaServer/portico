package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// ErrKeyLimit: the account already has as many keys of this kind as it may.
var ErrKeyLimit = errors.New("too many keys")

// APIKey is a long-lived credential for a tool: a server API key, a person's own access
// token, or an app password. Only the hash of its secret is stored.
type APIKey struct {
	ID         int64
	PublicID   string
	Kind       string
	Name       string
	Scopes     []string
	CreatedAt  time.Time
	LastUsedAt time.Time
	ExpiresAt  time.Time
}

const keyColumns = `id, public_id, kind, name, scopes, created_at, ifnull(last_used_at, 0), ifnull(expires_at, 0)`

func scanKey(row interface{ Scan(...any) error }) (APIKey, error) {
	var k APIKey
	var scopes string
	var created, used, expires int64
	err := row.Scan(&k.ID, &k.PublicID, &k.Kind, &k.Name, &scopes, &created, &used, &expires)
	k.Scopes, k.CreatedAt = strings.Fields(scopes), time.UnixMilli(created)
	if used != 0 {
		k.LastUsedAt = time.UnixMilli(used)
	}
	if expires != 0 {
		k.ExpiresAt = time.UnixMilli(expires)
	}
	return k, err
}

// NewAPIKey is a key to create.
type NewAPIKey struct {
	UserID  int64
	Kind    string
	Name    string
	KeyHash string
	Scopes  []string
	// ExpiresAt is zero for a key that does not expire.
	ExpiresAt time.Time
	// Limit is how many live keys of this kind the account may hold.
	Limit int
	Now   time.Time
}

const liveKey = `revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)`

// CreateAPIKey stores a new key.
func (d *DB) CreateAPIKey(ctx context.Context, n NewAPIKey) (APIKey, error) {
	var out APIKey
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		at := n.Now.UnixMilli()
		var live int
		if err := tx.QueryRow(`SELECT count(*) FROM api_keys WHERE user_id = ? AND kind = ? AND `+liveKey, n.UserID, n.Kind, at).Scan(&live); err != nil {
			return err
		}
		if live >= n.Limit {
			return ErrKeyLimit
		}
		var expires any
		if !n.ExpiresAt.IsZero() {
			expires = n.ExpiresAt.UnixMilli()
		}
		var err error
		out, err = scanKey(tx.QueryRow(`INSERT INTO api_keys (public_id, user_id, kind, name, key_hash, scopes, created_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?) RETURNING `+keyColumns, NewID("key"), n.UserID, n.Kind, n.Name, n.KeyHash, strings.Join(n.Scopes, " "), at, expires))
		if err != nil {
			return err
		}
		return audit(tx, n.UserID, "key.create", out.PublicID, n.Kind+" "+n.Name, n.Now)
	})
	return out, err
}

// APIKeys lists an account's live keys of one kind, newest first.
func (d *DB) APIKeys(ctx context.Context, userID int64, kind string, now time.Time) ([]APIKey, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT `+keyColumns+` FROM api_keys WHERE user_id = ? AND kind = ? AND `+liveKey+`
		ORDER BY created_at DESC, id DESC`, userID, kind, now.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIKey
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// RevokeAPIKey ends a key at once. Revoking one that is already ended, or not this
// account's, changes nothing.
func (d *DB) RevokeAPIKey(ctx context.Context, userID int64, kind, publicID string, now time.Time) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE api_keys SET revoked_at = ? WHERE user_id = ? AND kind = ? AND public_id = ? AND revoked_at IS NULL`,
			now.UnixMilli(), userID, kind, publicID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil
		}
		return audit(tx, userID, "key.revoke", publicID, kind, now)
	})
}

// KeyCaller is who a request carrying a key acts for.
type KeyCaller struct {
	UserID int64
	// ProfileID is the account's main profile: a key acts as its owner's main profile.
	ProfileID int64
	Role      string
	Scopes    []string
}

// KeyCaller looks a presented key up. A key that is unknown, revoked, expired, an app
// password (which only signs in to other apps' protocols) or whose account is disabled is
// ErrNotFound. Unlike a session's token, a key is read from the database on every request,
// so revoking one or disabling its account takes effect at once.
func (d *DB) KeyCaller(ctx context.Context, keyHash string, now time.Time) (KeyCaller, error) {
	var c KeyCaller
	var id int64
	var scopes string
	var used sql.NullInt64
	err := d.read.QueryRowContext(ctx, `SELECT k.id, k.scopes, k.last_used_at, u.id, u.role, p.id
		FROM api_keys k JOIN users u ON u.id = k.user_id JOIN profiles p ON p.user_id = u.id AND p.is_primary = 1
		WHERE k.key_hash = ? AND k.kind IN ('api_key', 'personal_token') AND u.disabled_at IS NULL
			AND k.revoked_at IS NULL AND (k.expires_at IS NULL OR k.expires_at > ?)`, keyHash, now.UnixMilli()).
		Scan(&id, &scopes, &used, &c.UserID, &c.Role, &c.ProfileID)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	if err != nil {
		return c, err
	}
	c.Scopes = strings.Fields(scopes)
	// "Last used" is for the owner to spot a key nobody uses. An hour's precision is plenty,
	// and it is not worth making a read wait for a write.
	if now.Sub(time.UnixMilli(used.Int64)) > time.Hour {
		go d.write(context.WithoutCancel(ctx), Background, func(tx *sql.Tx) error {
			_, err := tx.Exec(`UPDATE api_keys SET last_used_at = ? WHERE id = ?`, now.UnixMilli(), id)
			return err
		})
	}
	return c, nil
}
