package access

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// AppPasswordRequest is a native, owner-only grant. Its secret is recoverable
// only by the owner; the chosen account/profile and scopes are immutable until the grant is revoked.
type AppPasswordRequest struct {
	CredentialRequest
	AccountID string `json:"accountId"`
	ProfileID string `json:"profileId"`
}

// AppPassword is deliberately a name/authority projection with no secret hint.
type AppPassword struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Username  string   `json:"username"`
	AccountID string   `json:"accountId"`
	ProfileID string   `json:"profileId"`
	Scopes    []string `json:"scopes"`
	State     string   `json:"state"`
}
type AppPasswordPage struct {
	Items      []AppPassword `json:"items"`
	NextCursor string        `json:"nextCursor,omitempty"`
}
type IssuedAppPassword struct {
	AppPassword AppPassword `json:"appPassword"`
	Secret      string      `json:"secret,omitempty"`
}

func appPassword(c Credential) AppPassword {
	return AppPassword{ID: c.ID, Name: c.Name, Username: c.Member.Username, AccountID: c.Member.AccountID, ProfileID: c.Member.ProfileID, Scopes: c.Scopes, State: c.State}
}

func (s *Store) AppPasswords(ctx context.Context, auth Authorize, cursor string) (AppPasswordPage, error) {
	page, err := s.Credentials(ctx, auth, CredentialQuery{Kind: identity.CredentialKindAppPassword, Limit: "100", Cursor: cursor})
	if err != nil {
		return AppPasswordPage{}, err
	}
	out := AppPasswordPage{Items: []AppPassword{}, NextCursor: page.NextCursor}
	for _, c := range page.Items {
		out.Items = append(out.Items, appPassword(c))
	}
	return out, nil
}

