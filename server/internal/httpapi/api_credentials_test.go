package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func issueCredential(t *testing.T, f administrationFixture, path, session, body string) access.IssuedCredential {
	t.Helper()
	w := f.call("POST", path, session, body)
	if w.Code != 201 {
		t.Fatalf("create %s: %d %s", path, w.Code, w.Body)
	}
	var out access.IssuedCredential
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func registryErrorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return body.Error.Code
}

func TestAPIKeyIssueReceiptAndAuthenticatesWithinItsScopes(t *testing.T) {
	f := administrationFixtureNew(t)
	request := `{"operationId":"key-create-1","name":"Automation","scopes":["read"]}`
	key := issueCredential(t, f, "/v1/admin/api-keys", f.owner, request)
	if !strings.HasPrefix(key.Secret, identity.APIKeyPrefix) || key.Credential.State != "active" || key.Credential.Kind != "key" || len(key.Credential.Scopes) != 1 {
		t.Fatalf("issued key: %+v", key.Credential)
	}
	if strings.Contains(key.Credential.Hint, key.Secret) || len(key.Credential.Hint) != 6 {
		t.Fatalf("hint reveals too much: %q", key.Credential.Hint)
	}
	// The same request again names the key and cannot show the secret.
	w := f.call("POST", "/v1/admin/api-keys", f.owner, request)
	if w.Code != 201 || strings.Contains(w.Body.String(), key.Secret) || !strings.Contains(w.Body.String(), key.Credential.ID) {
		t.Fatalf("replay: %d %s", w.Code, w.Body)
	}
	// Authentication keeps using the digest; recovery is a separate field and route.
	var stored string
	if err := f.deps.DB.QueryRow(`SELECT secret_hash FROM api_credentials WHERE id=?`, key.Credential.ID).Scan(&stored); err != nil || stored != identity.Digest(key.Secret) || strings.Contains(stored, key.Secret) {
		t.Fatalf("stored secret: %q %v", stored, err)
	}
	var plain int
	if err := f.deps.DB.QueryRow(`SELECT (SELECT count(*) FROM api_credentials WHERE instr(secret_hash||hint||name,?)>0)+(SELECT count(*) FROM authorization_family_tokens WHERE token_hash=?)+(SELECT count(*) FROM access_receipts WHERE instr(body,?)>0)`, key.Secret, key.Secret, key.Secret).Scan(&plain); err != nil || plain != 0 {
		t.Fatalf("the secret is stored somewhere: %d %v", plain, err)
	}
	// A read works and carries the limit headers.
	w = f.call("GET", "/v1/me", key.Secret, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"accountId":"owner"`) {
		t.Fatalf("read with a read key: %d %s", w.Code, w.Body)
	}
	if w.Header().Get("RateLimit-Policy") == "" || w.Header().Get("RateLimit") == "" {
		t.Fatalf("no limit headers: %v", w.Header())
	}
	// Scopes it was not given are refused, on hand-registered and typed routes.
	for _, refused := range []struct{ method, path, body, code string }{
		{"PATCH", "/v1/preferences", `{}`, "insufficient_scope"},
		{"POST", "/v1/libraries/abc/scans", ``, "insufficient_scope"},
		{"GET", "/v1/admin/access/members", ``, "insufficient_scope"},
		{"GET", "/v1/admin/backups", ``, "insufficient_scope"},
		{"POST", "/v1/queues", `{}`, "insufficient_scope"},
		// Session-only whatever the scopes.
		{"GET", "/v1/admin/api-keys", ``, "session_required"},
		{"POST", "/v1/admin/api-keys", `{"operationId":"key-create-2","name":"Wider","scopes":["admin"]}`, "session_required"},
		{"POST", "/v1/me/access-tokens", `{"operationId":"token-create-9","name":"Wider","scopes":["admin"]}`, "session_required"},
		{"DELETE", "/v1/sessions/current", ``, "session_required"},
		{"POST", "/v1/direct/profile-switch", `{}`, "session_required"},
		{"POST", "/v1/quick-connect/decision", `{}`, "session_required"},
		{"POST", "/v1/direct/password", `{}`, "session_required"},
		{"GET", "/v1/devices", ``, "insufficient_scope"},
		{"POST", "/v1/direct/profiles", `{"name":"Kids"}`, "insufficient_scope"},
		{"POST", "/v1/auth/refresh", `{}`, "session_required"},
	} {
		w = f.call(refused.method, refused.path, key.Secret, refused.body)
		if w.Code != 403 || registryErrorCode(t, w) != refused.code {
			t.Fatalf("%s %s with a read key: %d %s", refused.method, refused.path, w.Code, w.Body)
		}
	}
	// It is listed with a last-used time only after the use is written; the
	// ordinary list never carries the secret.
	w = f.call("GET", "/v1/admin/api-keys", f.owner, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"name":"Automation"`) || strings.Contains(w.Body.String(), key.Secret) {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	// Revoked is refused at once, and revoking again is not an error.
	for range 2 {
		w = f.call("DELETE", "/v1/admin/api-keys/"+key.Credential.ID, f.owner, "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"revoked"`) {
			t.Fatalf("revoke: %d %s", w.Code, w.Body)
		}
	}
	if w = f.call("GET", "/v1/me", key.Secret, ""); w.Code != 401 {
		t.Fatalf("revoked key still authenticates: %d %s", w.Code, w.Body)
	}
}

func TestCredentialScopeNeverRaisesTheMemberBehindIt(t *testing.T) {
	f := administrationFixtureNew(t)
	// The owner's admin key administers.
	key := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"key-admin-1","name":"Console script","scopes":["admin"]}`)
	if w := f.call("GET", "/v1/admin/access/members", key.Secret, ""); w.Code != 200 {
		t.Fatalf("admin key on an admin route: %d %s", w.Code, w.Body)
	}
	// A member may hold every scope and is still a member.
	token := issueCredential(t, f, "/v1/me/access-tokens", f.member, `{"operationId":"token-create-1","name":"My script","scopes":["read","playback","library","admin"]}`)
	if !strings.HasPrefix(token.Secret, identity.AccessTokenPrefix) || token.Credential.Member.AccountID != "member" {
		t.Fatalf("issued token: %+v", token.Credential)
	}
	if w := f.call("GET", "/v1/me", token.Secret, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"accountId":"member"`) {
		t.Fatalf("member token read: %d %s", w.Code, w.Body)
	}
	for _, path := range []string{"/v1/admin/access/members", "/v1/admin/backups", "/v1/admin/logs"} {
		if w := f.call("GET", path, token.Secret, ""); w.Code != 403 {
			t.Fatalf("member token reached %s: %d %s", path, w.Code, w.Body)
		}
	}
	// A member cannot make an owner key, see other members' tokens, or end one
	// that is not theirs.
	if w := f.call("POST", "/v1/admin/api-keys", f.member, `{"operationId":"key-member-1","name":"Nope","scopes":["read"]}`); w.Code != 403 {
		t.Fatalf("member made an API key: %d %s", w.Code, w.Body)
	}
	if w := f.call("GET", "/v1/admin/access-tokens", f.member, ""); w.Code != 404 {
		t.Fatalf("member listed everyone's tokens: %d %s", w.Code, w.Body)
	}
	other := issueCredential(t, f, "/v1/me/access-tokens", f.admin, `{"operationId":"token-create-2","name":"Admin script","scopes":["read"]}`)
	if w := f.call("DELETE", "/v1/me/access-tokens/"+other.Credential.ID, f.member, ""); w.Code != 404 {
		t.Fatalf("member ended another member's token: %d %s", w.Code, w.Body)
	}
	if w := f.call("GET", "/v1/me/access-tokens", f.member, ""); w.Code != 200 || strings.Contains(w.Body.String(), other.Credential.ID) || !strings.Contains(w.Body.String(), token.Credential.ID) {
		t.Fatalf("member's own list: %d %s", w.Code, w.Body)
	}
	// Personal token inventory belongs only to its member, including against
	// owner credentials. The retired administration routes expose no inventory.
	if w := f.call("GET", "/v1/admin/access-tokens", f.owner, ""); w.Code != 404 || strings.Contains(w.Body.String(), token.Credential.ID) || strings.Contains(w.Body.String(), other.Credential.ID) {
		t.Fatalf("retired owner token inventory: %d %s", w.Code, w.Body)
	}
	if w := f.call("DELETE", "/v1/admin/access-tokens/"+token.Credential.ID, f.owner, ""); w.Code != 404 {
		t.Fatalf("retired owner token deletion: %d %s", w.Code, w.Body)
	}
	if w := f.call("GET", "/v1/me", token.Secret, ""); w.Code != 200 {
		t.Fatalf("a retired route changed a member's token: %d %s", w.Code, w.Body)
	}
	if w := f.call("DELETE", "/v1/me/access-tokens/"+token.Credential.ID, f.member, ""); w.Code != 200 {
		t.Fatalf("member could not end their own token: %d %s", w.Code, w.Body)
	}
	if w := f.call("GET", "/v1/me", token.Secret, ""); w.Code != 401 {
		t.Fatalf("ended token still works: %d", w.Code)
	}
}

