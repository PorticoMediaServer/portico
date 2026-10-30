package access

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/notify"
	"github.com/PorticoMediaServer/portico/server/internal/streamlimits"
	"reflect"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Invitation is an administrator-only record. Notes never belong in a shared card or recipient response.
// Codes are derived under a server-only key; only their digest is in the database.
type Invitation struct {
	// ServerID is carried with a Local code so a link can pin its destination.
	ServerID         string   `json:"serverId,omitempty"`
	ID               string   `json:"id"`
	Notes            *string  `json:"notes,omitempty"`
	Role             string   `json:"role"`
	AllowedLibraries []string `json:"allowedLibraries"`
	State            string   `json:"state"`
	CreatedAt        string   `json:"createdAt"`
	ExpiresAt        string   `json:"expiresAt"`
	AcceptedAt       string   `json:"acceptedAt,omitempty"`
	AccountID        string   `json:"accountId,omitempty"`
	Revision         int64    `json:"revision"`
	// PorticoUsername, when set, is the one Portico Account this invitation is
	// for: only that account can accept it, and it cannot be used to create a
	// password account. Empty means whoever holds the code.
	PorticoUsername string `json:"porticoUsername,omitempty"`
	// Access is what the person gets beyond role and libraries once they join.
	Access InvitationAccess `json:"access"`
	// Code is present on creation; an authorized pending-link read can recover it.
	Code string `json:"code,omitempty"`
}

// InvitationAccess is the part of a member's limits an invitation sets: streams
// at once, watching away from home, access tokens and the highest rating. It
// is validated with the member-limit rules when the invitation is made and
// written with the membership, in the accepting transaction, so the person
// never exists for a moment without it. Everything else starts at the default.
type InvitationAccess struct {
	MaxStreamsMode   streamlimits.Mode `json:"maxStreamsMode"`
	MaxStreams       int               `json:"maxStreams"`
	AllowRemote      bool              `json:"allowRemote"`
	AccessTokens     bool              `json:"accessTokens"`
	MaxContentRating string            `json:"maxContentRating"`
}

// DefaultInvitationAccess is what every new member gets when an invitation
// does not say otherwise.
func DefaultInvitationAccess() InvitationAccess {
	d := DefaultLimits()
	return InvitationAccess{MaxStreamsMode: d.MaxStreamsMode, MaxStreams: d.MaxStreams, AllowRemote: d.AllowRemote, AccessTokens: d.AccessTokens}
}

// normalize applies the member-limit rules. A rating is one value the rating
// table knows in any system, stored in one spelling, as a member's own is.
func (v *InvitationAccess) normalize() error {
	if !streamlimits.Valid(v.MaxStreamsMode, v.MaxStreams) {
		return invalid("access.maxStreams")
	}
	v.MaxContentRating = strings.ToUpper(strings.TrimSpace(v.MaxContentRating))
	if _, ok := ratingCeiling("", v.MaxContentRating); !ok || len(v.MaxContentRating) > 32 {
		return invalid("access.maxContentRating")
	}
	return nil
}

// limits is the member's whole limits document this grant produces.
func (v InvitationAccess) limits() Limits {
	out := DefaultLimits()
	out.MaxStreamsMode, out.MaxStreams, out.AllowRemote, out.AccessTokens, out.MaxContentRating = v.MaxStreamsMode, v.MaxStreams, v.AllowRemote, v.AccessTokens, v.MaxContentRating
	out.MaximumAge, _ = ratingCeiling("", v.MaxContentRating)
	return out
}

