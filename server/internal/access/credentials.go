package access

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// API keys and personal access tokens.
//
// Both are the same thing underneath: a credential that acts as the member who
// made it (internal/identity/api_credentials.go), plus the row here that names
// it and lists its scopes. Its lifetime is its own: it ends when it is revoked,
// when it expires, or when its member is disabled or deleted, and not when the
// member changes their password or signs out everywhere. They differ only in who makes them and
// where they are listed: an API key is made by the owner in the console; a
// personal access token is made by any member for themselves.
//
// Scopes are few and plain, and one does not include another: a credential
// that should read and control playback is given both.
//
//	read     — look at libraries, titles, the guide and your own state.
//	playback — play, control playback, and change your own watch state, lists,
//	           preferences and recordings.
//	library  — scan, match, edit and delete what is in the libraries.
//	admin    — everything, as far as the member who made it may go.
//
// A scope never raises what the member behind it may do: a member's token with
// every scope still cannot administer the server.
var CredentialScopes = []string{"read", "playback", "library", "admin"}

const (
	// MaxAPIKeys bounds the keys in use on one server; MaxAccessTokens the
	// tokens in use for one member.
	MaxAPIKeys      = 100
	MaxAccessTokens = 25
	// maxCredentialLifetime bounds a chosen expiry. "No expiry" is separate.
	maxCredentialLifetime = 10 * 365 * 24 * time.Hour
)

// ErrCredentialMember reports a credential asked for by a session that cannot
// hold one (a Portico-policy session of the retired kind, or an account that
// has not chosen a profile).
var ErrCredentialMember = errors.New("this session cannot hold an API credential")

// CredentialMember says whose credential a row is, for the owner's lists.
type CredentialMember struct {
	AccountID string `json:"accountId"`
	Username  string `json:"username"`
	ProfileID string `json:"profileId"`
	Profile   string `json:"profile"`
}

// Credential is a key or token as it is listed. It never carries the secret.
type Credential struct {
	ID     string   `json:"id"`
	Kind   string   `json:"kind"`
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
	// Hint is the last characters of the secret: enough to tell two apart in a
	// list, not enough to use one.
	Hint       string `json:"hint"`
	CreatedAt  string `json:"createdAt"`
	ExpiresAt  string `json:"expiresAt,omitempty"`
	LastUsedAt string `json:"lastUsedAt,omitempty"`
	// State is active, expired, revoked or ended. "ended"
	// means it stopped working without being revoked by hand; EndedReason says
	// why: member_disabled, profile_deleted or unavailable. A
	// password change or "sign out everywhere" does not end a credential.
	State       string           `json:"state"`
	EndedReason string           `json:"endedReason,omitempty"`
	Member      CredentialMember `json:"member"`
	createdMs   int64
}

// IssuedCredential is the one response that carries a secret.
type IssuedCredential struct {
	Credential Credential `json:"credential"`
	// Secret is present only the first time this request is answered. A retry
	// of the same request returns the credential without it.
	Secret string `json:"secret,omitempty"`
}

type CredentialPage struct {
	Page
	Items []Credential `json:"items"`
	Total int          `json:"total"`
}

type CredentialRequest struct {
	OperationID string   `json:"operationId"`
	Name        string   `json:"name"`
	Scopes      []string `json:"scopes"`
	// ExpiresAt is an RFC 3339 time. Leave it out for a credential that lasts
	// until it is revoked.
	ExpiresAt string `json:"expiresAt,omitempty"`
}

// NormalizeScopes validates a requested scope list and returns it in the
// published order without duplicates.
func NormalizeScopes(requested []string) ([]string, bool) {
	if len(requested) == 0 || len(requested) > len(CredentialScopes) {
		return nil, false
	}
	seen := map[string]bool{}
	for _, scope := range requested {
		known := false
		for _, candidate := range CredentialScopes {
			known = known || candidate == scope
		}
		if !known || seen[scope] {
			return nil, false
		}
		seen[scope] = true
	}
	out := make([]string, 0, len(seen))
	for _, scope := range CredentialScopes {
		if seen[scope] {
			out = append(out, scope)
		}
	}
	return out, true
}

const credentialColumns = `c.id,c.kind,c.name,c.scopes,c.hint,c.created_ms,c.expires_ms,c.revoked_ms,c.ended_reason,c.account_id,c.profile_id,
 COALESCE(u.last_used_ms,0),COALESCE(f.revoked,1),COALESCE(f.revoked_reason,''),
 CASE WHEN a.epoch IS NOT NULL AND a.epoch=f.epoch THEN 1 ELSE 0 END,COALESCE(a.username,''),COALESCE(p.name,''),
 COALESCE(m.disabled,1),COALESCE(p.deleted,1)`