func TestCredentialExpiresWhenItsMakerChose(t *testing.T) {
	f := administrationFixtureNew(t)
	expiring := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
	token := issueCredential(t, f, "/v1/me/access-tokens", f.member, `{"operationId":"token-expiry-1","name":"Short","scopes":["read"],"expiresAt":"`+expiring+`"}`)
	lasting := issueCredential(t, f, "/v1/me/access-tokens", f.member, `{"operationId":"token-expiry-2","name":"Long","scopes":["read"]}`)
	if token.Credential.ExpiresAt == "" || lasting.Credential.ExpiresAt != "" {
		t.Fatalf("expiry: %q %q", token.Credential.ExpiresAt, lasting.Credential.ExpiresAt)
	}
	for _, bad := range []string{`"2001-01-01T00:00:00Z"`, `"soon"`, `"2999-01-01T00:00:00Z"`} {
		if w := f.call("POST", "/v1/me/access-tokens", f.member, `{"operationId":"token-expiry-bad","name":"Bad","scopes":["read"],"expiresAt":`+bad+`}`); w.Code != 400 {
			t.Fatalf("expiry %s accepted: %d %s", bad, w.Code, w.Body)
		}
	}
	for _, bad := range []string{`[]`, `["read","read"]`, `["everything"]`, `["session"]`, `["open"]`} {
		if w := f.call("POST", "/v1/me/access-tokens", f.member, `{"operationId":"token-scope-bad","name":"Bad","scopes":`+bad+`}`); w.Code != 400 {
			t.Fatalf("scopes %s accepted: %d %s", bad, w.Code, w.Body)
		}
	}
	// Past its expiry the store refuses it; so does the family's own horizon.
	store := f.deps.Access.Access
	real := store.Now
	store.Now = func() time.Time { return time.Now().Add(3 * time.Hour) }
	dbwork.BumpAuthority()
	if w := f.call("GET", "/v1/me", token.Secret, ""); w.Code != 401 {
		t.Fatalf("expired token works: %d %s", w.Code, w.Body)
	}
	store.Now = real
	if _, err := f.deps.DB.Exec(`UPDATE authorization_session_families SET authorization_horizon='2001-01-01T00:00:00Z' WHERE id=(SELECT family_id FROM api_credentials WHERE id=?)`, token.Credential.ID); err != nil {
		t.Fatal(err)
	}
	dbwork.BumpAuthority()
	if w := f.call("GET", "/v1/me", token.Secret, ""); w.Code != 401 {
		t.Fatalf("token outlived its family horizon: %d %s", w.Code, w.Body)
	}
	if w := f.call("GET", "/v1/me", lasting.Secret, ""); w.Code != 200 {
		t.Fatalf("a token with no expiry: %d %s", w.Code, w.Body)
	}
}