func (s *Store) CreateAppPassword(ctx context.Context, auth Authorize, issuer *identity.Service, q AppPasswordRequest) (out IssuedAppPassword, err error) {
	scopes, ok := NormalizeScopes(q.Scopes)
	if !ok || !validOperationID(q.OperationID) || strings.TrimSpace(q.Name) == "" || !safeText(q.Name, 80) || q.AccountID == "" || q.ProfileID == "" {
		return out, invalid("name", "scopes", "accountId", "profileId", "operationId")
	}
	var expires time.Time
	if q.ExpiresAt != "" {
		expires, err = time.Parse(time.RFC3339, q.ExpiresAt)
		if err != nil || !expires.After(s.Now().Add(time.Minute)) || expires.After(s.Now().Add(maxCredentialLifetime)) {
			return out, invalid("expiresAt")
		}
	}
	err = s.transaction(ctx, auth, func(tx *sql.Tx) error {
		p, e := issuer.IntegrationPrincipalTx(ctx, tx, q.AccountID, q.ProfileID)
		if e != nil {
			return e
		}
		body, want, e := receipt(tx, "integration-app-password", q.OperationID, q)
		if e != nil {
			return e
		}
		if body != "" {
			var saved struct {
				ID string `json:"id"`
			}
			if e = json.Unmarshal([]byte(body), &saved); e != nil {
				return e
			}
			c, e := s.credentialTx(ctx, tx, saved.ID)
			out.AppPassword = appPassword(c)
			return e
		}
		var count int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM api_credentials WHERE kind='app_password' AND revoked_ms=0 AND (expires_ms=0 OR expires_ms>?)`, s.now()).Scan(&count); e != nil {
			return e
		}
		if count >= MaxAPIKeys {
			return ErrCapacity
		}
		c, secret, e := s.issueIntegrationTx(ctx, tx, issuer, p, identity.CredentialKindAppPassword, strings.TrimSpace(q.Name), scopes, expires, "")
		if e != nil {
			return e
		}
		out = IssuedAppPassword{AppPassword: appPassword(c), Secret: secret}
		return saveReceipt(tx, "integration-app-password", q.OperationID, want, map[string]string{"id": c.ID}, s.now())
	})
	if err != nil {
		return IssuedAppPassword{}, err
	}
	return out, nil
}

func (s *Store) issueIntegrationTx(ctx context.Context, tx *sql.Tx, issuer *identity.Service, p identity.Principal, kind, name string, scopes []string, expires time.Time, parent string) (Credential, string, error) {
	id := token()[:22]
	issued, err := issuer.IssueAPICredentialTx(ctx, tx, p, kind, id, name, expires)
	if err != nil {
		return Credential{}, "", err
	}
	var expiry int64
	if !expires.IsZero() {
		expiry = expires.UnixMilli()
	}
	var parentValue any
	if parent != "" {
		parentValue = parent
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO api_credentials(id,kind,account_id,profile_id,name,scopes,secret_hash,hint,family_id,device_id,created_ms,expires_ms,parent_id,secret) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, kind, p.AccountID, p.ProfileID, name, strings.Join(scopes, ","), identity.Digest(issued.Secret), "", issued.FamilyID, issued.DeviceID, s.now(), expiry, parentValue, issued.Secret)
	if err != nil {
		return Credential{}, "", err
	}
	c, err := s.credentialTx(ctx, tx, id)
	return c, issued.Secret, err
}

// ExchangeAppPassword never consults account password hashes or external identity
// providers. A username plus this dedicated, revocable secret is the whole proof.
func (s *Store) ExchangeAppPassword(ctx context.Context, issuer *identity.Service, username, secret, name string) (out IssuedCredential, err error) {
	if !strings.HasPrefix(secret, "papp.") || len(secret) > 256 || len(username) > 120 {
		return out, identity.ErrUnauthorized
	}
	resolved, err := s.ResolveCredential(ctx, identity.Digest(secret))
	if err != nil || resolved.Kind != identity.CredentialKindAppPassword {
		return out, identity.ErrUnauthorized
	}
	ctx = identity.WithAPICredential(ctx, identity.APICredential{ID: resolved.ID, Kind: resolved.Kind, AccountID: resolved.AccountID, Scopes: resolved.Scopes, Digest: identity.Digest(secret)})
	p, err := issuer.AuthenticateContext(ctx, secret)
	if err != nil {
		return out, err
	}
	return s.mintIntegrationToken(ctx, issuer, p, resolved.ID, username, name, identity.CredentialKindIntegrationSession)
}

// MintIntegrationToken can only continue an app-password grant, and binds every
// child directly to that grant. There is no general credential minting authority.
func (s *Store) MintIntegrationToken(ctx context.Context, issuer *identity.Service, p identity.Principal, parentID, username, name string) (IssuedCredential, error) {
	return s.mintIntegrationToken(ctx, issuer, p, parentID, username, name, identity.CredentialKindKey)
}
func (s *Store) mintIntegrationToken(ctx context.Context, issuer *identity.Service, p identity.Principal, parentID, username, name, kind string) (out IssuedCredential, err error) {
	if name == "" {
		name = "Integration"
	}
	if !safeText(name, 80) {
		return out, invalid("name")
	}
	err = s.transaction(ctx, nil, func(tx *sql.Tx) error {
		if _, e := issuer.SessionFamilyTx(ctx, tx, p); e != nil {
			return e
		}
		parent, e := s.credentialTx(ctx, tx, parentID)
		if e != nil {
			return e
		}
		if parent.Kind == identity.CredentialKindKey || parent.Kind == identity.CredentialKindIntegrationSession {
			if e = tx.QueryRowContext(ctx, `SELECT parent_id FROM api_credentials WHERE id=? AND parent_id IS NOT NULL`, parentID).Scan(&parentID); e != nil {
				if errors.Is(e, sql.ErrNoRows) {
					return identity.ErrForbidden
				}
				return e
			}
			parent, e = s.credentialTx(ctx, tx, parentID)
			if e != nil {
				return e
			}
		}
		if parent.Kind != identity.CredentialKindAppPassword || parent.State != "active" || parent.Member.AccountID != p.AccountID || parent.Member.ProfileID != p.ProfileID || (username != "" && username != parent.Member.Username) {
			return identity.ErrUnauthorized
		}
		var count int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM api_credentials WHERE parent_id=? AND kind=? AND revoked_ms=0 AND (expires_ms=0 OR expires_ms>?)`, parentID, kind, s.now()).Scan(&count); e != nil {
			return e
		}
		if count >= MaxAccessTokens {
			if kind != identity.CredentialKindIntegrationSession {
				return ErrCapacity
			}
			// App logins are bounded sessions, not persistent named keys. Retire only
			// the oldest login at the session limit, keeping every issued key intact.
			var id, family, device string
			if e = tx.QueryRowContext(ctx, `SELECT id,family_id,device_id FROM api_credentials WHERE parent_id=? AND kind='integration_session' AND revoked_ms=0 AND (expires_ms=0 OR expires_ms>?) ORDER BY created_ms,id LIMIT 1`, parentID, s.now()).Scan(&id, &family, &device); e != nil {
				return e
			}
			if _, e = tx.ExecContext(ctx, `UPDATE api_credentials SET revoked_ms=? WHERE id=?`, s.now(), id); e != nil {
				return e
			}
			if e = issuer.RevokeAPICredentialTx(ctx, tx, family, device); e != nil {
				return e
			}
		}
		if kind == identity.CredentialKindKey {
			var all int
			if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM api_credentials WHERE kind='key' AND revoked_ms=0 AND (expires_ms=0 OR expires_ms>?)`, s.now()).Scan(&all); e != nil {
				return e
			}
			if all >= MaxAPIKeys {
				return ErrCapacity
			}
		}
		var expires time.Time
		if kind == identity.CredentialKindIntegrationSession {
			expires = s.Now().Add(90 * 24 * time.Hour)
		}
		if parent.ExpiresAt != "" {
			bound, e := time.Parse(time.RFC3339, parent.ExpiresAt)
			if e != nil {
				return e
			}
			if expires.IsZero() || bound.Before(expires) {
				expires = bound
			}
		}
		// Login sessions cannot extend their caller. Named API keys follow the
		// app grant, as native owner-issued keys can outlive the issuing session.
		if kind == identity.CredentialKindIntegrationSession && !p.ExpiresAt.IsZero() && p.ExpiresAt.Before(expires) {
			expires = p.ExpiresAt
		}
		c, secret, e := s.issueIntegrationTx(ctx, tx, issuer, p, kind, name, parent.Scopes, expires, parentID)
		out = IssuedCredential{Credential: c, Secret: secret}
		return e
	})
	if err != nil {
		return IssuedCredential{}, err
	}
	return out, nil
}

func (s *Store) RevokeAppPassword(ctx context.Context, auth Authorize, issuer *identity.Service, id string) (out AppPassword, err error) {
	err = s.transaction(ctx, auth, func(tx *sql.Tx) error {
		c, e := s.credentialTx(ctx, tx, id)
		if e != nil {
			return e
		}
		if c.Kind != identity.CredentialKindAppPassword {
			return sql.ErrNoRows
		}
		rows, e := tx.QueryContext(ctx, `SELECT id,family_id,device_id FROM api_credentials WHERE (id=? OR parent_id=?) AND revoked_ms=0`, id, id)
		if e != nil {
			return e
		}
		type grant struct{ id, family, device string }
		grants := []grant{}
		for rows.Next() {
			var g grant
			if e = rows.Scan(&g.id, &g.family, &g.device); e != nil {
				rows.Close()
				return e
			}
			grants = append(grants, g)
		}
		if e = errors.Join(rows.Err(), rows.Close()); e != nil {
			return e
		}
		for _, g := range grants {
			if _, e = tx.ExecContext(ctx, `UPDATE api_credentials SET revoked_ms=? WHERE id=?`, s.now(), g.id); e != nil {
				return e
			}
			if e = issuer.RevokeAPICredentialTx(ctx, tx, g.family, g.device); e != nil {
				return e
			}
		}
		c, e = s.credentialTx(ctx, tx, id)
		out = appPassword(c)
		return e
	})
	return out, err
}

// Credentials already share the database's backup and file-access boundary.
// The server intentionally stores replay/TOTP secrets in that boundary rather
// than using the retired file-key scheme (identity/migrate_sealed.go). API keys
// and app passwords use the same recoverable storage; account passwords do not.
func recoverableCredentialSecret(kind, secret string) string {
	if kind == identity.CredentialKindKey || kind == identity.CredentialKindAppPassword || kind == identity.CredentialKindIntegrationSession {
		return secret
	}
	return ""
}

type CredentialSecret struct {
	ID     string `json:"id"`
	Secret string `json:"secret"`
}

func (s *Store) RevealCredential(ctx context.Context, auth Authorize, id, kind string) (out CredentialSecret, err error) {
	err = s.snapshot(ctx, auth, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT id,secret FROM api_credentials WHERE id=? AND kind=? AND revoked_ms=0 AND (expires_ms=0 OR expires_ms>?)`, id, kind, s.now()).Scan(&out.ID, &out.Secret)
	})
	return out, err
}

