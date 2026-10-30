package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func integrationFixture(t *testing.T) administrationFixture {
	t.Helper()
	f := administrationFixtureNew(t)
	if _, err := f.deps.DB.Exec(`INSERT INTO server_settings(singleton,"integrations.compatibilityEnabled") VALUES(1,1) ON CONFLICT(singleton) DO UPDATE SET "integrations.compatibilityEnabled"=1`); err != nil {
		t.Fatal(err)
	}
	return f
}
func newApp(t *testing.T, f administrationFixture, name string) access.IssuedAppPassword {
	t.Helper()
	b, _ := json.Marshal(access.AppPasswordRequest{CredentialRequest: access.CredentialRequest{OperationID: "app-" + name, Name: name, Scopes: []string{"read"}}, AccountID: "member", ProfileID: "member-profile"})
	w := f.call("POST", "/v1/integrations/app-passwords", f.owner, string(b))
	if w.Code != 201 {
		t.Fatalf("app create: %d %s", w.Code, w.Body)
	}
	var app access.IssuedAppPassword
	if err := json.Unmarshal(w.Body.Bytes(), &app); err != nil {
		t.Fatal(err)
	}
	return app
}
func appLogin(t *testing.T, f administrationFixture, app access.IssuedAppPassword) IntegrationToken {
	t.Helper()
	b, _ := json.Marshal(IntegrationLogin{Username: "member", Password: app.Secret, Name: app.AppPassword.Name})
	w := f.call("POST", "/v1/integrations/authenticate", "", string(b))
	if w.Code != 201 {
		t.Fatalf("app login: %d %s", w.Code, w.Body)
	}
	var out IntegrationToken
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func TestIntegrationAppPasswordAuthorityRecoveryAndRevocation(t *testing.T) {
	f := integrationFixture(t)
	app := newApp(t, f, "first")
	if !strings.HasPrefix(app.Secret, "papp.") || app.AppPassword.AccountID != "member" {
		t.Fatal("wrong app binding")
	}
	path := "/v1/integrations/app-passwords/" + app.AppPassword.ID + "/secret"
	for _, token := range []string{f.member, f.admin, ""} {
		if w := f.call("GET", path, token, ""); w.Code == 200 {
			t.Fatal("non-owner recovered app secret")
		}
	}
	for range 2 {
		w := f.call("GET", path, f.owner, "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), app.Secret) {
			t.Fatalf("owner reveal: %d %s", w.Code, w.Body)
		}
	}
	if w := f.call("GET", "/v1/me", app.Secret, ""); w.Code != 401 {
		t.Fatal("app password used as native bearer")
	}
	login := appLogin(t, f, app)
	if w := f.call("GET", "/v1/me", login.Token, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"accountId":"member"`) {
		t.Fatalf("token authority: %d %s", w.Code, w.Body)
	}
	for _, secret := range []string{f.owner, f.member, "real-account-password"} {
		b, _ := json.Marshal(IntegrationLogin{Username: "member", Password: secret})
		if w := f.call("POST", "/v1/integrations/authenticate", "", string(b)); w.Code != 401 {
			t.Fatalf("non-app secret accepted: %d", w.Code)
		}
	}
	if w := f.call("GET", path, login.Token, ""); w.Code != 403 {
		t.Fatal("integration token revealed owner-managed app password")
	}
	if w := f.call("DELETE", "/v1/integrations/app-passwords/"+app.AppPassword.ID, f.owner, ""); w.Code != 200 {
		t.Fatalf("revoke: %d %s", w.Code, w.Body)
	}
	if w := f.call("GET", "/v1/me", login.Token, ""); w.Code != 401 {
		t.Fatalf("revoked descendant: %d %s", w.Code, w.Body)
	}
	b, _ := json.Marshal(IntegrationLogin{Username: "member", Password: app.Secret})
	if w := f.call("POST", "/v1/integrations/authenticate", "", string(b)); w.Code != 401 {
		t.Fatal("revoked app authenticated")
	}
}
func TestIntegrationKeyRecoveryIsConfinedToOriginalAppGrant(t *testing.T) {
	f := integrationFixture(t)
	first := newApp(t, f, "first")
	second := newApp(t, f, "second")
	one := appLogin(t, f, first)
	two := appLogin(t, f, second)
	// Each POST mints only the current app grant, never owner authority.
	w := f.call("POST", "/v1/integrations/keys", one.Token, `{"name":"Seerr"}`)
	if w.Code != 201 {
		t.Fatalf("mint: %d %s", w.Code, w.Body)
	}
	var made access.IssuedCredential
	_ = json.Unmarshal(w.Body.Bytes(), &made)
	owner := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"owner-private","name":"Owner private","scopes":["admin"]}`)
	w = f.call("GET", "/v1/integrations/keys", one.Token, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), made.Secret) || strings.Contains(w.Body.String(), owner.Secret) || strings.Contains(w.Body.String(), two.Token) {
		t.Fatalf("own key list: %d %s", w.Code, w.Body)
	}
	w = f.call("GET", "/v1/integrations/keys", two.Token, "")
	if w.Code != 200 || strings.Contains(w.Body.String(), made.Secret) || strings.Contains(w.Body.String(), one.Token) {
		t.Fatalf("cross-grant leak: %d %s", w.Code, w.Body)
	}
	if _, err := f.deps.DB.Exec(`UPDATE server_settings SET "integrations.compatibilityEnabled"=0`); err != nil {
		t.Fatal(err)
	}
	if w = f.call("GET", "/v1/me", one.Token, ""); w.Code != 401 {
		t.Fatalf("disabled integration token accepted: %d", w.Code)
	}
}
func TestOwnerAPIKeyRecoveryIsExplicitAndSessionOnly(t *testing.T) {
	f := administrationFixtureNew(t)
	key := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"recover-key","name":"Recoverable","scopes":["read"]}`)
	path := "/v1/admin/api-keys/" + key.Credential.ID + "/secret"
	for range 2 {
		w := f.call("GET", path, f.owner, "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), key.Secret) {
			t.Fatalf("recover: %d %s", w.Code, w.Body)
		}
	}
	for _, token := range []string{f.member, f.admin, key.Secret} {
		if w := f.call("GET", path, token, ""); w.Code == 200 {
			t.Fatal("non-owner-session reveal")
		}
	}
	if w := f.call("GET", "/v1/admin/api-keys", f.owner, ""); strings.Contains(w.Body.String(), key.Secret) {
		t.Fatal("ordinary list unexpectedly includes secret")
	}
	var plain, hash string
	if err := f.deps.DB.QueryRow(`SELECT secret,secret_hash FROM api_credentials WHERE id=?`, key.Credential.ID).Scan(&plain, &hash); err != nil || plain != key.Secret || hash != identity.Digest(key.Secret) {
		t.Fatal("recoverable storage/lookup mismatch")
	}
}
func TestCompatibilityHeaderNormalization(t *testing.T) {
	r := httptest.NewRequest("GET", "http://local/Items?api_key=pk.test&id=1", nil)
	r.Header.Set("Authorization", `MediaBrowser Client="Tool", Device="Local", Token="pk.test"`)
	r.Header.Set("X-Emby-Token", "pk.test")
	out := normalizeCompatibilityCredential(r)
	if out.Header.Get("Authorization") != "Bearer pk.test" || strings.Contains(out.RequestURI, "pk.test") || out.URL.Query().Get("api_key") != "" {
		t.Fatal("proof not normalized/redacted")
	}
	r.Header.Set("X-Emby-Token", "pk.other")
	if _, present, ambiguous := presentedAPICredential(normalizeCompatibilityCredential(r)); !present || !ambiguous {
		t.Fatal("conflicting proof accepted")
	}
}

func TestIntegrationRepeatedLoginsDoNotConsumeOrRevokeNamedKeys(t *testing.T) {
	f := integrationFixture(t)
	app := newApp(t, f, "recurring")
	first := appLogin(t, f, app)
	w := f.call("POST", "/v1/integrations/keys", first.Token, `{"name":"Persistent tool"}`)
	if w.Code != 201 {
		t.Fatalf("mint: %d %s", w.Code, w.Body)
	}
	var key access.IssuedCredential
	if err := json.Unmarshal(w.Body.Bytes(), &key); err != nil {
		t.Fatal(err)
	}
	if key.Credential.Kind != "key" || !strings.HasPrefix(key.Secret, "pk.") || key.Credential.ExpiresAt != "" {
		t.Fatal("derived named key must follow the native key model and app grant lifetime")
	}
	for range access.MaxAccessTokens {
		appLogin(t, f, app)
	}
	if w = f.call("GET", "/v1/me", key.Secret, ""); w.Code != 200 {
		t.Fatalf("repeated login retired a named key: %d", w.Code)
	}
	var count int
	if err := f.deps.DB.QueryRow(`SELECT count(*) FROM api_credentials WHERE parent_id=? AND kind='integration_session' AND revoked_ms=0`, app.AppPassword.ID).Scan(&count); err != nil || count != access.MaxAccessTokens {
		t.Fatalf("unbounded login sessions: %d %v", count, err)
	}
	w = f.call("GET", "/v1/admin/api-keys", f.owner, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), key.Credential.ID) || strings.Contains(w.Body.String(), key.Secret) {
		t.Fatal("owner key list must include the derived key without its secret")
	}
	w = f.call("GET", "/v1/admin/api-keys/"+key.Credential.ID+"/secret", f.owner, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), key.Secret) {
		t.Fatal("owner could not recover a derived key")
	}
	if _, err := f.deps.DB.Exec(`UPDATE server_settings SET "integrations.compatibilityEnabled"=0`); err != nil {
		t.Fatal(err)
	}
	if w = f.call("GET", "/v1/me", key.Secret, ""); w.Code != 401 {
		t.Fatal("disabled app-granted key retained native access")
	}
}

func TestIntegrationCredentialManagementPreservesBoundedResetSession(t *testing.T) {
	f := integrationFixture(t)
	app := newApp(t, f, "before-reset")
	key := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"before-reset-key","name":"Existing","scopes":["read"]}`)
	if _, err := f.deps.DB.Exec(`INSERT INTO reset_file_sessions(family_id,kind) SELECT family_id,'owner' FROM authorization_family_tokens WHERE token_hash=?`, identity.Digest(f.owner)); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/admin/api-keys/" + key.Credential.ID + "/secret", "/v1/integrations/app-passwords/" + app.AppPassword.ID + "/secret", "/Auth/Keys"} {
		w := f.call("GET", path, f.owner, "")
		if w.Code != 403 {
			t.Fatalf("bounded reset recovered credentials through %s: %d", path, w.Code)
		}
	}
	w := f.call("POST", "/v1/integrations/app-passwords", f.owner, `{"operationId":"reset-cannot-mint","name":"Persistent escape","accountId":"owner","profileId":"owner-profile","scopes":["admin"]}`)
	if w.Code != 403 {
		t.Fatalf("bounded reset minted app grant: %d %s", w.Code, w.Body)
	}
}