// endSessions does what every "end this member's sessions" path does: revoke
// the member's families for a reason and move the account's epoch.
func endSessions(t *testing.T, f administrationFixture, account string, reason identity.RevocationReason) {
	t.Helper()
	err := dbwork.WithWriteTx(context.Background(), f.deps.DB, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		if e := identity.RevokeFamiliesMatchingTx(context.Background(), tx, reason, `authority='local' AND account_id=?`, account); e != nil {
			return e
		}
		_, e := tx.Exec(`UPDATE accounts SET epoch=epoch+1 WHERE id=?`, account)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (f administrationFixture) session(t *testing.T, account, role string) string {
	t.Helper()
	var epoch int
	if err := f.deps.DB.QueryRow(`SELECT epoch FROM accounts WHERE id=?`, account).Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	out, err := f.deps.Identity.Issue(account, account+"-profile", "local", role, epoch)
	if err != nil {
		t.Fatal(err)
	}
	return out.AccessToken
}

func TestPasswordChangeAndSignOutEverywhereKeepKeysWorking(t *testing.T) {
	f := administrationFixtureNew(t)
	// Sessions made before and after the credentials, so the statement that
	// ends them meets a credential in the middle of its rows.
	before := f.session(t, "owner", "owner")
	key := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"key-lasting-1","name":"Download manager","scopes":["read","library"]}`)
	token := issueCredential(t, f, "/v1/me/access-tokens", f.owner, `{"operationId":"token-lasting-1","name":"Script","scopes":["admin"]}`)
	after := f.session(t, "owner", "owner")
	works := func(when string) {
		t.Helper()
		if w := f.call("GET", "/v1/me", key.Secret, ""); w.Code != 200 {
			t.Fatalf("%s: the key stopped working: %d %s", when, w.Code, w.Body)
		}
		if w := f.call("GET", "/v1/admin/access/members", token.Secret, ""); w.Code != 200 {
			t.Fatalf("%s: the token stopped working: %d %s", when, w.Code, w.Body)
		}
	}
	works("at first")
	// The owner changes their password: every session ends, on every device.
	endSessions(t, f, "owner", identity.RevokedPasswordChange)
	for name, session := range map[string]string{"the first session": f.owner, "a session made before the key": before, "a session made after the key": after} {
		if w := f.call("GET", "/v1/me", session, ""); w.Code != 401 {
			t.Fatalf("%s survived a password change: %d %s", name, w.Code, w.Body)
		}
	}
	works("after a password change")
	// The owner signs in again and signs out everywhere, through the real path.
	again := f.session(t, "owner", "owner")
	if err := f.deps.Identity.SignOutEverywhere(context.Background(), again); err != nil {
		t.Fatalf("sign out everywhere: %v", err)
	}
	if w := f.call("GET", "/v1/me", again, ""); w.Code != 401 {
		t.Fatalf("a session survived sign out everywhere: %d %s", w.Code, w.Body)
	}
	works("after signing out everywhere")
	// They are still listed as working and can still be revoked one at a time.
	fresh := f.session(t, "owner", "owner")
	w := f.call("GET", "/v1/admin/api-keys", fresh, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"active"`) || strings.Contains(w.Body.String(), `"state":"ended"`) {
		t.Fatalf("list after ending sessions: %d %s", w.Code, w.Body)
	}
	if w = f.call("DELETE", "/v1/admin/api-keys/"+key.Credential.ID, fresh, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"revoked"`) {
		t.Fatalf("revoke after ending sessions: %d %s", w.Code, w.Body)
	}
	if w = f.call("GET", "/v1/me", key.Secret, ""); w.Code != 401 {
		t.Fatalf("a revoked key works: %d %s", w.Code, w.Body)
	}
	if w = f.call("GET", "/v1/me", token.Secret, ""); w.Code != 200 {
		t.Fatalf("revoking one credential ended another: %d %s", w.Code, w.Body)
	}
	// A key's own entry is not among the member's devices, where "sign out"
	// and "forget" would act on it by accident.
	var listed int
	if err := f.deps.DB.QueryRow(`SELECT count(*) FROM identity_devices WHERE platform='api' AND id IN(SELECT device_id FROM api_credentials)`).Scan(&listed); err != nil || listed == 0 {
		t.Fatalf("credential devices: %d %v", listed, err)
	}
	if w = f.call("GET", "/v1/devices", fresh, ""); w.Code == 200 && strings.Contains(w.Body.String(), "Script") {
		t.Fatalf("a token is listed among devices: %s", w.Body)
	}
}

func TestDisablingOrDeletingTheMemberEndsTheirKeysForGood(t *testing.T) {
	f := administrationFixtureNew(t)
	token := issueCredential(t, f, "/v1/me/access-tokens", f.member, `{"operationId":"token-disable-1","name":"Member script","scopes":["read"]}`)
	other := issueCredential(t, f, "/v1/me/access-tokens", f.admin, `{"operationId":"token-disable-2","name":"Admin script","scopes":["read"]}`)
	if w := f.call("GET", "/v1/me", token.Secret, ""); w.Code != 200 {
		t.Fatalf("before: %d %s", w.Code, w.Body)
	}
	// The owner disables the member, as the member editor does.
	endSessions(t, f, "member", identity.RevokedMembershipRemoved)
	if _, err := f.deps.DB.Exec(`UPDATE direct_memberships SET disabled=1,revision=revision+1 WHERE account_id='member'`); err != nil {
		t.Fatal(err)
	}
	dbwork.BumpAuthority()
	if w := f.call("GET", "/v1/me", token.Secret, ""); w.Code != 401 {
		t.Fatalf("a disabled member's token works: %d %s", w.Code, w.Body)
	}
	w := f.call("GET", "/v1/admin/access-tokens?accountId=member", f.owner, "")
	if w.Code != 404 || strings.Contains(w.Body.String(), token.Credential.ID) {
		t.Fatalf("retired inventory after disabling: %d %s", w.Code, w.Body)
	}
	// Enabling the member again does not bring the token back.
	if _, err := f.deps.DB.Exec(`UPDATE direct_memberships SET disabled=0,revision=revision+1 WHERE account_id='member'`); err != nil {
		t.Fatal(err)
	}
	endSessions(t, f, "member", identity.RevokedAdminRevoke)
	if w = f.call("GET", "/v1/me", token.Secret, ""); w.Code != 401 {
		t.Fatalf("re-enabling the member revived their token: %d %s", w.Code, w.Body)
	}
	// Another member's token was not touched.
	if w = f.call("GET", "/v1/me", other.Secret, ""); w.Code != 200 {
		t.Fatalf("disabling one member ended another's token: %d %s", w.Code, w.Body)
	}
	// Deleting a member takes their tokens with them.
	if _, err := f.deps.DB.Exec(`DELETE FROM accounts WHERE id='admin'`); err != nil {
		t.Fatal(err)
	}
	dbwork.BumpAuthority()
	if w = f.call("GET", "/v1/me", other.Secret, ""); w.Code != 401 {
		t.Fatalf("a deleted member's token works: %d %s", w.Code, w.Body)
	}
	var left int
	if err := f.deps.DB.QueryRow(`SELECT count(*) FROM api_credentials WHERE account_id='admin'`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("a deleted member's tokens are still stored: %d %v", left, err)
	}
}

func TestACredentialHoldsTheRoleItsMemberHoldsNow(t *testing.T) {
	f := administrationFixtureNew(t)
	token := issueCredential(t, f, "/v1/me/access-tokens", f.admin, `{"operationId":"token-role-1","name":"Admin script","scopes":["admin"]}`)
	narrow := issueCredential(t, f, "/v1/me/access-tokens", f.admin, `{"operationId":"token-role-2","name":"Admin reader","scopes":["read"]}`)
	if w := f.call("GET", "/v1/admin/access/members", token.Secret, ""); w.Code != 200 {
		t.Fatalf("an administrator's token: %d %s", w.Code, w.Body)
	}
	setRole := func(account, role string) {
		t.Helper()
		if _, err := f.deps.DB.Exec(`UPDATE direct_memberships SET role=?,revision=revision+1 WHERE account_id=?`, role, account); err != nil {
			t.Fatal(err)
		}
		endSessions(t, f, account, identity.RevokedMembershipRemoved)
	}
	// Demoted: the token keeps working as a member's and loses administration.
	setRole("admin", "member")
	if w := f.call("GET", "/v1/me", token.Secret, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"role":"member"`) {
		t.Fatalf("a demoted member's token: %d %s", w.Code, w.Body)
	}
	if w := f.call("GET", "/v1/admin/access/members", token.Secret, ""); w.Code != 403 {
		t.Fatalf("a demoted member's token still administers: %d %s", w.Code, w.Body)
	}
	// Promoted again: the token can do what its member can, within its scopes,
	// and a token that was given less is still held to less.
	setRole("admin", "admin")
	if w := f.call("GET", "/v1/admin/access/members", token.Secret, ""); w.Code != 200 {
		t.Fatalf("a promoted member's admin token: %d %s", w.Code, w.Body)
	}
	if w := f.call("GET", "/v1/admin/access/members", narrow.Secret, ""); w.Code != 403 || registryErrorCode(t, w) != "insufficient_scope" {
		t.Fatalf("a read token administers after a promotion: %d %s", w.Code, w.Body)
	}
}