// IntegrationKeys exposes only keys descended from the caller's app-password
// grant. Actual owner recovery authority also permits the owner API-key list.
// Scope filtering is set inclusion, not a foreign API administrator flag.
type IntegrationKey struct {
	Name   string `json:"name"`
	ID     string `json:"id"`
	Secret string `json:"secret"`
}
type IntegrationKeyPage struct {
	Items []IntegrationKey `json:"items"`
}

func (s *Store) IntegrationKeys(ctx context.Context, auth Authorize, p identity.Principal, credential identity.APICredential) (out IntegrationKeyPage, err error) {
	out.Items = []IntegrationKey{}
	err = s.snapshot(ctx, auth, func(tx *sql.Tx) error {
		root := ""
		switch credential.Kind {
		case "":
			if p.Role != identity.TierOwner || credential.ID != "" {
				return identity.ErrForbidden
			}
		case identity.CredentialKindIntegrationSession, identity.CredentialKindKey:
			if e := tx.QueryRowContext(ctx, `SELECT COALESCE(parent_id,'') FROM api_credentials WHERE id=? AND account_id=? AND profile_id=? AND revoked_ms=0`, credential.ID, p.AccountID, p.ProfileID).Scan(&root); e != nil {
				return e
			}
			if root == "" && (credential.Kind != identity.CredentialKindKey || p.Role != identity.TierOwner) {
				return identity.ErrForbidden
			}
		default:
			return identity.ErrForbidden
		}
		rows, e := tx.QueryContext(ctx, `SELECT id,name,scopes,secret FROM api_credentials WHERE revoked_ms=0 AND (expires_ms=0 OR expires_ms>?) AND ((?<>'' AND parent_id=? AND kind='key') OR (?='' AND kind='key')) ORDER BY created_ms DESC,id DESC LIMIT 100`, s.now(), root, root, root)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var key IntegrationKey
			var scopes string
			if e = rows.Scan(&key.ID, &key.Name, &scopes, &key.Secret); e != nil {
				return e
			}
			allowed := true
			for _, scope := range strings.Split(scopes, ",") {
				if credential.Kind == "" {
					continue
				}
				found := false
				for _, held := range credential.Scopes {
					if scope == held || held == "admin" {
						found = true
					}
				}
				allowed = allowed && found
			}
			if allowed {
				out.Items = append(out.Items, key)
			}
		}
		return rows.Err()
	})
	return out, err
}
