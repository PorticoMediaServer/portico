package db

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"
)

var (
	// ErrNotPermitted: the caller's role does not reach this far.
	ErrNotPermitted = errors.New("not permitted")
	// ErrUnknownLibrary: a library named in the request does not exist.
	ErrUnknownLibrary = errors.New("unknown library")
)

// Manages reports whether an account with actorRole may manage one with targetRole: the
// owner manages admins and members, an admin manages members, a member manages nobody.
// Nobody manages the owner, and nobody manages themselves through the member routes.
func Manages(actorRole, targetRole string) bool {
	switch actorRole {
	case "owner":
		return targetRole == "admin" || targetRole == "member"
	case "admin":
		return targetRole == "member"
	}
	return false
}

// audit records an admin or security action in the owner's log, in the same commit.
func audit(tx *sql.Tx, actorID int64, action, target, detail string, now time.Time) error {
	_, err := tx.Exec(`INSERT INTO audit_log (at, actor_user_id, actor_name, action, target, detail)
		SELECT ?, id, username, ?, nullif(?, ''), nullif(?, '') FROM users WHERE id = ?`, now.UnixMilli(), action, target, detail, actorID)
	return err
}

// Member is an account as an admin sees it.
type Member struct {
	Account
	AllLibraries bool
	// Libraries are the public ids of the libraries the member may see when AllLibraries is false.
	Libraries []string
	Notes     string
	TwoFactor bool
	// ViewingHours is the JSON of the member's viewing hours, or "" for any time.
	ViewingHours string
	// HomeOnly limits the account to the owner's home networks.
	HomeOnly bool
}

const memberColumns = `u.id, u.public_id, u.username, ifnull(u.email, ''), u.role, u.disabled_at IS NOT NULL, u.created_at,
	u.all_libraries, ifnull(u.notes, ''), u.two_factor_enabled_at IS NOT NULL, ifnull(u.viewing_hours, ''), u.home_only,
	ifnull((SELECT group_concat(l.public_id, ' ') FROM library_access a JOIN libraries l ON l.id = a.library_id WHERE a.user_id = u.id), '')`

func scanMember(row interface{ Scan(...any) error }) (Member, error) {
	var m Member
	var created int64
	var libraries string
	err := row.Scan(&m.ID, &m.PublicID, &m.Username, &m.Email, &m.Role, &m.Disabled, &created, &m.AllLibraries, &m.Notes, &m.TwoFactor, &m.ViewingHours, &m.HomeOnly, &libraries)
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrNotFound
	}
	m.CreatedAt, m.Libraries = time.UnixMilli(created), strings.Fields(libraries)
	return m, err
}

// Members lists every account, the owner first, then by name.
func (d *DB) Members(ctx context.Context) ([]Member, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT `+memberColumns+` FROM users u ORDER BY u.role = 'owner' DESC, u.username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		m, err := scanMember(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Member returns one account by public id.
func (d *DB) Member(ctx context.Context, publicID string) (Member, error) {
	return scanMember(d.read.QueryRowContext(ctx, `SELECT `+memberColumns+` FROM users u WHERE u.public_id = ?`, publicID))
}

// managed finds an account the actor may manage. An unknown id is ErrNotFound; the actor
// themself, the owner or a peer is ErrNotPermitted.
func managed(q queryer, actorID int64, actorRole, publicID string) (Member, error) {
	m, err := scanMember(q.QueryRowContext(context.Background(), `SELECT `+memberColumns+` FROM users u WHERE u.public_id = ?`, publicID))
	if err == nil && (m.ID == actorID || !Manages(actorRole, m.Role)) {
		err = ErrNotPermitted
	}
	return m, err
}

// ManagedMember returns an account the actor may manage, for acting on its profiles.
func (d *DB) ManagedMember(ctx context.Context, actorID int64, actorRole, publicID string) (Member, error) {
	return managed(d.read, actorID, actorRole, publicID)
}

// NewMember is an account an admin creates directly, with a first password to pass on.
type NewMember struct {
	ActorID   int64
	ActorRole string
	// Username is already in its stored form: see auth.Username.
	Username     string
	PasswordHash string
	Role         string
	ProfileName  string
	Now          time.Time
}

// CreateMember creates an account. A taken username is ErrConflict; a role the actor may
// not hand out is ErrNotPermitted.
func (d *DB) CreateMember(ctx context.Context, n NewMember) (Member, error) {
	var out Member
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		if !Manages(n.ActorRole, n.Role) {
			return ErrNotPermitted
		}
		id, err := insertUser(tx, n.Username, n.PasswordHash, n.Role, n.ProfileName, n.Now)
		if err != nil {
			return err
		}
		if out, err = scanMember(tx.QueryRow(`SELECT `+memberColumns+` FROM users u WHERE u.id = ?`, id)); err != nil {
			return err
		}
		return audit(tx, n.ActorID, "member.create", out.PublicID, out.Username+" as "+n.Role, n.Now)
	})
	return out, err
}