func TestCredentialIsNeverAnUnscopedSession(t *testing.T) {
	f := administrationFixtureNew(t)
	key := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"key-shape-1","name":"Shape","scopes":["read"]}`)
	// Authenticated anywhere but through the gate, the secret is not a session.
	if _, err := f.deps.Identity.Authenticate(key.Secret); !errors.Is(err, identity.ErrUnauthorized) {
		t.Fatalf("a key authenticated without the gate: %v", err)
	}
	// A stamp for one credential does not vouch for another secret.
	stamped := identity.WithAPICredential(context.Background(), identity.APICredential{ID: "other", Digest: identity.Digest("pk.someone-elses-secret-that-is-long-enough-000")})
	if _, err := f.deps.Identity.AuthenticateContext(stamped, key.Secret); !errors.Is(err, identity.ErrUnauthorized) {
		t.Fatalf("a foreign stamp admitted the key: %v", err)
	}
	if err := f.deps.Identity.LogoutToken(context.Background(), key.Secret); !errors.Is(err, identity.ErrUnauthorized) {
		t.Fatalf("a key signed itself out: %v", err)
	}
	// Two Authorization headers are never read two ways.
	r := httptest.NewRequest("GET", "/v1/me", nil)
	r.Header.Add("Authorization", "Bearer "+f.owner)
	r.Header.Add("Authorization", "Bearer "+key.Secret)
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("ambiguous credentials were accepted: %d %s", w.Code, w.Body)
	}
	// Unknown and malformed secrets are 401, never treated as a session.
	for _, secret := range []string{"pk.unknown-unknown-unknown-unknown-unknown-unknown", "pat.", "x.y", strings.Repeat("a", 300) + "."} {
		if w = f.call("GET", "/v1/me", secret, ""); w.Code != 401 {
			t.Fatalf("%q: %d %s", secret, w.Code, w.Body)
		}
	}
	// A session token cannot be mistaken for a credential.
	if identity.CredentialShaped(f.owner) || !identity.CredentialShaped(key.Secret) {
		t.Fatal("credential shape is not distinct from a session token")
	}
	// A route nothing has classified is closed to credentials.
	if f.deps.patternScope("GET /v1/not-yet-classified", "GET", "/v1/not-yet-classified") != apikit.ScopeSession {
		t.Fatal("an unclassified route is open to credentials")
	}
}

func TestPermittedCredentialIsActiveWithoutDeviceApproval(t *testing.T) {
	f := administrationFixtureNew(t)
	token := issueCredential(t, f, "/v1/me/access-tokens", f.member, `{"operationId":"token-no-approval","name":"Personal","scopes":["read"]}`)
	if token.Credential.State != "active" {
		t.Fatalf("state %q", token.Credential.State)
	}
	if w := f.call("GET", "/v1/me", token.Secret, ""); w.Code != 200 {
		t.Fatalf("permitted token refused: %d %s", w.Code, w.Body)
	}
}

func TestCredentialGateHoldsOneCredentialToItsRateAndShare(t *testing.T) {
	gate := newCredentialGate()
	now := time.Now()
	for i := range 5 {
		if _, _, ok := gate.take("a", 5, now); !ok {
			t.Fatalf("request %d refused inside the limit", i)
		}
	}
	if _, wait, ok := gate.take("a", 5, now); ok || wait <= 0 || wait > 13*time.Second {
		t.Fatalf("sixth request: ok=%v wait=%v", ok, wait)
	}
	if _, _, ok := gate.take("b", 5, now); !ok {
		t.Fatal("one credential's spending refused another")
	}
	if _, _, ok := gate.take("a", 5, now.Add(13*time.Second)); !ok {
		t.Fatal("the bucket did not refill")
	}
	for i := range credentialConcurrency {
		if !gate.enter("a", false) {
			t.Fatalf("request %d refused inside the share", i)
		}
	}
	if gate.enter("a", false) {
		t.Fatal("a credential exceeded its share of the server")
	}
	if !gate.enter("b", false) {
		t.Fatal("one credential's share refused another")
	}
	gate.leave("a", false)
	if !gate.enter("a", false) {
		t.Fatal("a released slot was not returned")
	}
	// Credentials together never take more than their global share.
	wide := newCredentialGate()
	admitted := 0
	for i := range 100 {
		if wide.enter(string(rune('a'+i%26))+string(rune('a'+i/26)), false) {
			admitted++
		}
	}
	if admitted != credentialConcurrencyGlobal {
		t.Fatalf("credentials together hold %d slots, want %d", admitted, credentialConcurrencyGlobal)
	}
	for range credentialStreams {
		if !gate.enter("a", true) {
			t.Fatal("a stream was refused inside the share")
		}
	}
	if gate.enter("a", true) {
		t.Fatal("a credential exceeded its share of streams")
	}
}

func TestCredentialRateLimitAnswers429WithRetryAfter(t *testing.T) {
	f := administrationFixtureNew(t)
	key := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"key-rate-1","name":"Busy","scopes":["read"]}`)
	if w := f.call("GET", "/v1/me", key.Secret, ""); w.Code != 200 {
		t.Fatalf("first request: %d %s", w.Code, w.Body)
	}
	f.deps.credentials.mu.Lock()
	f.deps.credentials.buckets[key.Credential.ID].tokens = 0
	f.deps.credentials.buckets[key.Credential.ID].at = time.Now()
	f.deps.credentials.mu.Unlock()
	w := f.call("GET", "/v1/me", key.Secret, "")
	if w.Code != 429 || registryErrorCode(t, w) != "rate_limited" || w.Header().Get("Retry-After") == "" {
		t.Fatalf("over the limit: %d %v %s", w.Code, w.Header(), w.Body)
	}
	// The owner's own session is not charged to the key.
	if w = f.call("GET", "/v1/me", f.owner, ""); w.Code != 200 {
		t.Fatalf("a busy key refused its owner's session: %d", w.Code)
	}
}

