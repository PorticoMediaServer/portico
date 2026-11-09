package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Account is a person's account on this server (a row of users).
type Account struct {
	ID        int64
	PublicID  string
	Username  string
	Email     string
	Role      string
	Disabled  bool
	CreatedAt time.Time
}

// Account returns an account by its internal id.
func (d *DB) Account(ctx context.Context, id int64) (Account, error) {
	var u Account
	var created int64
	err := d.read.QueryRowContext(ctx, `SELECT id, public_id, username, ifnull(email, ''), role, disabled_at IS NOT NULL, created_at
		FROM users WHERE id = ?`, id).Scan(&u.ID, &u.PublicID, &u.Username, &u.Email, &u.Role, &u.Disabled, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrNotFound
	}
	u.CreatedAt = time.UnixMilli(created)
	return u, err
}

// Credentials is what a password is compared against. For a username nobody has, or an
// account with no password, UserID is 0 and the caller compares against a dummy hash so
// the answer takes as long.
type Credentials struct {
	UserID       int64
	PasswordHash string
}

// CredentialsByUsername looks an account up by the lower-cased username people sign in with.
func (d *DB) CredentialsByUsername(ctx context.Context, username string) (Credentials, error) {
	return d.credentials(ctx, `username = ?`, username)
}

// CredentialsByID looks up a signed-in user's own password, to prove them again.
func (d *DB) CredentialsByID(ctx context.Context, userID int64) (Credentials, error) {
	return d.credentials(ctx, `id = ?`, userID)
}

func (d *DB) credentials(ctx context.Context, where string, arg any) (Credentials, error) {
	var c Credentials
	var hash sql.NullString
	err := d.read.QueryRowContext(ctx, `SELECT id, password_hash FROM users WHERE `+where, arg).Scan(&c.UserID, &hash)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !hash.Valid {
		return Credentials{}, nil
	}
	c.PasswordHash = hash.String
	return c, err
}

// ErrSetupDone: the owner account already exists.
var ErrSetupDone = errors.New("setup is already done")

// NewOwner is the first account on a new server.
type NewOwner struct {
	// Username is already in its stored form: see auth.Username.
	Username     string
	PasswordHash string
	ProfileName  string
	Session      NewSession
	Now          time.Time
}

// CreateOwner creates the owner account, its main profile and a first session, only while
// no account exists. Two people racing to set up a new server get one owner.
func (d *DB) CreateOwner(ctx context.Context, o NewOwner) (SessionState, error) {
	var out SessionState
	stamp, err := d.writeSessionsStamped(ctx, func(tx *sql.Tx, _ *changes) error {
		var exists bool
		if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM users)`).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return ErrSetupDone
		}
		userID, err := insertUser(tx, o.Username, o.PasswordHash, "owner", o.ProfileName, o.Now)
		if err != nil {
			return err
		}
		out, err = startSession(tx, userID, o.Session, o.Now)
		return err
	})
	out.Stamp = stamp
	return out, err
}

// insertUser creates an account with its main profile. A taken username is ErrConflict.
func insertUser(tx *sql.Tx, username, passwordHash, role, profileName string, now time.Time) (int64, error) {
	at := now.UnixMilli()
	var taken bool
	if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM users WHERE username = ?)`, username).Scan(&taken); err != nil {
		return 0, err
	}
	if taken {
		return 0, ErrConflict
	}
	res, err := tx.Exec(`INSERT INTO users (public_id, username, password_hash, password_changed_at, role, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, NewID("usr"), username, passwordHash, at, role, at, at)
	if err != nil {
		return 0, err
	}
	userID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	_, err = tx.Exec(`INSERT INTO profiles (public_id, user_id, name, is_primary, access_changed_at, created_at, updated_at)
		VALUES (?, ?, ?, 1, ?, ?, ?)`, NewID("prf"), userID, profileName, at, at, at)
	return userID, err
}

// ChangePassword stores the new password for a user who proved the current one and, in the
// same commit, ends every other session of the user, so whoever knew the old password is
// signed out. The session that made the change stays. Keys and tokens made for tools are
// not sessions and keep working. A wrong current password is ErrBadCredentials.
func (d *DB) ChangePassword(ctx context.Context, p PasswordProof, newHash string, keepSessionID int64, now time.Time) error {
	var refused error
	err := d.writeSessions(ctx, func(tx *sql.Tx, c *changes) error {
		refused = nil
		ok, err := proven(tx, now, p, &refused)
		if err != nil || !ok {
			return err
		}
		at := now.UnixMilli()
		if _, err := tx.Exec(`UPDATE users SET password_hash = ?, password_changed_at = ?, updated_at = ? WHERE id = ?`, newHash, at, at, p.UserID); err != nil {
			return err
		}
		return revoke(tx, c, now, "password_changed", `user_id = ? AND id <> ?`, p.UserID, keepSessionID)
	})
	if err == nil {
		err = refused
	}
	return err
}
