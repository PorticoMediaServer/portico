package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestSignInPolicyOwnerMainProfileAndTypedReplacement(t *testing.T) {
	f := administrationFixtureNew(t)
	for _, token := range []string{f.admin, f.member} {
		w := f.call("GET", "/v1/admin/sign-in/policy", token, "")
		if w.Code != 403 {
			t.Fatalf("non-owner policy read: %d %s", w.Code, w.Body)
		}
		w = f.call("PATCH", "/v1/admin/sign-in/policy", token, `{"values":{}}`)
		if w.Code != 403 {
			t.Fatalf("non-owner reached revision/body validation: %d %s", w.Code, w.Body)
		}
	}
	w := f.call("GET", "/v1/admin/sign-in/policy", f.owner, "")
	var current identity.SignInPolicy
	if e := json.Unmarshal(w.Body.Bytes(), &current); e != nil || w.Code != 200 || current.Values != identity.DefaultSignInPolicy() {
		t.Fatalf("read policy: %d %s %v", w.Code, w.Body, e)
	}
	q := identity.SignInPolicyPatch{ExpectedRevision: current.Revision, Values: current.Values}
	q.Values.StaySignedInDays = 7
	q.Values.PINRememberDays = 0
	q.Values.PasswordAttempts = "strict"
	q.Values.PINAttempts = "relaxed"
	q.Values.PasswordMinLength = 12
	q.Values.MemberResetCodeMinutes = 15
	q.Values.RequireTwoStep = "everyone"
	q.Values.RememberAccounts = false
	q.Values.EndedSessionsDays = 30
	body, e := json.Marshal(q)
	if e != nil {
		t.Fatal(e)
	}
	w = f.call("PATCH", "/v1/admin/sign-in/policy", f.owner, string(body))
	if w.Code != 200 {
		t.Fatalf("save policy: %d %s", w.Code, w.Body)
	}
	w = f.call("PATCH", "/v1/admin/sign-in/policy", f.owner, string(body))
	if w.Code != 412 || errorCode(t, w) != "revision_mismatch" {
		t.Fatalf("stale policy: %d %s", w.Code, w.Body)
	}
	// Required boolean presence and enum/null input stay strict, with no 500s.
	var object map[string]any
	if e = json.Unmarshal(body, &object); e != nil {
		t.Fatal(e)
	}
	delete(object["values"].(map[string]any), "rememberAccounts")
	incomplete, e := json.Marshal(object)
	if e != nil {
		t.Fatal(e)
	}
	w = f.call("PATCH", "/v1/admin/sign-in/policy", f.owner, string(incomplete))
	if w.Code != 400 {
		t.Fatalf("incomplete policy: %d %s", w.Code, w.Body)
	}
	w = f.call("PATCH", "/v1/admin/sign-in/policy", f.owner, `{"values":{}}`)
	if w.Code < 400 || w.Code >= 500 {
		t.Fatalf("malformed policy: %d %s", w.Code, w.Body)
	}
	// A second profile never borrows the owner's Management capability.
	if _, e = f.deps.DB.Exec(`INSERT INTO direct_profiles(id,account_id,name,is_primary) VALUES('owner-second','owner','Second',0)`); e != nil {
		t.Fatal(e)
	}
	// Required enrollment refuses issuance until the owner has a factor. Change
	// back through the original authorized main-profile session first.
	q.ExpectedRevision++
	q.Values.RequireTwoStep = "nobody"
	body, e = json.Marshal(q)
	if e != nil {
		t.Fatal(e)
	}
	w = f.call("PATCH", "/v1/admin/sign-in/policy", f.owner, string(body))
	if w.Code != 200 {
		t.Fatalf("restore enrollment setting: %d %s", w.Code, w.Body)
	}
	secondary, e := f.deps.Identity.Issue("owner", "owner-second", "local", "member", 1)
	if e != nil {
		t.Fatal(e)
	}
	w = f.call("GET", "/v1/admin/sign-in/policy", secondary.AccessToken, "")
	if w.Code != 403 {
		t.Fatalf("second profile read: %d %s", w.Code, w.Body)
	}
}