// Every route this package registers must say what a key or token needs to use
// it: on the typed registry, or in routeScopes. A missing entry is refused to
// credentials at run time; this is what makes someone decide.
func TestEveryRouteDeclaresACredentialScope(t *testing.T) {
	registry, err := FoundationRegistry(Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	typed := map[string]apikit.Scope{}
	for _, route := range registry.Definitions() {
		if !apikit.ValidScope(route.Requires) {
			t.Errorf("%s %s declares no credential scope", route.Method, route.Path)
		}
		typed[route.Method+" "+route.Path] = route.Requires
		// Adapter-owned patterns retain the canonical declaration's scope.
		// patternScope resolves this same muxPattern through Registry.Lookup.
		mounted := muxPattern(route.Method, route.Path)
		if previous, present := typed[mounted]; present && previous != route.Requires {
			t.Errorf("mounted routes disagree on credential scope: %s", mounted)
		}
		typed[mounted] = route.Requires
		if legacy, ok := routeScopes[muxPattern(route.Method, route.Path)]; ok && legacy != route.Requires {
			t.Errorf("%s %s: routeScopes says %s, the registry %s", route.Method, route.Path, legacy, route.Requires)
		}
	}
	literal := regexp.MustCompile(`"((?:GET|POST|PUT|PATCH|DELETE|HEAD) /[^"]*)"`)
	missing := map[string]bool{}
	check := func(route string) {
		if strings.HasSuffix(route, "/") {
			return // a prefix a route is concatenated onto; the whole routes are listed
		}
		if _, ok := routeScopes[route]; ok {
			return
		}
		if _, ok := typed[route]; ok {
			return
		}
		missing[route] = true
	}
	for _, dir := range []string{".", "../networking"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") || entry.Name() == "route_scopes.go" {
				continue
			}
			data, err := os.ReadFile(dir + "/" + entry.Name())
			if err != nil {
				t.Fatal(err)
			}
			if developmentOnlySource(data) {
				continue
			}
			for _, match := range literal.FindAllStringSubmatch(string(data), -1) {
				check(match[1])
			}
		}
	}
	for _, route := range composedRoutes {
		check(route)
	}
	for route := range routeLanes {
		if strings.Contains(route, " /") {
			check(route)
		}
	}
	for route := range missing {
		t.Errorf("route has no credential scope: %s", route)
	}
	for route, scope := range routeScopes {
		if !apikit.ValidScope(scope) {
			t.Errorf("%s has an unknown scope %q", route, scope)
		}
	}
}

