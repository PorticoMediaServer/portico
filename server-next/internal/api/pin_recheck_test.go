package api

import (
	"strings"
	"testing"
	"time"
)

// A device that remembers the owner's PIN enters the main profile without it and sees every
// owner page, but changes nothing sensitive until the PIN is typed; a typed PIN counts for an
// hour. The person can turn the check off, which itself needs the PIN. A remembered PIN is
// good on its own device only, until the PIN changes or the owner stops PINs being remembered.
func TestARememberedPINAndSensitiveSettings(t *testing.T) {
	s := newTestServer(t)
	now := time.Now()
	s.setClock(func() time.Time { return now })
	first := setup(t, s)
	main := first.Profiles[0].ID
	owner := onMain(t, s, first)
	if code := status(t, s, "PUT", "/v1/profiles/"+main+"/pin", owner.AccessToken, ProfilePIN{PIN: "1234"}); code != 200 {
		t.Fatalf("setting a PIN answered %d", code)
	}
	choose := "/v1/profiles/" + main + "/select"
	enter := func(n int, body ProfileSelect) (int, Session) {
		t.Helper()
		in := signIn(t, s, n)
		var out Session
		w := call(t, s, "POST", choose, in.Session.AccessToken, body, nil)
		if w.Code == 200 {
			jsonInto(w, &out)
		}
		return w.Code, out
	}
	sensitive := func(token, name string) int {
		return status(t, s, "POST", "/v1/admin/members", token, MemberCreate{Username: name, Password: testPassword})
	}

	// Typed, and remembered from now on: everything works, for an hour.
	code, typed := enter(5, ProfileSelect{PIN: "1234", RememberPIN: true})
	if code != 200 || typed.PINToken == "" || typed.PINTokenExpiresAt == nil {
		t.Fatalf("choosing with the PIN and rememberPin answered %d, token %q", code, typed.PINToken)
	}
	if got := sensitive(typed.AccessToken, "ann"); got != 200 {
		t.Fatalf("a sensitive change just after typing the PIN answered %d", got)
	}
	// Typed and not remembered: no token, and no hour to run out.
	if code, plain := enter(6, ProfileSelect{PIN: "1234"}); code != 200 || plain.PINToken != "" {
		t.Fatalf("choosing with the PIN alone answered %d with token %q", code, plain.PINToken)
	}

	// Entered with the remembered PIN: the owner's pages open, sensitive changes ask for the PIN.
	code, kept := enter(5, ProfileSelect{PINToken: typed.PINToken})
	if code != 200 || kept.Viewer.Role != "owner" {
		t.Fatalf("entering with a remembered PIN answered %d as %q", code, kept.Viewer.Role)
	}
	if got := status(t, s, "GET", "/v1/admin/members", kept.AccessToken, nil); got != 200 {
		t.Errorf("an owner page from a remembered PIN answered %d", got)
	}
	if w := call(t, s, "POST", "/v1/admin/members", kept.AccessToken, MemberCreate{Username: "bob", Password: testPassword}, nil); w.Code != 403 ||
		!strings.Contains(w.Body.String(), "profile_pin_required") {
		t.Errorf("a sensitive change from a remembered PIN answered %d %s", w.Code, w.Body)
	}
	for name, c := range map[string]struct {
		method, path string
		body         any
	}{
		"removing the PIN":      {"DELETE", "/v1/profiles/" + main + "/pin", nil},
		"changing the PIN":      {"PUT", "/v1/profiles/" + main + "/pin", ProfilePIN{PIN: "9999"}},
		"the sign-in settings":  {"PUT", "/v1/admin/settings/sign-in", map[string]any{"staySignedInDays": 7}},
		"making a key":          {"POST", "/v1/admin/api-keys", KeyCreate{Name: "Tool", Scopes: []string{"read"}}},
		"turning the check off": {"PATCH", "/v1/preferences", PreferencesEdit{Scope: "profile-server", DeviceClass: "web", Values: map[string]any{pinRecheckSetting: false}}},
	} {
		if got := status(t, s, c.method, c.path, kept.AccessToken, c.body); got != 403 {
			t.Errorf("%s from a remembered PIN answered %d, want 403", name, got)
		}
	}
	if got := status(t, s, "PUT", "/v1/admin/settings/general", kept.AccessToken, map[string]any{"name": "Den"}); got != 200 {
		t.Errorf("an ordinary setting from a remembered PIN answered %d", got)
	}
	// Forgot PIN still works from there: it asks for the account's password instead.
	if got := status(t, s, "PUT", "/v1/profiles/"+main+"/pin", kept.AccessToken, ProfilePIN{PIN: "1234", CurrentPassword: testPassword}); got != 200 {
		t.Errorf("Forgot PIN with the password from a remembered PIN answered %d", got)
	}

	// The hour after a typed PIN runs out on a device that remembers it.
	code, typed = enter(5, ProfileSelect{PIN: "1234", RememberPIN: true})
	if code != 200 {
		t.Fatalf("typing the PIN again answered %d", code)
	}
	now = now.Add(10 * time.Minute)
	if got := sensitive(typed.AccessToken, "cat"); got != 200 {
		t.Errorf("a sensitive change ten minutes after typing the PIN answered %d", got)
	}
	// With the check turned off (the PIN was typed), a remembered PIN changes anything.
	if got := status(t, s, "PATCH", "/v1/preferences", typed.AccessToken,
		PreferencesEdit{Scope: "profile-server", DeviceClass: "web", Values: map[string]any{pinRecheckSetting: false}}); got != 200 {
		t.Fatalf("turning the check off after typing the PIN answered %d", got)
	}
	if code, kept = enter(5, ProfileSelect{PINToken: typed.PINToken}); code != 200 || sensitive(kept.AccessToken, "dan") != 200 {
		t.Errorf("with the check off, a sensitive change from a remembered PIN was refused")
	}
	status(t, s, "PATCH", "/v1/preferences", typed.AccessToken, PreferencesEdit{Scope: "profile-server", DeviceClass: "web", Values: map[string]any{pinRecheckSetting: true}})
	// An hour later the same sign-in must type the PIN again.
	in := signIn(t, s, 7)
	var hour Session
	if w := call(t, s, "POST", choose, in.Session.AccessToken, ProfileSelect{PIN: "1234", RememberPIN: true}, &hour); w.Code != 200 {
		t.Fatalf("choosing with the PIN on a third device answered %d", w.Code)
	}
	now = now.Add(61 * time.Minute)
	code, hour = renew(t, s, in.Session.RefreshToken, 7, "an-hour-later")
	if code != 200 {
		t.Fatalf("renewing an hour later answered %d", code)
	}
	if got := sensitive(hour.AccessToken, "eve"); got != 403 {
		t.Errorf("a sensitive change 61 minutes after typing the PIN answered %d, want 403", got)
	}
	if got := status(t, s, "GET", "/v1/admin/members", hour.AccessToken, nil); got != 200 {
		t.Errorf("an owner page 61 minutes after typing the PIN answered %d", got)
	}
	if w := call(t, s, "POST", choose, hour.AccessToken, ProfileSelect{PIN: "1234"}, &hour); w.Code != 200 || sensitive(hour.AccessToken, "eve") != 200 {
		t.Errorf("typing the PIN again did not open sensitive changes")
	}

	// Good on its own device only; not after the PIN changes; not once the owner says always ask.
	if code, _ := enter(8, ProfileSelect{PINToken: typed.PINToken}); code != 403 {
		t.Errorf("a remembered PIN carried to another device answered %d", code)
	}
	code, fresh := enter(5, ProfileSelect{PIN: "1234"})
	if code != 200 || status(t, s, "PUT", "/v1/profiles/"+main+"/pin", fresh.AccessToken, ProfilePIN{PIN: "4321"}) != 200 {
		t.Fatal("changing the PIN after typing it was refused")
	}
	if code, _ := enter(5, ProfileSelect{PINToken: typed.PINToken}); code != 403 {
		t.Errorf("a remembered PIN still worked after the PIN changed: %d", code)
	}
	code, typed = enter(5, ProfileSelect{PIN: "4321", RememberPIN: true})
	if code != 200 || typed.PINToken == "" {
		t.Fatalf("remembering the new PIN answered %d", code)
	}
	saveSignIn(t, s, typed.AccessToken, `{"pinRememberDays":0}`, 200)
	if code, _ := enter(5, ProfileSelect{PINToken: typed.PINToken}); code != 403 {
		t.Errorf("a remembered PIN still worked after the owner chose always ask: %d", code)
	}
	if code, again := enter(5, ProfileSelect{PIN: "4321", RememberPIN: true}); code != 200 || again.PINToken != "" {
		t.Errorf("with always ask chosen, rememberPin answered %d and token %q", code, again.PINToken)
	}
}