// applyInvitationAccessTx writes an accepted invitation's grant as the member's
// limits, in the transaction that creates or restores the membership. A
// restored member starts from this grant, not from what they had before.
func applyInvitationAccessTx(ctx context.Context, tx *sql.Tx, account string, v InvitationAccess, now int64) error {
	limits := v.limits()
	raw, e := limitsBody(limits)
	if e != nil {
		return e
	}
	if e = streamlimits.WriteTx(ctx, tx, account, limits.MaxStreamsMode, limits.MaxStreams); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO access_limits(account_id,revision,updated_ms,body) VALUES(?,1,?,?) ON CONFLICT(account_id) DO UPDATE SET revision=access_limits.revision+1,updated_ms=excluded.updated_ms,body=excluded.body`, account, now, string(raw)); e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `UPDATE direct_memberships SET access_tokens=? WHERE account_id=?`, limits.AccessTokens, account)
	return e
}

type InvitationPage struct {
	Page
	Items []Invitation `json:"items"`
}

type InvitationRequest struct {
	OperationID      string   `json:"operationId"`
	Notes            *string  `json:"notes,omitempty"`
	Role             string   `json:"role"`
	AllowedLibraries []string `json:"allowedLibraries"`
	ExpiresInHours   int      `json:"expiresInHours"`
	// PorticoUsername binds the invitation to one Portico Account (optional).
	PorticoUsername string `json:"porticoUsername,omitempty"`
	// Access is optional; omitted means DefaultInvitationAccess.
	Access *InvitationAccess `json:"access,omitempty"`
}

// InvitationPreview is the explicit public join-page projection. It has no target identity or notes.
type InvitationPreview struct {
	ServerName        string              `json:"serverName"`
	ExpiresAt         string              `json:"expiresAt"`
	Role              string              `json:"role"`
	LibraryCount      int                 `json:"libraryCount"`
	Libraries         []InvitationLibrary `json:"libraries"`
	AllLibraries      bool                `json:"allLibraries"`
	SignInMethod      string              `json:"signInMethod"`
	InviterUsername   string              `json:"inviterUsername,omitempty"`
	ForPorticoAccount bool                `json:"forPorticoAccount,omitempty"`
}
type InvitationLibrary struct {
	Name string `json:"name"`
	// Kind is the actual library category, never inferred from its name.
	Kind string `json:"kind,omitempty"`
}

type InvitationPreviewRequest struct {
	Code      string `json:"code,omitempty"`
	ShortCode string `json:"shortCode,omitempty"`
}

var ErrInvitationNotFound = errors.New("invitation not found")

// ErrInvitationRecipient is an invitation presented by someone other than the
// account it was made for.
var ErrInvitationRecipient = errors.New("This invitation is for a different account.")

// boundUsername normalises the Portico Account username an invitation is for.
func boundUsername(raw string) (string, bool) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return "", true
	}
	if len(v) > 64 || !safeText(v, 64) || strings.ContainsAny(v, " \t\r\n") {
		return "", false
	}
	return v, true
}

// invitationCode is a domain-separated HMAC. Replacing the file key invalidates
// all existing pending links; their stored hashes cannot recover the key or code.
func (s *Store) invitationCode(id string) string {
	h := hmac.New(sha256.New, s.invitationKey)
	h.Write([]byte("portico-invitation-v1\x00" + id))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
func (s *Store) checkInvitationCode(id, code string) bool {
	return hmac.Equal([]byte(s.invitationCode(id)), []byte(code))
}
func invitationNotes(raw *string) (*string, error) {
	if raw == nil {
		return nil, nil
	}
	v := strings.TrimSpace(*raw)
	if !safeText(v, 500) {
		return nil, invalid("notes")
	}
	if v == "" {
		return nil, nil
	}
	return &v, nil
}

// Preview treats an unknown, expired, revoked, or accepted code identically.
// It does not mutate or reserve the invitation.
func (s *Store) Preview(ctx context.Context, code string) (out InvitationPreview, err error) {
	return s.PreviewCapability(ctx, InvitationPreviewRequest{Code: code})
}

func (s *Store) PreviewCapability(ctx context.Context, q InvitationPreviewRequest) (out InvitationPreview, err error) {
	err = s.snapshot(ctx, nil, func(tx *sql.Tx) error {
		selected, e := s.invitationCapabilityTx(ctx, tx, q.Code, q.ShortCode)
		if e != nil {
			return e
		}

		var id, libraries, bound string
		var expiry int64
		e = tx.QueryRowContext(ctx, `SELECT id,role,allowed_libraries,expires_ms,bound_username FROM access_invitations WHERE id=? AND state='pending' AND expires_ms>?`, selected, s.now()).Scan(&id, &out.Role, &libraries, &expiry, &bound)
		if errors.Is(e, sql.ErrNoRows) {
			return ErrInvitationNotFound
		}
		if e != nil {
			return e
		}
		var ids []string
		if e = json.Unmarshal([]byte(libraries), &ids); e != nil {
			return e
		}
		out.Libraries = []InvitationLibrary{}
		for _, library := range ids {
			var name, kind string
			if e = tx.QueryRowContext(ctx, `SELECT name,kind FROM libraries WHERE id=?`, library).Scan(&name, &kind); e != nil {
				if errors.Is(e, sql.ErrNoRows) {
					return ErrInvitationNotFound
				}
				return e
			}
			out.Libraries = append(out.Libraries, InvitationLibrary{Name: name, Kind: kind})
		}
		out.AllLibraries = len(ids) == 0
		out.SignInMethod = "local"
		var ownerLinked bool
		if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM direct_memberships m JOIN account_portico_links l ON l.account_id=m.account_id WHERE m.role='owner')`).Scan(&ownerLinked); e != nil {
			return e
		}
		if ownerLinked || bound != "" {
			out.SignInMethod = "portico"
		}
		if e = tx.QueryRowContext(ctx, `SELECT COALESCE(a.username,'') FROM access_invitations i LEFT JOIN accounts a ON a.id=i.created_by WHERE i.id=?`, id).Scan(&out.InviterUsername); e != nil {
			return e
		}
		out.ExpiresAt = time.UnixMilli(expiry).UTC().Format(time.RFC3339)
		out.LibraryCount = len(ids)
		out.ForPorticoAccount = bound != ""
		return nil
	})
	return
}

func invitationRow(row interface{ Scan(...any) error }) (Invitation, error) {
	v, _, err := invitationRowCreated(row)
	return v, err
}

func invitationRowCreated(row interface{ Scan(...any) error }) (Invitation, int64, error) {
	var v Invitation
	var libraries string
	var created, expires int64
	var accepted sql.NullInt64
	var account sql.NullString
	if e := row.Scan(&v.ID, &v.Notes, &v.Role, &libraries, &v.State, &created, &expires, &accepted, &account, &v.Revision, &v.PorticoUsername,
		&v.Access.MaxStreamsMode, &v.Access.MaxStreams, &v.Access.AllowRemote, &v.Access.AccessTokens, &v.Access.MaxContentRating); e != nil {
		return v, 0, e
	}
	if e := json.Unmarshal([]byte(libraries), &v.AllowedLibraries); e != nil {
		return v, 0, e
	}
	if v.AllowedLibraries == nil {
		v.AllowedLibraries = []string{}
	}
	v.CreatedAt = time.UnixMilli(created).UTC().Format(time.RFC3339)
	v.ExpiresAt = time.UnixMilli(expires).UTC().Format(time.RFC3339)
	if accepted.Valid {
		v.AcceptedAt = time.UnixMilli(accepted.Int64).UTC().Format(time.RFC3339)
	}
	if account.Valid {
		v.AccountID = account.String
	}
	return v, created, nil
}