func TestScopesDoNotIncludeOneAnotherExceptAdmin(t *testing.T) {
	read := []apikit.Scope{apikit.ScopeRead}
	if !apikit.ScopeAllows(read, apikit.ScopeRead) || !apikit.ScopeAllows(read, apikit.ScopeOpen) {
		t.Fatal("read does not allow read")
	}
	for _, need := range []apikit.Scope{apikit.ScopePlayback, apikit.ScopeLibrary, apikit.ScopeAdmin, apikit.ScopeSession, "", "nonsense"} {
		if apikit.ScopeAllows(read, need) {
			t.Fatalf("read allows %q", need)
		}
	}
	admin := []apikit.Scope{apikit.ScopeAdmin}
	for _, need := range []apikit.Scope{apikit.ScopeRead, apikit.ScopePlayback, apikit.ScopeLibrary, apikit.ScopeAdmin} {
		if !apikit.ScopeAllows(admin, need) {
			t.Fatalf("admin does not allow %q", need)
		}
	}
	if apikit.ScopeAllows(admin, apikit.ScopeSession) || apikit.ScopeAllows(nil, apikit.ScopeRead) {
		t.Fatal("session-only or empty grants were allowed")
	}
	if apikit.Grantable(apikit.ScopeSession) || apikit.Grantable(apikit.ScopeOpen) {
		t.Fatal("session or open can be granted")
	}
	var _ = http.MethodGet
}

