package db

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"
)

// Account is a person's account on this server (a row of users).
type Account struct {
	ID       int64
	PublicID string
	Username string
	Email    string
	Role     string
	Disabled bool
	// State is "active", or why the account cannot sign in now: "waiting" to move after the
	// kind of sign-in changed, "left" the server, or "not_moved" when the owner finished the
	// move without it.
	State     string
	CreatedAt time.Time
}

// Account returns an account by its internal id.
func (d *DB) Account(ctx context.Context, id int64) (Account, error) {
	var u Account
	var created int64
	err := d.read.QueryRowContext(ctx, `SELECT id, public_id, username, ifnull(email, ''), role, disabled_at IS NOT NULL, state, created_at
		FROM users WHERE id = ?`, id).Scan(&u.ID, &u.PublicID, &u.Username, &u.Email, &u.Role, &u.Disabled, &u.State, &created)
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
	Username string
	// PasswordHash is empty for an owner who signs in with a Portico Account.
	PasswordHash string
	// PorticoAccountID is set when the server is set up with Portico Accounts. The server's
	// kind of sign-in is then recorded in the same commit; without it the server is local.
	PorticoAccountID string
	// ApprovedAt is when that account approved the server: the link's numbers start there.
	ApprovedAt  time.Time
	ProfileName string
	// Settings are more settings rows setup decides, such as the remote access document.
	Settings map[string]string
	Session  NewSession
	Now      time.Time
}

// SignInKindKey is the setting that holds the server's kind of sign-in. It is absent on a
// server whose people sign in to it directly, and SignInPortico on one that uses Portico Accounts.
const (
	SignInKindKey = "sign_in_kind"
	SignInPortico = "portico_account"
)

// CreateOwner creates the owner account, its main profile and a first session, only while
// no account exists, together with the server's name and kind of sign-in. Two people racing
// to set up a new server get one owner.
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
		at := o.Now.UnixMilli()
		if o.PorticoAccountID != "" {
			if _, err := tx.Exec(`UPDATE users SET password_hash = NULL, password_changed_at = NULL, portico_account_id = ? WHERE id = ?`,
				o.PorticoAccountID, userID); err != nil {
				return err
			}
			if err := putSetting(tx, SignInKindKey, SignInPortico, at); err != nil {
				return err
			}
			// The link is made in the same commit, so "uses Portico Accounts" and "linked"
			// are never two facts.
			if err := linkServer(tx, o.PorticoAccountID, o.ApprovedAt, o.Now); err != nil {
				return err
			}
		}
		for key, value := range o.Settings {
			if err := putSetting(tx, key, value, at); err != nil {
				return err
			}
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

// IssueOwnerReset stores a one-time code that lets its holder choose a new password for the
// owner. publish puts the code where the owner can read it; it runs before the code is
// stored, so a failure in between leaves a file that opens nothing, never a code that works
// and that nobody was given.
//
// While a code is live (not used, not run out, not cancelled by wrong tries) nothing is
// written and nothing recorded: someone outside who keeps asking cannot replace the code the
// owner is typing, nor fill the activity record. Once it is over, a new one is written no
// sooner than every after the old one was made or ended. wait is every, or the time left
// before a new code can be written.
func (d *DB) IssueOwnerReset(ctx context.Context, codeHash string, expires time.Time, every time.Duration, now time.Time, publish func() error) (wait time.Duration, err error) {
	err = d.write(ctx, User, func(tx *sql.Tx) error {
		wait = every
		var ownerID int64
		if err := tx.QueryRow(`SELECT id FROM users WHERE role = 'owner' AND portico_account_id IS NULL`).Scan(&ownerID); errors.Is(err, sql.ErrNoRows) {
			return nil // no owner yet, or one who signs in with a Portico Account: the same answer, nothing to reset
		} else if err != nil {
			return err
		}
		var live bool
		var last int64
		err := tx.QueryRow(`SELECT used_at IS NULL AND expires_at > ?, max(created_at, ifnull(used_at, 0)) FROM auth_codes
			WHERE user_id = ? AND kind = 'owner_reset'`, now.UnixMilli(), ownerID).Scan(&live, &last)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if live {
			return nil
		}
		if since := now.Sub(time.UnixMilli(last)); err == nil && since >= 0 && since < every {
			wait = every - since
			return nil
		}
		if err := publish(); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM auth_codes WHERE user_id = ? AND kind = 'owner_reset'`, ownerID); err != nil {
			return err
		}
		if err := insertCode(tx, Code{Kind: "owner_reset", Hash: codeHash, UserID: ownerID, ExpiresAt: expires}, now); err != nil {
			return err
		}
		return audit(tx, ownerID, "owner.reset_code_written", "", "to a file in the server's data folder, asked for at the sign-in page", now)
	})
	return wait, err
}

// RedeemOwnerReset sets the owner's new password if the code is the one in the file, unused
// and in time. Everything signed in as the owner is signed out in the same commit: whoever
// held the old password must not keep a session made with it. Five wrong codes cancel the
// code. Every refusal is ErrBadCredentials or a *LimitedError.
func (d *DB) RedeemOwnerReset(ctx context.Context, codeHash, newHash string, turnOffTwoStep bool, limits []Limit, now time.Time) error {
	var refused error
	err := d.writeSessions(ctx, func(tx *sql.Tx, c *changes) error {
		refused = nil
		if wait, err := limitWait(ctx, tx, now, limits); err != nil {
			return err
		} else if wait > 0 {
			refused = &LimitedError{Wait: wait}
			return nil
		}
		at := now.UnixMilli()
		var ownerID int64
		var pending string
		var attempts int
		err := tx.QueryRow(`SELECT u.id, c.code_hash, c.attempts FROM users u JOIN auth_codes c ON c.user_id = u.id
			WHERE u.role = 'owner' AND c.kind = 'owner_reset' AND c.used_at IS NULL AND c.expires_at > ?`, at).Scan(&ownerID, &pending, &attempts)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err != nil || pending != codeHash {
			refused = ErrBadCredentials
			if err == nil {
				// The code stays in the table, spent, so the wait before a new file still counts.
				query := `UPDATE auth_codes SET attempts = attempts + 1 WHERE code_hash = ?`
				if attempts+1 >= resetAttempts {
					query = `UPDATE auth_codes SET attempts = attempts + 1, used_at = ` + strconv.FormatInt(at, 10) + ` WHERE code_hash = ?`
				}
				if _, err := tx.Exec(query, pending); err != nil {
					return err
				}
			}
			return limitFail(tx, now, limits)
		}
		if _, err := redeemCode(tx, "owner_reset", codeHash, now); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE users SET password_hash = ?, password_changed_at = ?, updated_at = ? WHERE id = ?`, newHash, at, at, ownerID); err != nil {
			return err
		}
		if turnOffTwoStep {
			if err := clearTwoFactor(tx, ownerID, now); err != nil {
				return err
			}
		}
		if err := revoke(tx, c, now, "owner_reset", `user_id = ?`, ownerID); err != nil {
			return err
		}
		return audit(tx, ownerID, "owner.password_reset", "", "with the code from the file in the server's data folder", now)
	})
	if err == nil {
		err = refused
	}
	return err
}
