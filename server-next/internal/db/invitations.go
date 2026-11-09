package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// maxOpenInvitations bounds how many invitations can be waiting at once.
const maxOpenInvitations = 200

// ErrInvitationLimit: too many invitations are already waiting to be accepted.
var ErrInvitationLimit = errors.New("too many open invitations")

// Invitation lets one person create their own account on this server.
type Invitation struct {
	ID           int64
	PublicID     string
	Role         string
	State        string
	AllLibraries bool
	Libraries    []string
	Note         string
	CreatedBy    string
	AcceptedBy   string
	CreatedAt    time.Time
	ExpiresAt    time.Time
	AcceptedAt   time.Time
}

const invitationColumns = `i.id, i.public_id, i.role, i.state, i.all_libraries, ifnull(i.note, ''),
	ifnull((SELECT username FROM users WHERE id = i.created_by_user_id), ''),
	ifnull((SELECT username FROM users WHERE id = i.accepted_user_id), ''),
	i.created_at, i.expires_at, ifnull(i.accepted_at, 0),
	ifnull((SELECT group_concat(l.public_id, ' ') FROM library_access a JOIN libraries l ON l.id = a.library_id WHERE a.invitation_id = i.id), '')`

func scanInvitation(row interface{ Scan(...any) error }, now time.Time) (Invitation, error) {
	var i Invitation
	var created, expires, accepted int64
	var libraries string
	err := row.Scan(&i.ID, &i.PublicID, &i.Role, &i.State, &i.AllLibraries, &i.Note, &i.CreatedBy, &i.AcceptedBy, &created, &expires, &accepted, &libraries)
	if errors.Is(err, sql.ErrNoRows) {
		return i, ErrNotFound
	}
	i.CreatedAt, i.ExpiresAt, i.Libraries = time.UnixMilli(created), time.UnixMilli(expires), strings.Fields(libraries)
	if accepted != 0 {
		i.AcceptedAt = time.UnixMilli(accepted)
	}
	if i.State == "open" && !i.ExpiresAt.After(now) {
		i.State = "expired"
	}
	return i, err
}

// NewInvitation is an invitation an admin makes.
type NewInvitation struct {
	ActorID      int64
	ActorRole    string
	CodeHash     string
	Role         string
	AllLibraries bool
	Libraries    []string
	Note         string
	ExpiresAt    time.Time
	Now          time.Time
}

// CreateInvitation stores an invitation. Inviting someone into a role the actor could not
// give them directly is ErrNotPermitted.
func (d *DB) CreateInvitation(ctx context.Context, n NewInvitation) (Invitation, error) {
	var out Invitation
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		if !Manages(n.ActorRole, n.Role) {
			return ErrNotPermitted
		}
		at := n.Now.UnixMilli()
		var open int
		if err := tx.QueryRow(`SELECT count(*) FROM invitations WHERE state = 'open' AND expires_at > ?`, at).Scan(&open); err != nil {
			return err
		}
		if open >= maxOpenInvitations {
			return ErrInvitationLimit
		}
		libraries, err := libraryIDs(tx, n.Libraries)
		if err != nil {
			return err
		}
		res, err := tx.Exec(`INSERT INTO invitations (public_id, code_hash, created_by_user_id, role, all_libraries, note, created_at, expires_at)
			VALUES (?, ?, ?, ?, ?, nullif(?, ''), ?, ?)`, NewID("inv"), n.CodeHash, n.ActorID, n.Role, n.AllLibraries, n.Note, at, n.ExpiresAt.UnixMilli())
		if err != nil {
			return err
		}
		id, _ := res.LastInsertId()
		for _, library := range libraries {
			if _, err := tx.Exec(`INSERT INTO library_access (library_id, invitation_id) VALUES (?, ?) ON CONFLICT DO NOTHING`, library, id); err != nil {
				return err
			}
		}
		if out, err = scanInvitation(tx.QueryRow(`SELECT `+invitationColumns+` FROM invitations i WHERE i.id = ?`, id), n.Now); err != nil {
			return err
		}
		return audit(tx, n.ActorID, "invitation.create", out.PublicID, "as "+n.Role, n.Now)
	})
	return out, err
}

// Invitations lists invitations, newest first.
func (d *DB) Invitations(ctx context.Context, now time.Time) ([]Invitation, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT `+invitationColumns+` FROM invitations i ORDER BY i.created_at DESC, i.id DESC LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Invitation
	for rows.Next() {
		i, err := scanInvitation(rows, now)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// RevokeInvitation makes an open invitation unusable. Revoking one that is not open changes nothing.
func (d *DB) RevokeInvitation(ctx context.Context, actorID int64, publicID string, now time.Time) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE invitations SET state = 'revoked' WHERE public_id = ? AND state = 'open'`, publicID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil
		}
		return audit(tx, actorID, "invitation.revoke", publicID, "", now)
	})
}

// InvitationByCode returns the open invitation behind a code, for the person holding it to
// look at before accepting. Anything else is ErrNotFound.
func (d *DB) InvitationByCode(ctx context.Context, codeHash string, now time.Time) (Invitation, error) {
	return scanInvitation(d.read.QueryRowContext(ctx, `SELECT `+invitationColumns+` FROM invitations i
		WHERE i.code_hash = ? AND i.state = 'open' AND i.expires_at > ?`, codeHash, now.UnixMilli()), now)
}

// Acceptance is a person creating their account with an invitation.
type Acceptance struct {
	CodeHash     string
	Username     string
	UsernameKey  string
	PasswordHash string
	ProfileName  string
	Session      NewSession
	Now          time.Time
}

// AcceptInvitation uses the invitation up, creates the account with the role and libraries
// the invitation carries, and signs the person in, all in one commit: of many people
// accepting one invitation at once, one gets an account. A code that is not an open
// invitation is ErrNotFound; a taken username is ErrConflict and leaves the invitation open.
func (d *DB) AcceptInvitation(ctx context.Context, a Acceptance) (SessionState, error) {
	var out SessionState
	stamp, err := d.writeSessionsStamped(ctx, func(tx *sql.Tx, _ *changes) error {
		at := a.Now.UnixMilli()
		var id int64
		var role string
		var allLibraries bool
		err := tx.QueryRow(`UPDATE invitations SET state = 'accepted', accepted_at = ? WHERE code_hash = ? AND state = 'open' AND expires_at > ?
			RETURNING id, role, all_libraries`, at, a.CodeHash, at).Scan(&id, &role, &allLibraries)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		userID, err := insertUser(tx, a.Username, a.UsernameKey, a.PasswordHash, role, a.ProfileName, a.Now)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE users SET all_libraries = ? WHERE id = ?`, allLibraries, userID); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO library_access (library_id, user_id) SELECT library_id, ? FROM library_access WHERE invitation_id = ?`, userID, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE invitations SET accepted_user_id = ? WHERE id = ?`, userID, id); err != nil {
			return err
		}
		if err := audit(tx, userID, "invitation.accept", "", a.Username, a.Now); err != nil {
			return err
		}
		out, err = startSession(tx, userID, a.Session, a.Now)
		return err
	})
	out.Stamp = stamp
	return out, err
}