// A key at admin scope does the member's own account work (profiles, members,
// devices, sessions) as the member, from a header or from the address of a
// GET; it still never signs in, changes a password or makes a key.
func TestAdminKeyManagesTheAccountButNeverCredentials(t *testing.T) {
	f := administrationFixtureNew(t)
	key := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"key-account-1","name":"House script","scopes":["admin"]}`)
	for _, allowed := range []struct{ method, path, body string }{
		{"GET", "/v1/direct", ``},
		{"POST", "/v1/direct/profiles", `{"name":"Kids"}`},
		{"GET", "/v1/direct/members", ``},
		{"GET", "/v1/devices", ``},
		{"GET", "/v1/direct/sessions", ``},
		{"GET", "/v1/admin/access/devices", ``},
	} {
		if w := f.call(allowed.method, allowed.path, key.Secret, allowed.body); w.Code >= 300 {
			t.Fatalf("%s %s with an admin key: %d %s", allowed.method, allowed.path, w.Code, w.Body)
		}
	}
	for _, refused := range []struct{ method, path, body string }{
		{"POST", "/v1/admin/api-keys", `{"operationId":"key-account-2","name":"Wider","scopes":["admin"]}`},
		{"GET", "/v1/me/access-tokens", ``},
		{"POST", "/v1/direct/password", `{}`},
		{"POST", "/v1/direct/two-factor/enrol", `{}`},
		{"POST", "/v1/direct/two-factor/recovery-codes", `{}`},
		{"POST", "/v1/direct/ownership", `{}`},
		{"POST", "/v1/direct/sign-in", `{}`},
		{"POST", "/v1/direct/profiles/x/select", `{}`},
		{"POST", "/v1/auth/remembered-accounts", `{}`},
	} {
		if w := f.call(refused.method, refused.path, key.Secret, refused.body); w.Code != 403 || registryErrorCode(t, w) != "session_required" {
			t.Fatalf("%s %s with an admin key: %d %s", refused.method, refused.path, w.Code, w.Body)
		}
	}
	// The same key in the address of a GET.
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/me?apiKey="+key.Secret, nil)
	f.handler.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"accountId":"owner"`) || strings.Contains(w.Body.String(), key.Secret) {
		t.Fatalf("a key in the address: %d %s", w.Code, w.Body)
	}
	// From another site's page too: the key in the address is the token.
	w = httptest.NewRecorder()
	r = httptest.NewRequest("GET", "/v1/me?apiKey="+key.Secret, nil)
	r.Header.Set("Origin", "https://tools.example")
	f.handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("a key in the address from another origin: %d %s", w.Code, w.Body)
	}
	// Not on a change, and not a session token.
	w = httptest.NewRecorder()
	f.handler.ServeHTTP(w, httptest.NewRequest("POST", "/v1/direct/profiles?apiKey="+key.Secret, strings.NewReader(`{"name":"Guests"}`)))
	if w.Code != 401 {
		t.Fatalf("a key in the address of a POST: %d %s", w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	f.handler.ServeHTTP(w, httptest.NewRequest("GET", "/v1/me?apiKey="+f.owner, nil))
	if w.Code != 401 {
		t.Fatalf("a session token in the address: %d %s", w.Code, w.Body)
	}
}

func TestQueryCredentialLeavesTheAddress(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/items/x/artwork?b=2&apiKey=pk.secret-value&a=1", nil)
	out := liftQueryCredential(r)
	if strings.Contains(out.URL.RawQuery, "secret") || strings.Contains(out.RequestURI, "secret") || out.URL.RawQuery != "b=2&a=1" {
		t.Fatalf("address after lifting: %q %q", out.URL.RawQuery, out.RequestURI)
	}
	if out.Header.Get("Authorization") != "Bearer pk.secret-value" || r.Header.Get("Authorization") != "" {
		t.Fatalf("header: %q (original %q)", out.Header.Get("Authorization"), r.Header.Get("Authorization"))
	}
	post := liftQueryCredential(httptest.NewRequest("POST", "/v1/x?apiKey=pk.secret-value", nil))
	if post.Header.Get("Authorization") != "" || strings.Contains(post.URL.RawQuery, "secret") {
		t.Fatalf("a POST kept the key: %q %q", post.Header.Get("Authorization"), post.URL.RawQuery)
	}
	if plain := httptest.NewRequest("GET", "/v1/x?a=1", nil); liftQueryCredential(plain) != plain {
		t.Fatal("a request with no key was copied")
	}
}