const credentialJoins = ` FROM api_credentials c
 LEFT JOIN api_credential_use u ON u.credential_id=c.id
 LEFT JOIN authorization_session_families f ON f.id=c.family_id
 LEFT JOIN identity_devices d ON d.id=c.device_id
 LEFT JOIN accounts a ON a.id=c.account_id
 LEFT JOIN direct_profiles p ON p.id=c.profile_id
 LEFT JOIN direct_memberships m ON m.account_id=c.account_id`

func (s *Store) credentialRow(row interface{ Scan(...any) error }) (Credential, error) {
	var c Credential
	var scopes, reason, ended string
	var created, expires, revoked, used int64
	var familyRevoked, epochCurrent, disabled, deleted int
	if err := row.Scan(&c.ID, &c.Kind, &c.Name, &scopes, &c.Hint, &created, &expires, &revoked, &ended, &c.Member.AccountID, &c.Member.ProfileID,
		&used, &familyRevoked, &reason, &epochCurrent, &c.Member.Username, &c.Member.Profile, &disabled, &deleted); err != nil {
		return c, err
	}
	c.Scopes = strings.Split(scopes, ",")
	c.createdMs = created
	c.CreatedAt = time.UnixMilli(created).UTC().Format(time.RFC3339)
	if expires > 0 {
		c.ExpiresAt = time.UnixMilli(expires).UTC().Format(time.RFC3339)
	}
	if used > 0 {
		c.LastUsedAt = time.UnixMilli(used).UTC().Format(time.RFC3339)
	}
	_ = reason
	switch {
	case revoked > 0 && ended != "":
		// Ended by something other than a person revoking it: its member was
		// disabled.
		c.State, c.EndedReason = "ended", ended
	case revoked > 0:
		c.State = "revoked"
	case expires > 0 && expires <= s.now():
		c.State = "expired"
	case disabled != 0:
		c.State, c.EndedReason = "ended", "member_disabled"
	case deleted != 0:
		c.State, c.EndedReason = "ended", "profile_deleted"
	case familyRevoked != 0 || epochCurrent == 0:
		// Not expected: the schema keeps a live credential's family alive. If it
		// happens the credential does not work, and the list must say so.
		c.State, c.EndedReason = "ended", "unavailable"
	default:
		c.State = "active"
	}
	return c, nil
}

func (s *Store) credentialTx(ctx context.Context, tx *sql.Tx, id string) (Credential, error) {
	return s.credentialRow(tx.QueryRowContext(ctx, `SELECT `+credentialColumns+credentialJoins+` WHERE c.id=?`, id))
}

// CreateCredential issues a key or token for maker, the caller's own live
// principal. issuer creates the session family behind it in this transaction.
var ErrAccessTokensOff = errors.New("Access tokens are turned off for this account.")

