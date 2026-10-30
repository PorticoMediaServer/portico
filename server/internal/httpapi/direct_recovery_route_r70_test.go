package httpapi

import (
	"testing"
)

// ACC-LEAF-17: the recovery-codes route must demand the same ordinary
// main-profile account session as its sibling two-step mutations. A personal
// access token and an API key — even one holding every scope the fixture can
// grant — must be refused by the credential gate, not merely by insufficient
// scope, and an unauthenticated call must be unauthorized. The accepted-response
// no-store header is not asserted here: this fixture has no confirmed factor, and
// faking one to reach that branch is exactly what the item forbids.

func TestRecoveryCodesRouteRequiresOrdinarySession(t *testing.T) {
	f := administrationFixtureNew(t)
	key := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"key-recovery-1","name":"Automation","scopes":["admin"]}`)
	token := issueCredential(t, f, "/v1/me/access-tokens", f.member, `{"operationId":"token-recovery-1","name":"Script","scopes":["read","admin"]}`)
	const body = `{"password":"Testing1!","code":"000000"}`
	targets := []struct{ method, path string }{
		{"POST", "/v1/direct/two-factor/recovery-codes"},
		{"POST", "/v1/direct/password"},
		{"DELETE", "/v1/direct/two-factor"},
	}
	for _, target := range targets {
		requestBody := body
		if target.path == "/v1/direct/password" {
			// Password change has its own wire shape; keep the original wrong proof.
			requestBody = `{"currentPassword":"Testing1!","newPassword":"Testing2!","code":"000000"}`
		}
		for _, credential := range []struct{ name, secret string }{
			{"api-key", key.Secret},
			{"personal-token", token.Secret},
		} {
			w := f.call(target.method, target.path, credential.secret, requestBody)
			if w.Code != 403 || registryErrorCode(t, w) != "session_required" {
				t.Fatalf("%s %s with a %s: %d %s", target.method, target.path, credential.name, w.Code, w.Body)
			}
		}
		if w := f.call(target.method, target.path, "", requestBody); w.Code != 401 {
			t.Fatalf("%s %s unauthenticated: %d %s", target.method, target.path, w.Code, w.Body)
		}
		// An ordinary session clears the credential gate; whatever the task itself
		// then decides (missing factor, wrong proof) must not be the gate's answer.
		if w := f.call(target.method, target.path, f.owner, requestBody); w.Code == 403 && registryErrorCode(t, w) == "session_required" {
			t.Fatalf("%s %s refused an ordinary session as a credential: %d %s", target.method, target.path, w.Code, w.Body)
		}
	}
}