const invitationColumns = `id,notes,role,allowed_libraries,state,created_ms,expires_ms,accepted_ms,account_id,revision,bound_username,max_streams_mode,max_streams,allow_remote,access_tokens,max_content_rating`

// invitationAccessTx reads the grant an invitation carries.
func invitationAccessTx(ctx context.Context, tx *sql.Tx, id string) (v InvitationAccess, e error) {
	e = tx.QueryRowContext(ctx, `SELECT max_streams_mode,max_streams,allow_remote,access_tokens,max_content_rating FROM access_invitations WHERE id=?`, id).Scan(&v.MaxStreamsMode, &v.MaxStreams, &v.AllowRemote, &v.AccessTokens, &v.MaxContentRating)
	return
}

// Invite creates a pending invitation. actor is the caller's tier: an admin may
// invite members only, because identity.ManagesTier forbids creating a peer.
func (s *Store) Invite(ctx context.Context, auth Authorize, actor identity.Principal, q InvitationRequest) (out Invitation, err error) {
	if !validOperationID(q.OperationID) {
		return out, invalid("operationId")
	}
	notes, e := invitationNotes(q.Notes)
	if e != nil {
		return out, e
	}
	q.Notes = notes
	bound, ok := boundUsername(q.PorticoUsername)
	if !ok {
		return out, invalid("porticoUsername")
	}
	if q.Role == "" {
		q.Role = identity.TierMember
	}
	if q.Role != identity.TierMember && q.Role != identity.TierAdmin {
		return out, invalid("role")
	}
	if !identity.ManagesTier(actor.Role, q.Role) {
		return out, identity.ErrForbidden
	}
	if q.AllowedLibraries == nil {
		q.AllowedLibraries = []string{}
	}
	if len(q.AllowedLibraries) > 256 {
		return out, invalid("allowedLibraries")
	}
	for _, id := range q.AllowedLibraries {
		if !validID(id) {
			return out, invalid("allowedLibraries")
		}
	}
	if q.ExpiresInHours == 0 {
		q.ExpiresInHours = 168
	}
	if q.ExpiresInHours < 1 || q.ExpiresInHours > 720 {
		return out, invalid("expiresInHours")
	}
	grant := DefaultInvitationAccess()
	if q.Access != nil {
		grant = *q.Access
	}
	if e = grant.normalize(); e != nil {
		return out, e
	}
	q.Access = &grant
	var code string
	err = s.transaction(ctx, auth, func(tx *sql.Tx) error {
		if e := invitationManageTx(ctx, tx, actor, q.Role); e != nil {
			return e
		}
		seen := map[string]bool{}
		for _, v := range q.AllowedLibraries {
			if seen[v] {
				return invalid("allowedLibraries")
			}
			seen[v] = true
			var present bool
			if e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM libraries WHERE id=?)`, v).Scan(&present); e != nil {
				return e
			}
			if !present {
				return invalid("allowedLibraries")
			}
		}
		scope := "invite"
		body, want, e := receipt(tx, scope, q.OperationID, q)
		if e != nil {
			return e
		}
		if body != "" {
			// A replay returns the stored document, which never carries the code:
			// the code exists exactly once, in the original response.
			return json.Unmarshal([]byte(body), &out)
		}
		// Expiry is an observed status, not an administrator's revocation. Keep
		// its original record and revision while excluding it from live capacity.
		var count int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM access_invitations WHERE state='pending' AND expires_ms>?`, s.now()).Scan(&count); e != nil {
			return e
		}
		if count >= 200 {
			return ErrCapacity
		}
		serverID, e := identity.LocalLinkServerIDTx(ctx, tx)
		if e != nil {
			return e
		}
		now := s.now()
		libraries, _ := json.Marshal(q.AllowedLibraries)
		id := token()[:22]
		code = s.invitationCode(id)
		expires := now + int64(q.ExpiresInHours)*int64(time.Hour/time.Millisecond)
		if _, e = tx.ExecContext(ctx, `INSERT INTO access_invitations(id,notes,code_hash,role,allowed_libraries,state,created_ms,expires_ms,created_by,revision,bound_username,max_streams_mode,max_streams,allow_remote,access_tokens,max_content_rating) VALUES(?,?,?,?,?,'pending',?,?,?,1,?,?,?,?,?,?)`,
			id, notes, digest(code), q.Role, string(libraries), now, expires, actor.AccountID, bound, grant.MaxStreamsMode, grant.MaxStreams, grant.AllowRemote, grant.AccessTokens, grant.MaxContentRating); e != nil {
			return e
		}
		if out, e = invitationRow(tx.QueryRowContext(ctx, `SELECT `+invitationColumns+` FROM access_invitations WHERE id=?`, id)); e != nil {
			return e
		}
		if e = saveReceipt(tx, scope, q.OperationID, want, out, now); e != nil {
			return e
		}
		out.Code = code
		out.ServerID = serverID
		return nil
	})
	return
}