func (s *Store) CreateCredential(ctx context.Context, auth Authorize, issuer *identity.Service, maker identity.Principal, kind string, q CredentialRequest) (out IssuedCredential, err error) {
	if issuer == nil || (kind != identity.CredentialKindKey && kind != identity.CredentialKindToken) {
		return out, identity.ErrUnauthorized
	}
	if maker.Authority != "local" || maker.Role == "account" || maker.AccountID == "" || maker.ProfileID == "" {
		return out, ErrCredentialMember
	}
	fields := []string{}
	if !validOperationID(q.OperationID) {
		fields = append(fields, "operationId")
	}
	name := strings.TrimSpace(q.Name)
	if name == "" || !safeText(name, 80) {
		fields = append(fields, "name")
	}
	scopes, ok := NormalizeScopes(q.Scopes)
	if !ok {
		fields = append(fields, "scopes")
	}
	var expires time.Time
	if q.ExpiresAt != "" {
		expires, err = time.Parse(time.RFC3339, q.ExpiresAt)
		now := s.Now()
		if err != nil || !expires.After(now.Add(time.Minute)) || expires.After(now.Add(maxCredentialLifetime)) {
			fields = append(fields, "expiresAt")
		}
	}
	if len(fields) > 0 {
		return out, invalid(fields...)
	}
	err = s.transaction(ctx, auth, func(tx *sql.Tx) error {
		if kind == identity.CredentialKindToken {
			var permitted bool
			if e := tx.QueryRowContext(ctx, `SELECT access_tokens=1 AND disabled=0 FROM direct_memberships WHERE account_id=?`, maker.AccountID).Scan(&permitted); e != nil {
				return e
			}
			if !permitted {
				return ErrAccessTokensOff
			}
		}

		scope := "api-credential:" + kind + ":" + maker.AccountID
		body, want, e := receipt(tx, scope, q.OperationID, q)
		if e != nil {
			return e
		}
		if body != "" {
			// A replay names the credential that was made. It cannot show the
			// secret again through an issuance receipt. Owner recovery is explicit.
			var made struct {
				ID string `json:"id"`
			}
			if e = json.Unmarshal([]byte(body), &made); e != nil {
				return e
			}
			out.Credential, e = s.credentialTx(ctx, tx, made.ID)
			return e
		}
		now := s.now()
		var count int
		if kind == identity.CredentialKindKey {
			e = tx.QueryRowContext(ctx, `SELECT count(*) FROM api_credentials WHERE kind='key' AND revoked_ms=0 AND (expires_ms=0 OR expires_ms>?)`, now).Scan(&count)
			if e == nil && count >= MaxAPIKeys {
				e = ErrCapacity
			}
		} else {
			e = tx.QueryRowContext(ctx, `SELECT count(*) FROM api_credentials WHERE kind='token' AND account_id=? AND revoked_ms=0 AND (expires_ms=0 OR expires_ms>?)`, maker.AccountID, now).Scan(&count)
			if e == nil && count >= MaxAccessTokens {
				e = ErrCapacity
			}
		}
		if e != nil {
			return e
		}
		id := token()[:22]
		issued, e := issuer.IssueAPICredentialTx(ctx, tx, maker, kind, id, name, expires)
		if e != nil {
			return e
		}
		var expiresMs int64
		if !expires.IsZero() {
			expiresMs = expires.UTC().Truncate(time.Second).UnixMilli()
		}
		hint := issued.Secret[len(issued.Secret)-6:]
		if _, e = tx.ExecContext(ctx, `INSERT INTO api_credentials(id,kind,account_id,profile_id,name,scopes,secret_hash,hint,family_id,device_id,created_ms,expires_ms,secret) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			id, kind, maker.AccountID, maker.ProfileID, name, strings.Join(scopes, ","), identity.Digest(issued.Secret), hint, issued.FamilyID, issued.DeviceID, now, expiresMs, recoverableCredentialSecret(kind, issued.Secret)); e != nil {
			return e
		}
		if out.Credential, e = s.credentialTx(ctx, tx, id); e != nil {
			return e
		}
		if e = saveReceipt(tx, scope, q.OperationID, want, map[string]string{"id": id}, now); e != nil {
			return e
		}
		out.Secret = issued.Secret
		return nil
	})
	if err != nil {
		return IssuedCredential{}, err
	}
	return out, nil
}

type CredentialQuery struct {
	Kind string
	// AccountID limits the list to one member's credentials. Empty lists every
	// member's, which only the owner's routes ask for.
	AccountID string
	Cursor    string
	Limit     string
}

func (s *Store) Credentials(ctx context.Context, auth Authorize, q CredentialQuery) (out CredentialPage, err error) {
	limit, err := pageLimit(q.Limit)
	if err != nil {
		return out, err
	}
	created, after, err := decodeCursor(q.Cursor)
	if err != nil {
		return out, err
	}
	out.Limit, out.Items = limit, []Credential{}
	err = s.snapshot(ctx, auth, func(tx *sql.Tx) error {
		if e := tx.QueryRowContext(ctx, `SELECT count(*)`+credentialJoins+` WHERE c.kind=? AND (?='' OR c.account_id=?)`, q.Kind, q.AccountID, q.AccountID).Scan(&out.Total); e != nil {
			return e
		}
		rows, e := tx.QueryContext(ctx, `SELECT `+credentialColumns+credentialJoins+` WHERE c.kind=? AND (?='' OR c.account_id=?) AND (?=0 OR (c.created_ms,c.id)<(?,?)) ORDER BY c.created_ms DESC,c.id DESC LIMIT ?`,
			q.Kind, q.AccountID, q.AccountID, created, created, after, limit+1)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			c, e := s.credentialRow(rows)
			if e != nil {
				return e
			}
			out.Items = append(out.Items, c)
		}
		return rows.Err()
	})
	if err != nil {
		return CredentialPage{}, err
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		last := out.Items[limit-1]
		out.NextCursor = encodeCursor(last.createdMs, last.ID)
	}
	return out, nil
}

// RevokeCredential ends a key or token. It is idempotent: revoking one that is
// already revoked answers with it as it stands. accountID, when set, limits
// the caller to their own; a row that is someone else's reads as absent.
func (s *Store) RevokeCredential(ctx context.Context, auth Authorize, issuer *identity.Service, kind, id, accountID string) (out Credential, err error) {
	if issuer == nil || !validID(id) {
		return out, invalid("id")
	}
	err = s.transaction(ctx, auth, func(tx *sql.Tx) error {
		var family, device string
		var revoked int64
		e := tx.QueryRowContext(ctx, `SELECT family_id,device_id,revoked_ms FROM api_credentials WHERE id=? AND kind=? AND (?='' OR account_id=?)`, id, kind, accountID, accountID).Scan(&family, &device, &revoked)
		if e != nil {
			return e
		}
		if revoked == 0 {
			// The credential is marked first: while it is live the schema will
			// not let its family be revoked by anything (api_credential_family_guard).
			if _, e = tx.ExecContext(ctx, `UPDATE api_credentials SET revoked_ms=? WHERE id=? AND revoked_ms=0`, s.now(), id); e != nil {
				return e
			}
			if e = issuer.RevokeAPICredentialTx(ctx, tx, family, device); e != nil {
				return e
			}
		}
		out, e = s.credentialTx(ctx, tx, id)
		return e
	})
	if err != nil {
		return Credential{}, err
	}
	return out, nil
}

// ResolvedCredential is what the HTTP edge needs to hold a request to a
// credential's scopes and limits. Whether the member behind it is still
// entitled to anything is decided, as for every session, by authentication.
type ResolvedCredential struct {
	ParentID  string
	ExpiresAt time.Time
	ID        string
	Kind      string
	AccountID string
	Scopes    []string
}

// ResolveCredential looks a presented secret up by its digest. An unknown,
// revoked or expired credential is identity.ErrUnauthorized.
func (s *Store) ResolveCredential(ctx context.Context, secretDigest string) (ResolvedCredential, error) {
	var out ResolvedCredential
	if s == nil || s.DB == nil || secretDigest == "" {
		return out, identity.ErrUnauthorized
	}
	var scopes string
	var expires, revoked int64
	err := dbwork.QueryRow(ctx, s.DB, `SELECT id,kind,account_id,scopes,expires_ms,revoked_ms,COALESCE(parent_id,'') FROM api_credentials c WHERE secret_hash=? AND (c.parent_id IS NULL OR EXISTS(SELECT 1 FROM api_credentials p WHERE p.id=c.parent_id AND p.kind='app_password' AND p.revoked_ms=0 AND (p.expires_ms=0 OR p.expires_ms>?)))`, secretDigest, s.now()).Scan(&out.ID, &out.Kind, &out.AccountID, &scopes, &expires, &revoked, &out.ParentID)
	if errors.Is(err, sql.ErrNoRows) {
		return ResolvedCredential{}, identity.ErrUnauthorized
	}
	if err != nil {
		return ResolvedCredential{}, err
	}
	if revoked > 0 || expires > 0 && expires <= s.now() {
		return ResolvedCredential{}, identity.ErrUnauthorized
	}
	if expires > 0 {
		out.ExpiresAt = time.UnixMilli(expires)
	}
	out.Scopes = strings.Split(scopes, ",")
	return out, nil
}

// RecordCredentialUse writes the last-used times the edge gathered. It is a
// background write: nothing waits on it and a lost batch loses only precision.
func (s *Store) RecordCredentialUse(ctx context.Context, uses map[string]CredentialUse) error {
	if s == nil || s.DB == nil || len(uses) == 0 {
		return nil
	}
	ids := make([]string, 0, len(uses))
	for id := range uses {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return dbwork.WithWriteTx(ctx, s.DB, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		for _, id := range ids {
			use := uses[id]
			if _, err := tx.ExecContext(ctx, `INSERT INTO api_credential_use(credential_id,last_used_ms,requests) SELECT id,?,? FROM api_credentials WHERE id=?
 ON CONFLICT(credential_id) DO UPDATE SET last_used_ms=max(last_used_ms,excluded.last_used_ms),requests=requests+excluded.requests`, use.LastUsedMs, use.Requests, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// CredentialUse is one credential's activity since the last write.
type CredentialUse struct {
	LastUsedMs int64
	Requests   int64
}

// PruneCredentials forgets credentials that stopped working more than ninety
// days ago, so a list does not grow for ever with the dead.
func (s *Store) PruneCredentials(ctx context.Context) error {
	if s == nil || s.DB == nil {
		return nil
	}
	cutoff := s.Now().Add(-90 * 24 * time.Hour).UnixMilli()
	_, err := dbwork.ExecWrite(ctx, s.DB, dbwork.ClassMaintenance, `DELETE FROM api_credentials WHERE id IN (SELECT id FROM api_credentials WHERE (revoked_ms>0 AND revoked_ms<?) OR (expires_ms>0 AND expires_ms<?) LIMIT 128)`, cutoff, cutoff)
	return err
}
