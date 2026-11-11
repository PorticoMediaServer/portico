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
	UserID int64
	// ProfileID is the profile the key acts as: the one it was made from. 0 is the main profile.
	ProfileID int64
	Kind      string
	Name      string
	KeyHash   string
	Scopes    []string
	// ExpiresAt is zero for a key that does not expire.
	ExpiresAt time.Time
	// Limit is how many live keys of this kind the account may hold.
	Limit int
	// IdempotencyKey and RequestHash make a repeated request return what it made the first time.
	IdempotencyKey string
	RequestHash    string
	Now            time.Time
}

const liveKey = `revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)`

// CreateAPIKey stores a new key and says whether n.KeyHash is its secret. The same request
// sent again with its Idempotency-Key does not make a second key. The server never kept the
// first key's secret, so while that key has never been used it is given n.KeyHash, the secret
// in this answer, and the secret nobody saw stops working. Once the key has been used, its
// secret was delivered and is in use: the repeat answers the key and keeps its secret. A key
// revoked since is ErrNotFound.
func (d *DB) CreateAPIKey(ctx context.Context, n NewAPIKey) (out APIKey, secretIsNew bool, err error) {
	err = d.write(ctx, User, func(tx *sql.Tx) error {
		at := n.Now.UnixMilli()
		ref, err := createdBefore(tx, n.UserID, n.IdempotencyKey, n.RequestHash)
		if err != nil {
			return err
		}
		if ref != "" {
			out, err = scanKey(tx.QueryRow(`UPDATE api_keys SET key_hash = ? WHERE public_id = ? AND user_id = ? AND revoked_at IS NULL
				AND last_used_at IS NULL RETURNING `+keyColumns, n.KeyHash, ref, n.UserID))
			if err == nil {
				secretIsNew = true
				return nil
			}
			if errors.Is(err, sql.ErrNoRows) {
				out, err = scanKey(tx.QueryRow(`SELECT `+keyColumns+` FROM api_keys WHERE public_id = ? AND user_id = ? AND revoked_at IS NULL`, ref, n.UserID))
			}
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		// A person whose "Access tokens" permission is off has no tokens or app passwords.
		var allowed bool
		if err := tx.QueryRow(`SELECT allow_tokens = 1 OR ? = 'api_key' FROM users WHERE id = ?`, n.Kind, n.UserID).Scan(&allowed); err != nil {
			return err
		}
		if !allowed {
			return ErrNotPermitted
		}
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
		out, err = scanKey(tx.QueryRow(`INSERT INTO api_keys (public_id, user_id, kind, name, key_hash, scopes, created_at, expires_at, profile_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, nullif(?, 0)) RETURNING `+keyColumns, NewID("key"), n.UserID, n.Kind, n.Name, n.KeyHash, strings.Join(n.Scopes, " "), at, expires, n.ProfileID))
		if err != nil {
			return err
		}
		if err := audit(tx, n.UserID, "key.create", out.PublicID, n.Kind+" "+n.Name, n.Now); err != nil {
			return err
		}
		secretIsNew = true
		return rememberCreated(tx, n.UserID, n.IdempotencyKey, n.RequestHash, out.PublicID, n.Now)
	})
	return out, secretIsNew, err
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
	// KeyID is the key's own row, which the request limit counts by.
	KeyID int64
	// PublicID and Hash are the key's id and the hash of its secret, from which a compatible
	// sign-in's token is worked out, so nothing more is stored.
	PublicID string
	Hash     string
	UserID   int64
	// ProfileID is the profile the key acts as and sees as: the one it was made from, or the
	// account's main profile.
	ProfileID int64
	// Role is the account's role on its main profile; a key made from another profile is a
	// member, as that profile is.
	Role   string
	Scopes []string
}

// ErrAccountUnavailable: the key is good but its account is turned off, or waiting to move to
// another kind of sign-in, so it could not sign in either.
var ErrAccountUnavailable = errors.New("the account cannot be used now")

// KeyCaller looks a presented key up. A key that is unknown, revoked, expired or an app
// password (which only signs in to other apps' protocols) is ErrNotFound; a personal token
// whose person may no longer hold tokens is too. A key whose account is turned off or not
// active is ErrAccountUnavailable. Unlike a session's token, a key is read from the database
// on every request, so revoking one or turning its account off takes effect at once.
func (d *DB) KeyCaller(ctx context.Context, keyHash string, now time.Time) (KeyCaller, error) {
	return d.keyCaller(ctx, "k.key_hash", keyHash, now, "'api_key', 'personal_token'", "")
}

// AppPasswordByID is AppPasswordCaller for a compatible sign-in's token, which names its app
// password by id. The caller checks the token against the key's Hash.
func (d *DB) AppPasswordByID(ctx context.Context, publicID string, now time.Time) (KeyCaller, error) {
	return d.keyCaller(ctx, "k.public_id", publicID, now, "'app_password'", "")
}

// AppPasswordCaller is KeyCaller for an app password, which only a tool's username and
// password sign-in accepts. username must be the app password's own account.
func (d *DB) AppPasswordCaller(ctx context.Context, username, keyHash string, now time.Time) (KeyCaller, error) {
	if username == "" {
		return KeyCaller{}, ErrNotFound
	}
	return d.keyCaller(ctx, "k.key_hash", keyHash, now, "'app_password'", username)
}

func (d *DB) keyCaller(ctx context.Context, column, value string, now time.Time, kinds, username string) (KeyCaller, error) {
	var c KeyCaller
	var scopes string
	var used sql.NullInt64
	var usable bool
	err := d.read.QueryRowContext(ctx, `SELECT k.id, k.public_id, k.key_hash, k.scopes, k.last_used_at, u.id,
			CASE WHEN p.is_primary = 1 THEN u.role ELSE 'member' END, p.id,
			u.disabled_at IS NULL AND u.state = 'active'
		FROM api_keys k JOIN users u ON u.id = k.user_id
			JOIN profiles p ON p.user_id = u.id AND (p.id = k.profile_id OR k.profile_id IS NULL AND p.is_primary = 1)
		WHERE `+column+` = ? AND k.kind IN (`+kinds+`) AND (k.kind = 'api_key' OR u.allow_tokens = 1)
			AND k.revoked_at IS NULL AND (k.expires_at IS NULL OR k.expires_at > ?) AND (? = '' OR u.username = ? COLLATE NOCASE)`,
		value, now.UnixMilli(), username, username).
		Scan(&c.KeyID, &c.PublicID, &c.Hash, &scopes, &used, &c.UserID, &c.Role, &c.ProfileID, &usable)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	if err != nil {
		return c, err
	}
	if !usable {
		return c, ErrAccountUnavailable
	}
	id := c.KeyID
	c.Scopes = strings.Fields(scopes)
	// "Last used" is for the owner to spot a key nobody uses. An hour's precision is plenty,
	// and it is not worth making a read wait for a write, except once: the first use is
	// recorded before it is answered, because a repeated create gives a never-used key a new
	// secret and must not do that to a key already in use.
	if !used.Valid {
		err := d.write(ctx, User, func(tx *sql.Tx) error {
			_, err := tx.Exec(`UPDATE api_keys SET last_used_at = ? WHERE id = ? AND last_used_at IS NULL`, now.UnixMilli(), id)
			return err
		})
		return c, err
	}
	if now.Sub(time.UnixMilli(used.Int64)) > time.Hour {
		go d.write(context.WithoutCancel(ctx), Background, func(tx *sql.Tx) error {
			_, err := tx.Exec(`UPDATE api_keys SET last_used_at = ? WHERE id = ?`, now.UnixMilli(), id)
			return err
		})
	}
	return c, nil
}