type InvitationQuery struct {
	Cursor string
	Limit  string
	State  string
}

func (s *Store) Invitations(ctx context.Context, auth Authorize, q InvitationQuery) (out InvitationPage, err error) {
	limit, err := pageLimit(q.Limit)
	if err != nil {
		return out, err
	}
	created, after, err := decodeCursor(q.Cursor)
	if err != nil {
		return out, err
	}
	switch q.State {
	case "", "pending", "accepted", "revoked":
	default:
		return out, invalid("state")
	}
	out.Limit, out.Items = limit, []Invitation{}
	err = s.snapshot(ctx, auth, func(tx *sql.Tx) error {
		query := `SELECT ` + invitationColumns + ` FROM access_invitations WHERE (?='' OR state=?) AND (?=0 OR (created_ms,id)<(?,?)) ORDER BY created_ms DESC,id DESC LIMIT ?`
		rows, e := tx.QueryContext(ctx, query, q.State, q.State, created, created, after, limit+1)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			v, e := invitationRow(rows)
			if e != nil {
				return e
			}
			out.Items = append(out.Items, v)
		}
		return rows.Err()
	})
	if err == nil && len(out.Items) > limit {
		last := out.Items[limit-1]
		out.Items = out.Items[:limit]
		t, _ := time.Parse(time.RFC3339, last.CreatedAt)
		out.NextCursor = encodeCursor(t.UnixMilli(), last.ID)
	}
	return
}

// Invitation reads one invitation as the list shows it. One that does not
// exist is sql.ErrNoRows.
func (s *Store) Invitation(ctx context.Context, auth Authorize, id string) (out Invitation, err error) {
	if !validID(id) {
		return out, invalid("id")
	}
	err = s.snapshot(ctx, auth, func(tx *sql.Tx) error {
		var e error
		out, e = invitationRow(tx.QueryRowContext(ctx, `SELECT `+invitationColumns+` FROM access_invitations WHERE id=?`, id))
		return e
	})
	return
}

type InvitationRevoke struct {
	ExpectedRevision int64  `json:"expectedRevision"`
	OperationID      string `json:"operationId"`
}

func (s *Store) RevokeInvitation(ctx context.Context, auth Authorize, id string, c InvitationRevoke) (out Invitation, err error) {
	if !validID(id) {
		return out, invalid("id")
	}
	if !validOperationID(c.OperationID) {
		return out, invalid("operationId")
	}
	err = s.transaction(ctx, auth, func(tx *sql.Tx) error {
		scope := "invite-revoke:" + id
		body, want, e := receipt(tx, scope, c.OperationID, c)
		if e != nil {
			return e
		}
		if body != "" {
			return json.Unmarshal([]byte(body), &out)
		}
		var state string
		var revision int64
		if e = tx.QueryRowContext(ctx, `SELECT state,revision FROM access_invitations WHERE id=?`, id).Scan(&state, &revision); e != nil {
			return e
		}
		if revision != c.ExpectedRevision {
			return &ConflictError{revision}
		}
		if state == "accepted" {
			// Revoking an accepted invitation would imply removing the account it
			// created, which is a separate, explicit act.
			return ErrConflict
		}
		if _, e = tx.ExecContext(ctx, `UPDATE access_invitations SET state='revoked',revision=revision+1 WHERE id=?`, id); e != nil {
			return e
		}
		if out, e = invitationRow(tx.QueryRowContext(ctx, `SELECT `+invitationColumns+` FROM access_invitations WHERE id=?`, id)); e != nil {
			return e
		}
		return saveReceipt(tx, scope, c.OperationID, want, out, s.now())
	})
	return
}

