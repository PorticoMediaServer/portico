package api

import (
	"testing"
	"time"
)

// An administrator key keeps the ordinary API's powers, deleting included, but can never
// make or end keys, change a password, the sign-in settings or who is an administrator.
func TestKeysCannotChangeWhoMaySignIn(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s)
	var key KeyCreated
	if w := call(t, s, "POST", "/v1/admin/api-keys", owner.AccessToken, KeyCreate{Name: "Cleaner", Scopes: []string{"admin"}}, &key); w.Code != 200 {
		t.Fatalf("making a key answered %d: %s", w.Code, w.Body)
	}
	max := addMember(t, s, owner.AccessToken, "max", "member")
	sam := addMember(t, s, owner.AccessToken, "sam", "admin")
	pat := addMember(t, s, owner.AccessToken, "pat", "admin")
	admin, yes, no := "admin", true, false
	if got := status(t, s, "PATCH", "/v1/admin/members/"+pat.ID, owner.AccessToken, MemberEdit{Disabled: &yes}); got != 200 {
		t.Fatalf("turning an administrator off answered %d", got)
	}
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/v1/admin/api-keys", KeyCreate{Name: "More", Scopes: []string{"read"}}},
		{"DELETE", "/v1/admin/api-keys/" + key.Key.ID, nil},
		{"POST", "/v1/me/tokens", KeyCreate{Name: "More", Scopes: []string{"read"}}},
		{"POST", "/v1/auth/password", PasswordRequest{CurrentPassword: testPassword, NewPassword: "another long password"}},
		{"PATCH", "/v1/admin/settings/sign-in", map[string]any{"passwordMinLength": 20}},
		{"PATCH", "/v1/admin/members/" + max.ID, MemberEdit{Role: &admin}},
		{"POST", "/v1/admin/members", MemberCreate{Username: "eve", Password: testPassword, Role: "admin"}},
		{"POST", "/v1/admin/invitations", InvitationCreate{Role: "admin"}},
		{"POST", "/v1/admin/members/" + max.ID + "/password-reset", nil},
		{"DELETE", "/v1/admin/members/" + sam.ID, nil},
		{"PATCH", "/v1/admin/members/" + sam.ID, MemberEdit{Disabled: &yes}},
		{"PATCH", "/v1/admin/members/" + pat.ID, MemberEdit{Disabled: &no}},
	} {
		var refused errorBody
		if w := call(t, s, c.method, c.path, key.Secret, c.body, &refused); w.Code != 403 || refused.Error.Code != "session_required" {
			t.Errorf("%s %s with a key answered %d %s, want 403 session_required", c.method, c.path, w.Code, refused.Error.Code)
		}
	}
	if got := status(t, s, "DELETE", "/v1/admin/members/"+max.ID, key.Secret, nil); got != 204 {
		t.Errorf("deleting a member with an administrator key answered %d, want 204", got)
	}
}

// A key is refused where its person could not sign in: while the account is turned off and
// from a network blocked for wrong guesses, where wrong keys count as wrong guesses. A key
// that makes too many requests is made to wait.
func TestKeysRefusedWherePersonCannotSignIn(t *testing.T) {
	s := newTestServer(t)
	now := time.Now()
	s.now = func() time.Time { return now }
	owner := onMainProfile(t, s)
	addMember(t, s, owner.AccessToken, "max", "member")
	var key KeyCreated
	if w := call(t, s, "POST", "/v1/me/tokens", signInAs(t, s, "max", testPassword, 6).AccessToken, KeyCreate{Name: "Script", Scopes: []string{"read"}}, &key); w.Code != 200 {
		t.Fatalf("making a token answered %d: %s", w.Code, w.Body)
	}
	var members Page[Member]
	call(t, s, "GET", "/v1/admin/members", owner.AccessToken, nil, &members)
	maxID := ""
	for _, m := range members.Items {
		if m.Username == "max" {
			maxID = m.ID
		}
	}
	off, on := true, false
	status(t, s, "PATCH", "/v1/admin/members/"+maxID, owner.AccessToken, MemberEdit{Disabled: &off})
	var refused errorBody
	if w := call(t, s, "GET", "/v1/me", key.Secret, nil, &refused); w.Code != 403 || refused.Error.Code != "account_unavailable" {
		t.Errorf("a turned-off account's token answered %d %s", w.Code, refused.Error.Code)
	}
	status(t, s, "PATCH", "/v1/admin/members/"+maxID, owner.AccessToken, MemberEdit{Disabled: &on})

	const away = "198.51.100.9"
	for range 30 {
		from(t, s, away, "", "GET", "/v1/me", "pk_wrong", nil, nil)
	}
	if got := from(t, s, away, "", "GET", "/v1/me", key.Secret, nil, nil); got != 429 {
		t.Errorf("the right token from a network blocked for wrong keys answered %d, want 429", got)
	}
	if got := from(t, s, away, "", "GET", "/v1/me", "pk_still_guessing", nil, nil); got != 429 {
		t.Errorf("a wrong key from the blocked network answered %d, want 429 like a right one", got)
	}
	if got := from(t, s, "203.0.113.7", "", "GET", "/v1/me", key.Secret, nil, nil); got != 200 {
		t.Fatalf("the token from another network answered %d, want 200", got)
	}
	slowed := 0
	for range keyRate + 1 {
		if from(t, s, "203.0.113.7", "", "GET", "/v1/me", key.Secret, nil, nil) == 429 {
			slowed++
		}
	}
	if slowed == 0 {
		t.Errorf("%d requests in a minute were all answered", keyRate+2)
	}
	now = now.Add(time.Minute)
	if got := from(t, s, "203.0.113.7", "", "GET", "/v1/me", key.Secret, nil, nil); got != 200 {
		t.Errorf("a minute later the token answered %d, want 200", got)
	}
}