// MemberEdit changes an account. A nil field is left as it is.
type MemberEdit struct {
	ActorID   int64
	ActorRole string
	PublicID  string
	// Role is admin or member. Only the owner can make or unmake an admin.
	Role     *string
	Disabled *bool
	Notes    *string
	// AllLibraries and Libraries say what the member may see.
	AllLibraries *bool
	Libraries    *[]string
	// ViewingHours is the JSON of the member's viewing hours; a pointer to "" removes them.
	ViewingHours *string
	// HomeOnly limits the account to the owner's home networks, or lifts that.
	HomeOnly *bool
	Now      time.Time
}

// libraryIDs resolves public library ids, refusing any that does not exist.
func libraryIDs(tx *sql.Tx, publicIDs []string) ([]int64, error) {
	ids := make([]int64, 0, len(publicIDs))
	for _, publicID := range publicIDs {
		var id int64
		err := tx.QueryRow(`SELECT id FROM libraries WHERE public_id = ?`, publicID).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUnknownLibrary
		}
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// UpdateMember edits an account the actor may manage. Disabling it ends every session of
// that account in the same commit; changing its role or what it may see makes its devices
// fetch fresh tokens, so no old token keeps a right that was taken away.
func (d *DB) UpdateMember(ctx context.Context, e MemberEdit) (Member, error) {
	var out Member
	err := d.writeSessions(ctx, func(tx *sql.Tx, c *changes) error {
		m, err := managed(tx, e.ActorID, e.ActorRole, e.PublicID)
		if err != nil {
			return err
		}
		at := e.Now.UnixMilli()
		var did []string
		accessMoved := false
		if e.Role != nil && *e.Role != m.Role {
			if !Manages(e.ActorRole, *e.Role) {
				return ErrNotPermitted
			}
			if _, err := tx.Exec(`UPDATE users SET role = ? WHERE id = ?`, *e.Role, m.ID); err != nil {
				return err
			}
			did = append(did, "role "+*e.Role)
			accessMoved = true
		}
		if e.Disabled != nil && *e.Disabled != m.Disabled {
			var disabledAt any
			if *e.Disabled {
				disabledAt = at
				if err := revoke(tx, c, e.Now, "account_disabled", `user_id = ?`, m.ID); err != nil {
					return err
				}
				did = append(did, "disabled")
			} else {
				did = append(did, "enabled")
			}
			if _, err := tx.Exec(`UPDATE users SET disabled_at = ? WHERE id = ?`, disabledAt, m.ID); err != nil {
				return err
			}
		}
		if e.Notes != nil {
			if _, err := tx.Exec(`UPDATE users SET notes = nullif(?, '') WHERE id = ?`, *e.Notes, m.ID); err != nil {
				return err
			}
		}
		if e.HomeOnly != nil {
			if _, err := tx.Exec(`UPDATE users SET home_only = ? WHERE id = ?`, *e.HomeOnly, m.ID); err != nil {
				return err
			}
			did = append(did, "home only")
		}
		if e.ViewingHours != nil {
			if _, err := tx.Exec(`UPDATE users SET viewing_hours = nullif(?, '') WHERE id = ?`, *e.ViewingHours, m.ID); err != nil {
				return err
			}
			did = append(did, "viewing hours")
		}
		if e.AllLibraries != nil || e.Libraries != nil {
			if e.AllLibraries != nil {
				if _, err := tx.Exec(`UPDATE users SET all_libraries = ? WHERE id = ?`, *e.AllLibraries, m.ID); err != nil {
					return err
				}
			}
			if e.Libraries != nil {
				ids, err := libraryIDs(tx, *e.Libraries)
				if err != nil {
					return err
				}
				if _, err := tx.Exec(`DELETE FROM library_access WHERE user_id = ?`, m.ID); err != nil {
					return err
				}
				for _, id := range ids {
					if _, err := tx.Exec(`INSERT INTO library_access (library_id, user_id) VALUES (?, ?) ON CONFLICT DO NOTHING`, id, m.ID); err != nil {
						return err
					}
				}
			}
			did = append(did, "libraries")
			accessMoved = true
		}
		if accessMoved {
			if err := accessChanged(tx, e.Now, `user_id = ?`, m.ID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`UPDATE users SET updated_at = ?, revision = revision + 1 WHERE id = ?`, at, m.ID); err != nil {
			return err
		}
		if err := markStale(tx, c, `user_id = ?`, m.ID); err != nil {
			return err
		}
		if len(did) > 0 {
			if err := audit(tx, e.ActorID, "member.update", m.PublicID, m.Username+": "+strings.Join(did, ", "), e.Now); err != nil {
				return err
			}
		}
		out, err = scanMember(tx.QueryRow(`SELECT `+memberColumns+` FROM users u WHERE u.id = ?`, m.ID))
		return err
	})
	return out, err
}

// DeleteMember removes an account the actor may manage, with its profiles and everything
// saved for them, and ends its sessions in the same commit. Removing one that is already
// gone changes nothing.
func (d *DB) DeleteMember(ctx context.Context, actorID int64, actorRole, publicID string, now time.Time) error {
	return d.writeSessions(ctx, func(tx *sql.Tx, c *changes) error {
		m, err := managed(tx, actorID, actorRole, publicID)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := revoke(tx, c, now, "account_removed", `user_id = ?`, m.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM users WHERE id = ?`, m.ID); err != nil {
			return err
		}
		return audit(tx, actorID, "member.remove", m.PublicID, m.Username, now)
	})
}

// PasswordReset is a code an admin makes for a member who cannot sign in.
type PasswordReset struct {
	ActorID   int64
	ActorRole string
	PublicID  string
	CodeHash  string
	// ResetTwoFactor also turns the member's two-step sign-in off when the code is used,
	// for someone who lost their phone as well.
	ResetTwoFactor bool
	ExpiresAt      time.Time
	Now            time.Time
}

// resetAttempts is how many wrong guesses a reset code survives.
const resetAttempts = 5

// IssuePasswordReset stores a reset code for an account the actor may manage, replacing any
// earlier one. In the same commit the member is signed out everywhere and the old password
// stops working: a code is made because the password is lost or has leaked, and waiting
// for it to be used would leave the leak open. Two-step sign-in is turned off only when the
// code is used.
func (d *DB) IssuePasswordReset(ctx context.Context, p PasswordReset) error {
	return d.writeSessions(ctx, func(tx *sql.Tx, c *changes) error {
		m, err := managed(tx, p.ActorID, p.ActorRole, p.PublicID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM auth_codes WHERE user_id = ? AND kind = 'reset'`, m.ID); err != nil {
			return err
		}
		payload := ""
		if p.ResetTwoFactor {
			payload = "two_factor"
		}
		if err := insertCode(tx, Code{Kind: "reset", Hash: p.CodeHash, UserID: m.ID, Payload: payload, ExpiresAt: p.ExpiresAt}, p.Now); err != nil {
			return err
		}
		at := p.Now.UnixMilli()
		if _, err := tx.Exec(`UPDATE users SET password_hash = NULL, password_changed_at = ?, updated_at = ? WHERE id = ?`, at, at, m.ID); err != nil {
			return err
		}
		if err := revoke(tx, c, p.Now, "password_reset", `user_id = ?`, m.ID); err != nil {
			return err
		}
		return audit(tx, p.ActorID, "member.password_reset", m.PublicID, m.Username, p.Now)
	})
}

// ResetRedemption is a member choosing a new password with a reset code.
type ResetRedemption struct {
	Username string
	CodeHash string
	NewHash  string
	Limits   []Limit
	Now      time.Time
}

// RedeemPasswordReset does the one thing a reset code allows: it sets the new password, if
// the code is this account's and unused. Five wrong codes for an account cancel its code.
// Every refusal is ErrBadCredentials or a *LimitedError, whether or not the account exists.
func (d *DB) RedeemPasswordReset(ctx context.Context, r ResetRedemption) error {
	var refused error
	err := d.writeSessions(ctx, func(tx *sql.Tx, c *changes) error {
		refused = nil
		if wait, err := limitWait(ctx, tx, r.Now, r.Limits); err != nil {
			return err
		} else if wait > 0 {
			refused = &LimitedError{Wait: wait}
			return nil
		}
		at := r.Now.UnixMilli()
		// The account's pending code, whatever was typed.
		var userID int64
		var pending, payload string
		var attempts int
		err := tx.QueryRow(`SELECT u.id, c.code_hash, ifnull(c.payload, ''), c.attempts FROM users u JOIN auth_codes c ON c.user_id = u.id
			WHERE u.username = ? AND u.disabled_at IS NULL AND c.kind = 'reset' AND c.used_at IS NULL AND c.expires_at > ?`,
			r.Username, at).Scan(&userID, &pending, &payload, &attempts)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err != nil || pending != r.CodeHash {
			refused = ErrBadCredentials
			if err == nil {
				query := `UPDATE auth_codes SET attempts = attempts + 1 WHERE code_hash = ?`
				if attempts+1 >= resetAttempts {
					query = `DELETE FROM auth_codes WHERE code_hash = ?`
				}
				if _, err := tx.Exec(query, pending); err != nil {
					return err
				}
			}
			return limitFail(tx, r.Now, r.Limits)
		}
		if _, err := redeemCode(tx, "reset", r.CodeHash, r.Now); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE users SET password_hash = ?, password_changed_at = ?, updated_at = ? WHERE id = ?`, r.NewHash, at, at, userID); err != nil {
			return err
		}
		if payload == "two_factor" {
			if err := clearTwoFactor(tx, userID, r.Now); err != nil {
				return err
			}
		}
		// Nothing should be signed in since the code was made; make sure of it.
		return revoke(tx, c, r.Now, "password_reset", `user_id = ?`, userID)
	})
	if err == nil {
		err = refused
	}
	return err
}

// IssueOwnerReset stores a one-time code that lets its holder choose a new password for the
// owner, replacing any earlier one. publish puts the code where the owner can read it; it
// runs before the code is stored, so a failure in between leaves a file that opens nothing,
// never a code that works and that nobody was given. A new code is made at most once in
// every period: asked sooner, nothing changes and wait is the time left.
func (d *DB) IssueOwnerReset(ctx context.Context, codeHash string, expires time.Time, every time.Duration, now time.Time, publish func() error) (wait time.Duration, err error) {
	err = d.write(ctx, User, func(tx *sql.Tx) error {
		wait = every
		var ownerID, last int64
		err := tx.QueryRow(`SELECT u.id, ifnull((SELECT max(created_at) FROM auth_codes WHERE user_id = u.id AND kind = 'owner_reset'), 0)
			FROM users u WHERE u.role = 'owner'`).Scan(&ownerID, &last)
		if errors.Is(err, sql.ErrNoRows) {
			return nil // a server with no owner yet: the same answer, and nothing to reset
		}
		if err != nil {
			return err
		}
		if since := now.Sub(time.UnixMilli(last)); since >= 0 && since < every {
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
		return audit(tx, ownerID, "owner.reset_file_written", "", "", now)
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
		return audit(tx, ownerID, "owner.reset_file_used", "", "", now)
	})
	if err == nil {
		err = refused
	}
	return err
}