// AcceptanceResult is recipient-safe; administrator notes are structurally absent.
type AcceptanceResult struct {
	AccountID        string   `json:"accountId"`
	Username         string   `json:"username"`
	ProfileID        string   `json:"profileId"`
	Role             string   `json:"role"`
	AllowedLibraries []string `json:"allowedLibraries"`
}
type Acceptance struct {
	Code      string `json:"code,omitempty"`
	ShortCode string `json:"shortCode,omitempty"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	Name      string `json:"name,omitempty"`
}

// invitationAcceptPlan bounds accepting an invitation: it creates the account
// with its membership, primary profile and limits, and spends the invitation.
var invitationAcceptPlan = dbwork.WritePlan{Name: "invitation-accept", Rows: 160, Pages: 128, VMOps: 250_000,
	Why: "joining creates the account, its membership, primary profile and limits and spends the invitation together, so nobody is ever half-joined"}

// Accept redeems an invitation code and creates the account it describes. It is
// unauthenticated by design — the invitee has no account yet — so the code is
// the whole authority and the caller rate-limits the route.
//
// hash is supplied by the caller so the password cost is paid outside the
// database transaction, which holds the single SQLite writer.
func (s *Store) Accept(ctx context.Context, q Acceptance, hash []byte) (out AcceptanceResult, err error) {
	if q.ShortCode != "" && q.Code != "" {
		return out, invalid("code", "shortCode")
	}
	if q.ShortCode == "" && (len(strings.TrimSpace(q.Code)) < 32 || len(strings.TrimSpace(q.Code)) > 128) {
		return out, invalid("code")
	}

	username := strings.ToLower(strings.TrimSpace(q.Username))
	if len(username) < 3 || len(username) > 64 || !safeText(username, 64) || strings.ContainsAny(username, " \t\r\n@") {
		return out, invalid("username")
	}
	name := strings.TrimSpace(q.Name)
	if name == "" {
		name = username
	}
	if !safeText(name, 120) {
		return out, invalid("name")
	}
	err = s.transaction(dbwork.WithPlan(ctx, invitationAcceptPlan), nil, func(tx *sql.Tx) error {
		if err := identity.RequireSignInKindTx(ctx, tx, identity.SignInLocal); err != nil {
			return err
		}
		selected, e := s.invitationCapabilityTx(ctx, tx, q.Code, q.ShortCode)
		if errors.Is(e, ErrInvitationNotFound) {
			return ErrExpired
		}
		if e != nil {
			return e
		}

		var id, role, libraries, bound string
		var notes *string
		var expires int64
		e = tx.QueryRowContext(ctx, `SELECT id,notes,role,allowed_libraries,expires_ms,bound_username FROM access_invitations WHERE id=? AND state='pending'`, selected).Scan(&id, &notes, &role, &libraries, &expires, &bound)
		if errors.Is(e, sql.ErrNoRows) {
			return ErrExpired
		}
		if e != nil {
			return e
		}
		now := s.now()
		if expires <= now {
			return ErrExpired
		}
		// An invitation made for one Portico Account cannot become a password
		// account for whoever happens to hold the code.
		if bound != "" {
			return ErrInvitationRecipient
		}
		var taken bool
		if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE username=?)`, username).Scan(&taken); e != nil {
			return e
		}
		if taken {
			return ErrConflict
		}
		if e = identity.ValidateNewLocalPasswordTx(ctx, tx, q.Password); e != nil {
			return e
		}
		account, profile := token(), token()
		// The direct_account_created trigger inserts the membership and primary
		// profile; the invitation's tier and library allow list are applied on top.
		if _, e = tx.ExecContext(ctx, `INSERT INTO accounts(id,username,password_hash,profile_id) VALUES(?,?,?,?)`, account, username, hash, profile); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE direct_memberships SET role=?,allowed_libraries=?,notes=?,joined_via=?,joined_ms=?,revision=revision+1 WHERE account_id=? AND role<>'owner'`, role, libraries, notes, id, now, account); e != nil {
			return e
		}
		grant, e := invitationAccessTx(ctx, tx, id)
		if e != nil {
			return e
		}
		if e = applyInvitationAccessTx(ctx, tx, account, grant, now); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE direct_profiles SET name=? WHERE id=?`, name, profile); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE access_invitations SET state='accepted',accepted_ms=?,account_id=?,revision=revision+1 WHERE id=?`, now, account, id); e != nil {
			return e
		}
		if e = s.joinedNotice(ctx, tx, now, id, account, username); e != nil {
			return e
		}
		m, err := readMember(ctx, tx, account)
		if err != nil {
			return err
		}
		out = AcceptanceResult{AccountID: m.AccountID, Username: m.Username, ProfileID: m.ProfileID, Role: m.Role, AllowedLibraries: m.AllowedLibraries}
		return e
	})
	return
}

// PorticoAcceptance redeems an invitation for a Portico Account whose identity
// the caller has already verified (a Hosted identity assertion). The account it
// creates is linked to that identity and has no password; signIn runs in the
// same transaction, so the invitee comes back signed in or not at all.
type PorticoAcceptance struct {
	ShortCode   string
	Code        string
	AccountID   string
	Username    string
	DisplayName string
	// Consent is the verified identity assertion, kept for Hosted.
	Consent string
}

// AcceptPortico is Accept for a Portico Account. A Hosted account that is
// already a member keeps its account. An invitation only grants access where
// there is none: it sets the tier and libraries of the membership it creates,
// and restores a turned-off membership with exactly its own tier and
// libraries, provided the invitation is newer than the turning off.
// An active member (and always the owner) is returned unchanged, because a
// link opened by the wrong account must neither demote nor promote it; the
// owner changes an active member through member management.
func (s *Store) AcceptPortico(ctx context.Context, q PorticoAcceptance, signIn func(context.Context, *sql.Tx) (identity.DirectSignIn, error)) (out identity.DirectSignIn, err error) {
	if q.ShortCode != "" && q.Code != "" {
		return out, invalid("code", "shortCode")
	}
	if q.ShortCode == "" && (len(strings.TrimSpace(q.Code)) < 32 || len(strings.TrimSpace(q.Code)) > 128) {
		return out, invalid("code")
	}

	if q.AccountID == "" || len(q.AccountID) > 128 || signIn == nil {
		return out, identity.ErrUnauthorized
	}
	username := strings.ToLower(strings.TrimSpace(q.Username))
	if len(username) < 3 || len(username) > 64 || !safeText(username, 64) || strings.ContainsAny(username, " \t\r\n@") {
		return out, invalid("username")
	}
	name := strings.TrimSpace(q.DisplayName)
	if name == "" || !safeText(name, 120) {
		name = username
	}
	err = s.transaction(dbwork.WithPlan(ctx, invitationAcceptPlan), nil, func(tx *sql.Tx) error {
		if err := identity.RequireSignInKindTx(ctx, tx, identity.SignInPortico); err != nil {
			return err
		}
		selected, e := s.invitationCapabilityTx(ctx, tx, q.Code, q.ShortCode)
		if errors.Is(e, ErrInvitationNotFound) {
			return ErrExpired
		}
		if e != nil {
			return e
		}

		var id, role, libraries, bound string
		var notes *string
		var expires, invited int64
		e = tx.QueryRowContext(ctx, `SELECT id,notes,role,allowed_libraries,expires_ms,created_ms,bound_username FROM access_invitations WHERE id=? AND state='pending'`, selected).Scan(&id, &notes, &role, &libraries, &expires, &invited, &bound)
		if errors.Is(e, sql.ErrNoRows) {
			return ErrExpired
		}
		if e != nil {
			return e
		}
		now := s.now()
		if expires <= now {
			return ErrExpired
		}
		if bound != "" && bound != strings.ToLower(strings.TrimSpace(q.Username)) {
			return ErrInvitationRecipient
		}
		var account string
		created := 0
		e = tx.QueryRowContext(ctx, `SELECT account_id FROM account_portico_links WHERE hosted_account_id=?`, q.AccountID).Scan(&account)
		if errors.Is(e, sql.ErrNoRows) {
			created = 1
			account = token()
			profile := token()
			var taken bool
			if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE username=?)`, username).Scan(&taken); e != nil {
				return e
			}
			if taken {
				return ErrConflict
			}
			// The direct_account_created trigger adds the membership and the
			// primary profile, as for any invited account; the journal trigger
			// queues the push that tells Hosted. The primary (manage) profile is
			// always this one new profile, named after the Portico Account's
			// display name: Hosted keeps no profiles for a server, so there is
			// no other to choose (INT N4). The role is the invitation's, admin
			// included, and Hosted indexes it as pushed (INT N3).
			if _, e = tx.ExecContext(ctx, `INSERT INTO accounts(id,username,password_hash,profile_id) VALUES(?,?,x'',?)`, account, strings.ToLower(username), profile); e != nil {
				return e
			}
			if _, e = tx.ExecContext(ctx, `INSERT INTO account_portico_links(account_id,hosted_account_id,consent) VALUES(?,?,?)`, account, q.AccountID, q.Consent); e != nil {
				return e
			}
			if _, e = tx.ExecContext(ctx, `UPDATE direct_profiles SET name=? WHERE id=?`, name, profile); e != nil {
				return e
			}
		} else if e != nil {
			return e
		} else {
			// The account is already here. If it is turned off, this invitation
			// brings it back only when it was made after the account was turned
			// off: a later decision (the owner turning someone off, or the
			// person leaving) is not undone by an older invitation that
			// happened to be lying around. Inviting them again does it.
			var disabled bool
			var disabledMs int64
			if e = tx.QueryRowContext(ctx, `SELECT disabled,disabled_ms FROM direct_memberships WHERE account_id=?`, account).Scan(&disabled, &disabledMs); e != nil {
				return e
			}
			if disabled && invited < disabledMs {
				return ErrExpired
			}
		}
		granted, e := tx.ExecContext(ctx, `UPDATE direct_memberships SET role=?,allowed_libraries=?,notes=?,joined_via=?,joined_ms=?,disabled=0,revision=revision+1 WHERE account_id=? AND role<>'owner' AND (?<>0 OR disabled<>0)`, role, libraries, notes, id, now, account, created)
		if e != nil {
			return e
		}
		// The grant goes exactly where the tier and libraries did: a new or
		// restored membership. An active member's limits are left alone.
		if n, e := granted.RowsAffected(); e != nil {
			return e
		} else if n == 1 {
			grant, e := invitationAccessTx(ctx, tx, id)
			if e != nil {
				return e
			}
			if e = applyInvitationAccessTx(ctx, tx, account, grant, now); e != nil {
				return e
			}
		}
		if _, e = tx.ExecContext(ctx, `UPDATE access_invitations SET state='accepted',accepted_ms=?,account_id=?,revision=revision+1 WHERE id=?`, now, account, id); e != nil {
			return e
		}
		// Accepting is a fresh consent to be listed here, even for an account
		// whose standing did not change (it left at Hosted and joins again):
		// always push it, with the consent the sign-in below records.
		if _, e = tx.ExecContext(ctx, `INSERT INTO hosted_membership_journal(hosted_account_id) VALUES(?)`, q.AccountID); e != nil {
			return e
		}
		if e = s.joinedNotice(ctx, tx, now, id, account, q.Username); e != nil {
			return e
		}
		out, e = signIn(ctx, tx)
		return e
	})
	return
}

