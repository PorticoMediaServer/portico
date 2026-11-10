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
	// Libraries are the public ids of the libraries shared when AllLibraries is false, and
	// LibraryNames their names in the same order.
	Libraries    []string
	LibraryNames []string
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
	ifnull((SELECT group_concat(l.public_id || char(31) || l.name, char(30)) FROM library_access a JOIN libraries l ON l.id = a.library_id WHERE a.invitation_id = i.id), '')`

func scanInvitation(row interface{ Scan(...any) error }, now time.Time) (Invitation, error) {
	var i Invitation
	var created, expires, accepted int64
	var libraries string
	err := row.Scan(&i.ID, &i.PublicID, &i.Role, &i.State, &i.AllLibraries, &i.Note, &i.CreatedBy, &i.AcceptedBy, &created, &expires, &accepted, &libraries)
	if errors.Is(err, sql.ErrNoRows) {
		return i, ErrNotFound
	}
	i.CreatedAt, i.ExpiresAt = time.UnixMilli(created), time.UnixMilli(expires)
	for _, library := range strings.FieldsFunc(libraries, func(r rune) bool { return r == 30 }) {
		id, name, _ := strings.Cut(library, "\x1f")
		i.Libraries, i.LibraryNames = append(i.Libraries, id), append(i.LibraryNames, name)
	}
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
	ActorID   int64
	ActorRole string
	// PublicID is chosen by the caller, because the invitation's code is worked out from it.
	PublicID     string
	CodeHash     string
	Role         string
	AllLibraries bool
	Libraries    []string
	Note         string
	ExpiresAt    time.Time
	// IdempotencyKey and RequestHash make a repeated request return what it made the first time.
	IdempotencyKey string
	RequestHash    string
	Now            time.Time
}

// CreateInvitation stores an invitation. Inviting someone into a role the actor could not
// give them directly is ErrNotPermitted.
func (d *DB) CreateInvitation(ctx context.Context, n NewInvitation) (Invitation, error) {
	var out Invitation
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		if !Manages(n.ActorRole, n.Role) {
			return ErrNotPermitted
		}
		ref, err := createdBefore(tx, n.ActorID, n.IdempotencyKey, n.RequestHash)
		if err != nil {
			return err
		}
		if ref != "" {
			out, err = scanInvitation(tx.QueryRow(`SELECT `+invitationColumns+` FROM invitations i WHERE i.public_id = ?`, ref), n.Now)
			return err
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
			VALUES (?, ?, ?, ?, ?, nullif(?, ''), ?, ?)`, n.PublicID, n.CodeHash, n.ActorID, n.Role, n.AllLibraries, n.Note, at, n.ExpiresAt.UnixMilli())
		if err != nil {
			return err
		}
		id, _ := res.LastInsertId()
		if err := shareWithInvitation(tx, id, libraries); err != nil {
			return err
		}
		if out, err = scanInvitation(tx.QueryRow(`SELECT `+invitationColumns+` FROM invitations i WHERE i.id = ?`, id), n.Now); err != nil {
			return err
		}
		if err := audit(tx, n.ActorID, "invitation.create", out.PublicID, "as "+n.Role, n.Now); err != nil {
			return err
		}
		return rememberCreated(tx, n.ActorID, n.IdempotencyKey, n.RequestHash, out.PublicID, n.Now)
	})
	return out, err
}

func shareWithInvitation(tx *sql.Tx, id int64, libraries []int64) error {
	if _, err := tx.Exec(`DELETE FROM library_access WHERE invitation_id = ?`, id); err != nil {
		return err
	}
	for _, library := range libraries {
		if _, err := tx.Exec(`INSERT INTO library_access (library_id, invitation_id) VALUES (?, ?) ON CONFLICT DO NOTHING`, library, id); err != nil {
			return err
		}
	}
	return nil
}

// InvitationEdit changes an invitation that is still waiting. A nil field is left as it is.
type InvitationEdit struct {
	ActorID      int64
	ActorRole    string
	PublicID     string
	Role         *string
	AllLibraries *bool
	Libraries    *[]string
	Note         *string
	ExpiresAt    *time.Time
	Now          time.Time
}

// UpdateInvitation edits an open invitation; its code stays the same. One that is used,
// revoked or expired is ErrNotFound. An invitation into a role the actor could not give
// directly, before or after the edit, is ErrNotPermitted.
func (d *DB) UpdateInvitation(ctx context.Context, e InvitationEdit) (Invitation, error) {
	var out Invitation
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		i, err := scanInvitation(tx.QueryRow(`SELECT `+invitationColumns+` FROM invitations i WHERE i.public_id = ?`, e.PublicID), e.Now)
		if err != nil {
			return err
		}
		if i.State != "open" {
			return ErrNotFound
		}
		if e.Role != nil {
			i.Role = *e.Role
		}
		if !Manages(e.ActorRole, i.Role) {
			return ErrNotPermitted
		}
		if e.AllLibraries != nil {
			i.AllLibraries = *e.AllLibraries
		}
		if e.Note != nil {
			i.Note = *e.Note
		}
		if e.ExpiresAt != nil {
			i.ExpiresAt = *e.ExpiresAt
		}
		if e.Libraries != nil {
			libraries, err := libraryIDs(tx, *e.Libraries)
			if err != nil {
				return err
			}
			if err := shareWithInvitation(tx, i.ID, libraries); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`UPDATE invitations SET role = ?, all_libraries = ?, note = nullif(?, ''), expires_at = ? WHERE id = ?`,
			i.Role, i.AllLibraries, i.Note, i.ExpiresAt.UnixMilli(), i.ID); err != nil {
			return err
		}
		if out, err = scanInvitation(tx.QueryRow(`SELECT `+invitationColumns+` FROM invitations i WHERE i.id = ?`, i.ID), e.Now); err != nil {
			return err
		}
		return audit(tx, e.ActorID, "invitation.update", out.PublicID, "as "+out.Role, e.Now)
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

// InvitationAccount is the username of the account an invitation created when it was
// accepted, or ErrNotFound if it has not created one.
func (d *DB) InvitationAccount(ctx context.Context, codeHash string) (string, error) {
	var username string
	err := d.read.QueryRowContext(ctx, `SELECT u.username FROM invitations i JOIN users u ON u.id = i.accepted_user_id
		WHERE i.code_hash = ? AND i.state = 'accepted'`, codeHash).Scan(&username)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return username, err
}

// Acceptance is a person creating their account with an invitation.
type Acceptance struct {
	CodeHash string
	// Username is already in its stored form: see auth.Username.
	Username     string
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
		userID, err := insertUser(tx, a.Username, a.PasswordHash, role, a.ProfileName, a.Now)
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
