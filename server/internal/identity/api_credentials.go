package identity

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"strings"
	"time"
)

// API keys and personal access tokens.
//
// A key or token is not a second kind of authority: it acts as the member who
// made it, and every request it makes gets the same live checks of the account,
// the profile, the role and the device that a session gets. It is stored in the
// same family tables so that all of that code applies to it unchanged, and the
// HTTP edge holds it to the scopes it was given.
//
// Its lifetime, though, is its own. It does not rotate, and it ends only when
// it is revoked, when it reaches the expiry its maker chose, or when its member
// is disabled or deleted. Changing a password or signing out everywhere ends a
// member's sessions and leaves their keys and tokens working: an owner who
// changes their password must not silently break what they have connected. The
// rules that keep the session machinery from ending a credential are triggers
// in the schema (api_credential_*), so they hold for every present and future
// path that ends sessions.
//
// Two rules keep a credential from ever being used as an unscoped session:
//
//   - Its secret carries a prefix with a '.', which an ordinary session token
//     (unpadded base64url) cannot contain. The shape alone identifies it.
//   - AuthenticateContext accepts a credential-shaped bearer only when the
//     context says the edge has already resolved that exact secret and checked
//     the route's scope. A handler that authenticates a bearer it found
//     anywhere else gets ErrUnauthorized.

const (
	// APIKeyPrefix and AccessTokenPrefix start every secret of their kind.
	APIKeyPrefix      = "pk."
	AccessTokenPrefix = "pat."

	// CredentialKindKey is an owner API key; CredentialKindToken is a personal
	// access token any member may make for themselves.
	CredentialKindKey                = "key"
	CredentialKindToken              = "token"
	CredentialKindAppPassword        = "app_password"
	CredentialKindIntegrationSession = "integration_session"

	// credentialDevicePlatform marks the device row a credential is bound to,
	// so device lists can tell it from a phone or a television.
	credentialDevicePlatform = "api"
)

// credentialForever is the horizon of a credential with no expiry.
var credentialForever = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)

// ErrCredentialInput reports a key or token request that cannot be issued.
var ErrCredentialInput = requestinput.New("invalid API credential request")

// CredentialShaped reports whether a bearer is an API key or personal access
// token by its shape. It says nothing about whether the secret is valid.
func CredentialShaped(bearer string) bool { return strings.Contains(bearer, ".") }

// APICredential is what the HTTP edge established about the key or token a
// request carries, after it resolved the secret and checked the route's scope.
type APICredential struct {
	ID        string
	Kind      string
	AccountID string
	Scopes    []string
	// Digest is the digest of the secret the edge resolved. Authentication
	// compares it with the bearer it is asked about, so a context admitted for
	// one credential cannot vouch for another.
	Digest string
}

type apiCredentialKey struct{}

// WithAPICredential records that the edge resolved and scope-checked c for
// this request.
func WithAPICredential(ctx context.Context, c APICredential) context.Context {
	return context.WithValue(ctx, apiCredentialKey{}, c)
}

// APICredentialFrom returns the credential the edge admitted for this request.
func APICredentialFrom(ctx context.Context) (APICredential, bool) {
	c, ok := ctx.Value(apiCredentialKey{}).(APICredential)
	return c, ok && c.ID != "" && c.Digest != ""
}

// admitBearer refuses a credential-shaped bearer the edge has not vouched for.
func admitBearer(ctx context.Context, bearer string) error {
	if !CredentialShaped(bearer) {
		return nil
	}
	c, ok := APICredentialFrom(ctx)
	if !ok || c.Digest != Digest(bearer) {
		return ErrUnauthorized
	}
	return nil
}

// IssuedCredential is the result of issuing a key or token.
type IssuedCredential struct {
	// Secret is returned at issuance. Authentication uses its digest; native
	// owner recovery stores the permitted credential secret separately.
	Secret   string
	FamilyID string
	DeviceID string
	// AwaitingApproval is true when the owner approves new devices by hand and
	// this credential, like any new device, waits for that approval.
}