// Profiles are put in the order asked for, the main one always first, and a list that is not
// exactly the account's profiles is refused.
func TestProfilesArePutInOrder(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s).AccessToken
	var a, b Profile
	call(t, s, "POST", "/v1/profiles", owner, ProfileCreate{Name: "A"}, &a)
	call(t, s, "POST", "/v1/profiles", owner, ProfileCreate{Name: "B"}, &b)
	var before Page[Profile]
	call(t, s, "GET", "/v1/profiles", owner, nil, &before)
	main := before.Items[0].ID
	var after Page[Profile]
	if w := call(t, s, "PUT", "/v1/profiles/order", owner, ProfileOrder{IDs: []string{b.ID, main, a.ID}}, &after); w.Code != 200 ||
		len(after.Items) != 3 || after.Items[0].ID != main || after.Items[1].ID != b.ID || after.Items[2].ID != a.ID {
		t.Fatalf("ordering answered %d %+v", w.Code, after.Items)
	}
	call(t, s, "GET", "/v1/profiles", owner, nil, &before)
	if before.Items[1].ID != b.ID {
		t.Errorf("the picker lists %+v after ordering", before.Items)
	}
	for name, ids := range map[string][]string{"one missing": {main, a.ID}, "one twice": {main, a.ID, a.ID}, "a stranger": {main, a.ID, "prf_nobody"}} {
		if code := status(t, s, "PUT", "/v1/profiles/order", owner, ProfileOrder{IDs: ids}); code != 400 {
			t.Errorf("%s answered %d, want 400", name, code)
		}
	}
}

// Every route named as sensitive exists: a renamed route must not drop out of the check unseen.
func TestSensitiveRoutesExist(t *testing.T) {
	s := newTestServer(t)
	known := map[string]bool{}
	for _, route := range s.Routes() {
		known[route.Pattern] = true
	}
	for pattern := range sensitiveRoutes {
		if !known[pattern] {
			t.Errorf("%s is named as sensitive and is not a route", pattern)
		}
	}
}