// HashPassword applies the same cost and policy the direct identity service uses
// for a member password, so an invited account is indistinguishable from one the
// owner created by hand.
func HashPassword(password string) ([]byte, error) {
	if !identity.ValidDirectPassword(password) {
		return nil, invalid("password")
	}
	return bcrypt.GenerateFromPassword([]byte(password), identity.PasswordCost)
}

// RedeemForRegistrationTx spends a member invitation for an account that
// self-registration has just created, inside the registration's transaction,
// and returns the libraries it grants. It runs after the username was found
// free, so a name clash never costs the invitee their code. Self-registration
// only ever creates plain members, so an administrator's invitation, and one
// made for a particular Portico Account, are refused here and stay usable
// through the invitation link.
func RedeemForRegistrationTx(ctx context.Context, tx *sql.Tx, code, account string, now int64) ([]string, error) {
	code = strings.TrimSpace(code)
	if len(code) < 32 || len(code) > 128 || !validID(account) {
		return nil, identity.ErrRegistrationInvite
	}
	var id, role, libraries, bound string
	var notes *string
	var expires int64
	e := tx.QueryRowContext(ctx, `SELECT id,notes,role,allowed_libraries,expires_ms,bound_username FROM access_invitations WHERE code_hash=? AND state='pending'`, digest(code)).Scan(&id, &notes, &role, &libraries, &expires, &bound)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, identity.ErrRegistrationInvite
	}
	if e != nil {
		return nil, e
	}
	if expires <= now || role != identity.TierMember || bound != "" {
		return nil, identity.ErrRegistrationInvite
	}
	var ids []string
	if e = json.Unmarshal([]byte(libraries), &ids); e != nil {
		return nil, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE direct_memberships SET notes=?,joined_via=?,joined_ms=? WHERE account_id=?`, notes, id, now, account); e != nil {
		return nil, e
	}
	grant, e := invitationAccessTx(ctx, tx, id)
	if e != nil {
		return nil, e
	}
	if e = applyInvitationAccessTx(ctx, tx, account, grant, now); e != nil {
		return nil, e
	}
	var username string
	if e = tx.QueryRowContext(ctx, `SELECT username FROM accounts WHERE id=?`, account).Scan(&username); e != nil {
		return nil, e
	}
	if e = (&Store{}).joinedNotice(ctx, tx, now, id, account, username); e != nil {
		return nil, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE access_invitations SET state='accepted',accepted_ms=?,account_id=?,revision=revision+1 WHERE id=? AND state='pending'`, now, account, id); e != nil {
		return nil, e
	}
	return ids, nil
}