// IssueAPICredentialTx creates the session family a key or token authenticates
// with, for the member and profile maker is signed in as. maker must be the
// caller's own live principal: the family carries its account, profile, role
// and epoch, and the same check that guards every sign-in runs here.
//
// expires is the moment the credential stops working; the zero time means it
// lasts until revoked.
func (s *Service) IssueAPICredentialTx(ctx context.Context, tx *sql.Tx, maker Principal, kind, credentialID, label string, expires time.Time) (IssuedCredential, error) {
	var out IssuedCredential
	if tx == nil || maker.Authority != "local" || maker.Role == "account" || !validFamilyID(maker.AccountID) || !validFamilyID(maker.ProfileID) || maker.Epoch < 1 || !validFamilyID(credentialID) {
		return out, ErrUnauthorized
	}
	if maker.Hash != "" {
		if e := refuseResetDescendantTx(ctx, tx, familyForTokenTx(ctx, tx, maker.Hash)); e != nil {
			return out, e
		}
	}
	prefix := ""
	switch kind {
	case CredentialKindKey:
		prefix = APIKeyPrefix
	case CredentialKindToken:
		prefix = AccessTokenPrefix
	case CredentialKindAppPassword:
		prefix = "papp."
	case CredentialKindIntegrationSession:
		prefix = "pit."
	default:
		return out, ErrCredentialInput
	}
	name, ok := deviceText(label, 120)
	if !ok || name == "" {
		return out, ErrCredentialInput
	}
	now := time.Now().UTC().Truncate(time.Second)
	horizon := credentialForever
	if !expires.IsZero() {
		horizon = expires.UTC().Truncate(time.Second)
		if !horizon.After(now) {
			return out, ErrCredentialInput
		}
	}
	p := Principal{Viewer: Viewer{AccountID: maker.AccountID, ProfileID: maker.ProfileID, ServerID: s.serverID, Authority: "local", Role: maker.Role}, Epoch: maker.Epoch}
	if err := s.checkFamilyPrincipalTx(ctx, tx, p); err != nil {
		return out, err
	}
	stamp := now.Format(time.RFC3339)
	out.DeviceID, out.FamilyID = Token(), Token()
	out.Secret = prefix + Token()
	if _, err := tx.ExecContext(ctx, `INSERT INTO identity_devices(id,authority,account_id,installation_id,name,platform,app,app_version,ip,first_seen,last_seen,remember_account) VALUES(?,?,?,?,?,?,?,'','',?,?,0)`,
		out.DeviceID, "local", p.AccountID, "api-credential-"+credentialID, name, credentialDevicePlatform, "api-"+kind, stamp, stamp); err != nil {
		return out, err
	}
	horizonText := horizon.Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx, `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,revoked) VALUES(?,?,?,?,?,?,?,?,1,0)`,
		out.FamilyID, s.serverID, p.AccountID, p.ProfileID, "local", p.Role, p.Epoch, horizonText); err != nil {
		return out, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO identity_device_families(family_id,device_id) VALUES(?,?)`, out.FamilyID, out.DeviceID); err != nil {
		return out, err
	}
	// The credential continues its maker's PIN-attempt lineage; it is not a new
	// device chain with a budget of its own, so a maker with no session has none.
	lineageCtx := ctx
	if kind != CredentialKindAppPassword || maker.Hash != "" {
		var err error
		lineageCtx, err = withPINLineageOf(ctx, tx, familyForTokenTx(ctx, tx, maker.Hash))
		if err != nil {
			return out, err
		}
	}
	// A new owner-authorized app password is a fixed-profile origin device.
	// Its descendants inherit this lineage; none may select a profile or use
	// a PIN/session-only endpoint, so exchanges cannot create new PIN budgets.
	if err := bindPINLineageTx(lineageCtx, tx, p.AccountID, out.DeviceID, out.FamilyID); err != nil {
		return out, err
	}
	// One token for the family's whole life: there is no refresh credential, so
	// nothing can rotate it or push its horizon out.
	if _, err := tx.ExecContext(ctx, `INSERT INTO authorization_family_tokens(token_hash,family_id,generation,expires_at,retired) VALUES(?,?,1,?,0)`, Digest(out.Secret), out.FamilyID, horizonText); err != nil {
		return out, err
	}
	return out, ctx.Err()
}

// RevokeAPICredentialTx ends a key or token whose credential row the caller has
// already marked revoked in this transaction (until it is, the schema refuses
// to revoke its family): the family is revoked, whatever
// hangs off it (queues, playback, streams) retires through the same hook a
// sign-out uses, and its device row goes so it no longer shows in device lists.
func (s *Service) RevokeAPICredentialTx(ctx context.Context, tx *sql.Tx, familyID, deviceID string) error {
	if tx == nil || !validFamilyID(familyID) {
		return ErrUnauthorized
	}
	if err := RevokeFamilyTx(ctx, tx, familyID, RevokedAdminRevoke); err != nil {
		return err
	}
	if s.OnFamilyRevokedTx != nil {
		if err := s.OnFamilyRevokedTx(ctx, tx, FamilyState{ID: familyID, DeviceID: deviceID}); err != nil {
			return err
		}
	}
	if deviceID != "" {
		if _, err := tx.ExecContext(ctx, `DELETE FROM identity_devices WHERE id=? AND platform=?`, deviceID, credentialDevicePlatform); err != nil {
			return err
		}
	}
	return ctx.Err()
}