// RegistrationRedeemer is RedeemForRegistrationTx in the shape the identity
// service installs (identity.Service.RedeemInvitation).
func (s *Store) RegistrationRedeemer() identity.InvitationRedeemer {
	return func(ctx context.Context, tx *sql.Tx, code, account string) ([]string, error) {
		var id string
		if e := tx.QueryRowContext(ctx, `SELECT id FROM access_invitations WHERE code_hash=?`, digest(strings.TrimSpace(code))).Scan(&id); e != nil {
			return nil, identity.ErrRegistrationInvite
		}
		if !s.checkInvitationCode(id, strings.TrimSpace(code)) {
			return nil, identity.ErrRegistrationInvite
		}
		return RedeemForRegistrationTx(ctx, tx, code, account, s.now())
	}
}

// joinedNotice deliberately carries usernames only and no private invitation text.
func (s *Store) joinedNotice(ctx context.Context, tx *sql.Tx, now int64, id, account, username string) error {
	rows, e := tx.QueryContext(ctx, `SELECT account_id FROM direct_memberships WHERE role IN('owner','admin') AND disabled=0 ORDER BY account_id`)
	if e != nil {
		return e
	}
	var accounts []string
	for rows.Next() {
		var a string
		if e = rows.Scan(&a); e != nil {
			rows.Close()
			return e
		}
		accounts = append(accounts, a)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	var serverName string
	if e = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT value FROM configuration WHERE key='name'),'Portico')`).Scan(&serverName); e != nil {
		return e
	}
	for _, a := range accounts {
		_, e = notify.Raise(tx, now, notify.Draft{Scope: notify.AdminScope("local", a), Audience: notify.AudienceAccountAdmin, Source: notify.SourceSecurity, Severity: notify.SeverityInfo, Category: "people.joined", DedupeKey: "invitation:" + id, Title: fmt.Sprintf("%s joined %s", username, serverName), Body: "The invitation was accepted.", Arguments: map[string]string{"username": username, "accountId": account, "server": serverName}, Actions: []notify.Action{}})
		if e != nil {
			return e
		}
	}
	return nil
}

// InvitationChange edits only pending, unexpired offers, preserving their code.
type InvitationChange struct {
	ExpectedRevision int64    `json:"expectedRevision"`
	OperationID      string   `json:"operationId"`
	Notes            *string  `json:"notes,omitempty"`
	Role             string   `json:"role"`
	AllowedLibraries []string `json:"allowedLibraries"`
	ExpiresAt        string   `json:"expiresAt"`
	// Access replaces the grant; omitted keeps the current one.
	Access *InvitationAccess `json:"access,omitempty"`
}
type InvitationLinkRequest struct {
	ExpectedRevision int64 `json:"expectedRevision"`
}
type InvitationLink struct {
	ServerID  string `json:"serverId,omitempty"`
	Code      string `json:"code"`
	ExpiresAt string `json:"expiresAt"`
}

func invitationManageTx(ctx context.Context, tx *sql.Tx, actor identity.Principal, role string) error {
	live, e := Role(tx, actor.AccountID)
	if e != nil {
		return e
	}
	if !identity.ManagesTier(live, role) || !identity.ManagesTier(actor.Role, role) {
		return identity.ErrForbidden
	}
	return nil
}
func (s *Store) EditInvitation(ctx context.Context, auth Authorize, actor identity.Principal, id string, q InvitationChange) (out Invitation, err error) {
	if !validID(id) || q.ExpectedRevision < 1 || !validOperationID(q.OperationID) {
		return out, invalid("id", "expectedRevision", "operationId")
	}
	notes, e := invitationNotes(q.Notes)
	if e != nil {
		return out, e
	}
	q.Notes = notes
	if q.Role != identity.TierMember && q.Role != identity.TierAdmin {
		return out, invalid("role")
	}
	expiry, e := time.Parse(time.RFC3339, q.ExpiresAt)
	if e != nil || expiry.UnixMilli() <= s.now() || expiry.After(s.Now().Add(720*time.Hour)) {
		return out, invalid("expiresAt")
	}
	if len(q.AllowedLibraries) > 256 {
		return out, invalid("allowedLibraries")
	}
	if q.AllowedLibraries == nil {
		q.AllowedLibraries = []string{}
	}
	seen := map[string]bool{}
	for _, v := range q.AllowedLibraries {
		if !validID(v) || seen[v] {
			return out, invalid("allowedLibraries")
		}
		seen[v] = true
	}
	if q.Access != nil {
		if e = q.Access.normalize(); e != nil {
			return out, e
		}
	}
	err = s.transaction(ctx, auth, func(tx *sql.Tx) error {
		current, e := invitationRow(tx.QueryRowContext(ctx, `SELECT `+invitationColumns+` FROM access_invitations WHERE id=?`, id))
		if e != nil {
			return e
		}
		if e = invitationManageTx(ctx, tx, actor, current.Role); e != nil {
			return e
		}
		if e = invitationManageTx(ctx, tx, actor, q.Role); e != nil {
			return e
		}
		currentExpiry, _ := time.Parse(time.RFC3339, current.ExpiresAt)
		if current.State != "pending" || currentExpiry.UnixMilli() <= s.now() {
			return ErrExpired
		}
		body, want, e := receipt(tx, "invite-edit:"+id, q.OperationID, q)
		if e != nil {
			return e
		}
		if body != "" {
			return json.Unmarshal([]byte(body), &out)
		}
		if current.Revision != q.ExpectedRevision {
			return &ConflictError{current.Revision}
		}
		for _, v := range q.AllowedLibraries {
			var present bool
			if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM libraries WHERE id=?)`, v).Scan(&present); e != nil {
				return e
			}
			if !present {
				return invalid("allowedLibraries")
			}
		}
		libraries, _ := json.Marshal(q.AllowedLibraries)
		grant := current.Access
		if q.Access != nil {
			grant = *q.Access
		}
		if _, e = tx.ExecContext(ctx, `UPDATE access_invitations SET notes=?,role=?,allowed_libraries=?,expires_ms=?,max_streams_mode=?,max_streams=?,allow_remote=?,access_tokens=?,max_content_rating=?,revision=revision+1 WHERE id=?`, notes, q.Role, string(libraries), expiry.UnixMilli(), grant.MaxStreamsMode, grant.MaxStreams, grant.AllowRemote, grant.AccessTokens, grant.MaxContentRating, id); e != nil {
			return e
		}
		out, e = invitationRow(tx.QueryRowContext(ctx, `SELECT `+invitationColumns+` FROM access_invitations WHERE id=?`, id))
		if e != nil {
			return e
		}
		return saveReceipt(tx, "invite-edit:"+id, q.OperationID, want, out, s.now())
	})
	return
}
func (s *Store) PendingInvitationLink(ctx context.Context, auth Authorize, actor identity.Principal, id string, q InvitationLinkRequest) (out InvitationLink, err error) {
	if !validID(id) || q.ExpectedRevision < 1 {
		return out, invalid("id", "expectedRevision")
	}
	err = s.snapshot(ctx, auth, func(tx *sql.Tx) error {
		v, e := invitationRow(tx.QueryRowContext(ctx, `SELECT `+invitationColumns+` FROM access_invitations WHERE id=?`, id))
		if e != nil {
			return e
		}
		if e = invitationManageTx(ctx, tx, actor, v.Role); e != nil {
			return e
		}
		if v.Revision != q.ExpectedRevision {
			return &ConflictError{v.Revision}
		}
		expires, _ := time.Parse(time.RFC3339, v.ExpiresAt)
		if v.State != "pending" || expires.UnixMilli() <= s.now() {
			return ErrExpired
		}
		code := s.invitationCode(id)
		var hash string
		if e = tx.QueryRowContext(ctx, `SELECT code_hash FROM access_invitations WHERE id=?`, id).Scan(&hash); e != nil {
			return e
		}
		if !hmac.Equal([]byte(hash), []byte(digest(code))) {
			return ErrExpired
		}
		serverID, e := identity.LocalLinkServerIDTx(ctx, tx)
		if e != nil {
			return e
		}
		out = InvitationLink{ServerID: serverID, Code: code, ExpiresAt: v.ExpiresAt}
		return nil
	})
	return
}

// Published bounds are the same ones enforced by invitationNotes and the store.
func notesShape(t reflect.Type) map[string]any {
	shape, e := apikit.Schema(t)
	if e != nil {
		panic(e)
	}
	fields := shape["properties"].(map[string]any)
	if note, ok := fields["notes"].(map[string]any); ok {
		note["maxLength"] = 500
	}
	return shape
}
func (InvitationRequest) JSONSchema() map[string]any {
	type wire InvitationRequest
	return notesShape(reflect.TypeFor[wire]())
}
func (InvitationChange) JSONSchema() map[string]any {
	type wire InvitationChange
	return notesShape(reflect.TypeFor[wire]())
}
func (Invitation) JSONSchema() map[string]any {
	type wire Invitation
	return notesShape(reflect.TypeFor[wire]())
}
