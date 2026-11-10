package api

import (
	"encoding/base32"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
)

func setSignIn(t *testing.T, s *Server, token string, edit SignInSettingsEdit) {
	t.Helper()
	if w := call(t, s, "PATCH", "/v1/admin/settings/sign-in", token, edit, nil); w.Code != 200 {
		t.Fatalf("changing the sign-in settings answered %d: %s", w.Code, w.Body)
	}
}

func to[T any](v T) *T { return &v }

// Only the owner changes the sign-in settings, each takes one of its listed choices, and
// each choice does what it says: the shortest password, how strict wrong guesses are, how
// long a sign-in and a reset code last. The settings are still there after a restart.
func TestSignInSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.db")
	s := openTestServer(t, path)
	owner := onMainProfile(t, s)
	addMember(t, s, owner.AccessToken, "ada", "admin")
	max := addMember(t, s, owner.AccessToken, "max", "member")
	admin := signInAs(t, s, "ada", testPassword, 2)

	var settings SignInSettings
	if w := call(t, s, "GET", "/v1/admin/settings/sign-in", owner.AccessToken, nil, &settings); w.Code != 200 || settings.Policy != auth.DefaultPolicy() {
		t.Fatalf("the first settings: %d %+v", w.Code, settings.Policy)
	}
	if got := status(t, s, "PATCH", "/v1/admin/settings/sign-in", admin.AccessToken, SignInSettingsEdit{StaySignedInDays: to(7)}); got != 403 {
		t.Errorf("an admin changing the sign-in settings answered %d, want 403", got)
	}
	for name, edit := range map[string]SignInSettingsEdit{
		"a stay-signed-in time that is not offered": {StaySignedInDays: to(45)},
		"a strictness that does not exist":          {PINAttempts: to("lenient")},
		"a password length that is not offered":     {PasswordMinLength: to(4)},
	} {
		if got := status(t, s, "PATCH", "/v1/admin/settings/sign-in", owner.AccessToken, edit); got != 400 {
			t.Errorf("%s answered %d, want 400", name, got)
		}
	}
	setSignIn(t, s, owner.AccessToken, SignInSettingsEdit{StaySignedInDays: to(7), PasswordMinLength: to(16), PasswordAttempts: to("strict"), ResetCodeMinutes: to(15)})
	closeDB(s.db)
	s = openTestServer(t, path)
	if got := s.policy(); got.StaySignedInDays != 7 || got.PasswordMinLength != 16 || got.PINRememberDays != 30 {
		t.Fatalf("after a restart the settings are %+v", got)
	}

	// A password that was long enough before still signs in; a new one must meet the new length.
	in := signIn(t, s, 5)
	if left := time.Until(in.Session.SessionExpiresAt); left < 6*24*time.Hour || left > 7*24*time.Hour {
		t.Errorf("a new sign-in lasts %v, want 7 days", left)
	}
	owner = onMain(t, s, in)
	if w := call(t, s, "POST", "/v1/admin/members", owner.AccessToken, MemberCreate{Username: "sam", Password: testPassword}, nil); w.Code != 400 {
		t.Errorf("a new password of %d characters answered %d, want 400", len(testPassword), w.Code)
	}
	var reset PasswordResetCode
	if w := call(t, s, "POST", "/v1/admin/members/"+max.ID+"/password-reset", owner.AccessToken, PasswordResetCreate{}, &reset); w.Code != 200 ||
		time.Until(reset.ExpiresAt) > 15*time.Minute || time.Until(reset.ExpiresAt) < 14*time.Minute {
		t.Errorf("a reset code: %d, good for %v, want 15 minutes", w.Code, time.Until(reset.ExpiresAt))
	}
	// On Strict the third wrong password already waits, and for ten seconds.
	wrong := SignInRequest{Username: "ada", Password: "not the password", Device: device(7)}
	var refused errorBody
	for range 3 {
		call(t, s, "POST", "/v1/auth/sign-in", "", wrong, nil)
	}
	if w := call(t, s, "POST", "/v1/auth/sign-in", "", wrong, &refused); w.Code != 429 || refused.Error.RetryAfterMs < 9000 || refused.Error.RetryAfterMs > 10000 {
		t.Errorf("the fourth wrong password on Strict: %d with a wait of %d ms, want 429 and ten seconds", w.Code, refused.Error.RetryAfterMs)
	}
}

// When the owner requires two-step sign-in, a person it applies to sets it up at their
// next sign-in before anything else, and cannot turn it off. Nobody else is held up.
func TestRequiredTwoStep(t *testing.T) {
	s := newTestServer(t)
	now := time.Now()
	s.now = func() time.Time { return now }
	owner := onMainProfile(t, s)
	addMember(t, s, owner.AccessToken, "max", "member")
	setSignIn(t, s, owner.AccessToken, SignInSettingsEdit{RequireTwoStep: to("admins")})

	if in := signInAs(t, s, "max", testPassword, 3); status(t, s, "GET", "/v1/libraries", in.AccessToken, nil) != 200 {
		t.Error("a member was held up by a rule for the owner and admins")
	}
	// The owner's sign-in from before the rule carries on; their next one is held.
	if got := status(t, s, "GET", "/v1/devices", owner.AccessToken, nil); got != 200 {
		t.Errorf("the sign-in from before the rule answered %d", got)
	}
	held := signIn(t, s, 4)
	if !held.Session.TwoStepSetupRequired {
		t.Fatal("the owner's next sign-in was not told to set up two-step sign-in")
	}
	token := held.Session.AccessToken
	var refused errorBody
	for _, path := range []string{"/v1/profiles", "/v1/libraries", "/v1/admin/members"} {
		if w := call(t, s, "GET", path, token, nil, &refused); w.Code != 403 || refused.Error.Code != "factor_setup_required" {
			t.Errorf("%s before setting up answered %d %s", path, w.Code, refused.Error.Code)
		}
	}
	if w := call(t, s, "POST", "/v1/profiles/"+held.Profiles[0].ID+"/select", token, ProfileSelect{}, &refused); w.Code != 403 {
		t.Errorf("choosing a profile before setting up answered %d", w.Code)
	}
	var setupInfo TwoFactorSetup
	if w := call(t, s, "POST", "/v1/me/two-factor", token, TwoFactorEnrol{CurrentPassword: testPassword}, &setupInfo); w.Code != 200 {
		t.Fatalf("setting up from the held sign-in answered %d: %s", w.Code, w.Body)
	}
	secret, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(setupInfo.Secret)
	code := auth.TOTPCode(secret, auth.TOTPStep(now))
	if w := call(t, s, "POST", "/v1/me/two-factor/confirm", token, TwoFactorVerify{Code: code}, nil); w.Code != 200 {
		t.Fatalf("confirming answered %d: %s", w.Code, w.Body)
	}
	// The same sign-in is now an ordinary one: it renews and chooses a profile.
	status, renewed := renew(t, s, held.Session.RefreshToken, 4, "after-setup")
	if status != 200 || renewed.TwoStepSetupRequired {
		t.Fatalf("renewing after setting up: %d %+v", status, renewed)
	}
	held.Session = renewed
	main := onMain(t, s, held)
	now = now.Add(time.Minute)
	off := TwoFactorProof{CurrentPassword: testPassword, Code: auth.TOTPCode(secret, auth.TOTPStep(now))}
	if w := call(t, s, "DELETE", "/v1/me/two-factor", main.AccessToken, off, &refused); w.Code != 403 || refused.Error.Code != "two_factor_required" {
		t.Errorf("turning it off while required answered %d %s", w.Code, refused.Error.Code)
	}
}

// An account with viewing hours cannot sign in outside them. Inside them its token never
// outlasts the window, so nothing plays long after the hours end; the renewal after that is
// refused with "Viewing hours have ended", the sign-in is kept, and it works again when the
// hours next begin.
func TestViewingHours(t *testing.T) {
	s := newTestServer(t)
	now := time.Date(2026, 11, 11, 18, 50, 0, 0, time.UTC) // a Wednesday
	// The owner signs in on the real clock: tokens are checked against the server's own.
	owner := onMainProfile(t, s)
	max := addMember(t, s, owner.AccessToken, "max", "member")
	evenings := MemberEdit{ViewingHours: &auth.Hours{Timezone: "UTC", Windows: []auth.Window{{Days: []int{1, 2, 3, 4, 5}, StartMinute: 16 * 60, EndMinute: 19 * 60}}}}
	var edited Member
	if w := call(t, s, "PATCH", "/v1/admin/members/"+max.ID, owner.AccessToken, evenings, &edited); w.Code != 200 || edited.ViewingHours == nil {
		t.Fatalf("setting viewing hours answered %d: %s", w.Code, w.Body)
	}
	bad := MemberEdit{ViewingHours: &auth.Hours{Timezone: "Mars/Olympus", Windows: evenings.ViewingHours.Windows}}
	if got := status(t, s, "PATCH", "/v1/admin/members/"+max.ID, owner.AccessToken, bad); got != 400 {
		t.Errorf("an unknown timezone answered %d, want 400", got)
	}
	s.now = func() time.Time { return now }
	in := signInAs(t, s, "max", testPassword, 3)
	if !in.ExpiresAt.Equal(time.Date(2026, 11, 11, 19, 0, 0, 0, time.UTC)) {
		t.Errorf("a token minted ten minutes before the hours end runs until %s", in.ExpiresAt)
	}
	if open, until, err := s.db.ViewingOpen(t.Context(), mustClaims(t, s, in.AccessToken).UserID, now); err != nil || !open || until.IsZero() {
		t.Errorf("the playback check inside the hours: %v %v %v", open, until, err)
	}
	now = now.Add(15 * time.Minute)
	var refused errorBody
	if w := call(t, s, "GET", "/v1/libraries", in.AccessToken, nil, nil); w.Code != 401 {
		t.Errorf("the token after the hours ended answered %d, want 401", w.Code)
	}
	body := RefreshRequest{RefreshToken: in.RefreshToken, InstallationID: device(3).InstallationID, RequestID: "evening"}
	if w := call(t, s, "POST", "/v1/auth/refresh", "", body, &refused); w.Code != 403 || refused.Error.Code != "outside_viewing_hours" || refused.Error.Message != "Viewing hours have ended." {
		t.Fatalf("renewing after the hours ended: %d %+v", w.Code, refused.Error)
	}
	wrongTime := SignInRequest{Username: "max", Password: testPassword, Device: device(4)}
	if w := call(t, s, "POST", "/v1/auth/sign-in", "", wrongTime, &refused); w.Code != 403 || refused.Error.Code != "outside_viewing_hours" {
		t.Errorf("signing in outside the hours answered %d %s", w.Code, refused.Error.Code)
	}
	wrongTime.Password = "not the password"
	if w := call(t, s, "POST", "/v1/auth/sign-in", "", wrongTime, nil); w.Code != 401 {
		t.Errorf("a wrong password outside the hours answered %d: the hours are told only to someone who knows the password", w.Code)
	}
	if open, _, _ := s.db.ViewingOpen(t.Context(), mustClaims(t, s, in.AccessToken).UserID, now); open {
		t.Error("the playback check says the hours are still open")
	}
	// The next afternoon the same sign-in carries on.
	now = time.Date(2026, 11, 12, 16, 30, 0, 0, time.UTC)
	if w := call(t, s, "POST", "/v1/auth/refresh", "", body, nil); w.Code != 200 {
		t.Fatalf("renewing when the hours began again answered %d: %s", w.Code, w.Body)
	}
}

// An account limited to home signs in on the owner's home networks and nowhere else. Away
// from home the reason is told only to someone who knows the password, and a device that
// leaves home keeps its sign-in for when it is back.
func TestAccountLimitedToHome(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s)
	const home, away = "192.168.1.20", "198.51.100.9"
	network := NetworkSettingsEdit{HomeNetworks: &[]string{"192.168.1.0/24"}, TrustedProxies: []string{}}
	if got := status(t, s, "PUT", "/v1/admin/settings/network", owner.AccessToken, network); got != 200 {
		t.Fatalf("saving the home networks answered %d", got)
	}
	max := addMember(t, s, owner.AccessToken, "max", "member")
	yes := true
	var edited Member
	if w := call(t, s, "PATCH", "/v1/admin/members/"+max.ID, owner.AccessToken, MemberEdit{HomeOnly: &yes}, &edited); w.Code != 200 || !edited.HomeOnly {
		t.Fatalf("limiting an account to home answered %d: %s", w.Code, w.Body)
	}

	signIn := SignInRequest{Username: "max", Password: testPassword, Device: device(5)}
	var refused errorBody
	r := newRequest("POST", "/v1/auth/sign-in", "", signIn)
	r.RemoteAddr = away + ":4000"
	if w := serve(s, r); w.Code != 403 || jsonInto(w, &refused) != nil || refused.Error.Code != "away_from_home" {
		t.Fatalf("signing in away from home answered %d %+v", w.Code, refused.Error)
	}
	wrong := signIn
	wrong.Password = "not the password"
	if got := from(t, s, away, "", "POST", "/v1/auth/sign-in", "", wrong, nil); got != 401 {
		t.Errorf("a wrong password away from home answered %d: the limit is told only to someone who knows the password", got)
	}
	var in SignInResponse
	if got := from(t, s, home, "", "POST", "/v1/auth/sign-in", "", signIn, &in); got != 200 {
		t.Fatalf("signing in at home answered %d", got)
	}
	renewal := RefreshRequest{RefreshToken: in.Session.RefreshToken, InstallationID: device(5).InstallationID, RequestID: "left-home"}
	if got := from(t, s, away, "", "POST", "/v1/auth/refresh", "", renewal, nil); got != 403 {
		t.Errorf("renewing away from home answered %d, want 403", got)
	}
	if got := from(t, s, home, "", "POST", "/v1/auth/refresh", "", renewal, nil); got != 200 {
		t.Errorf("the same renewal back at home answered %d: the sign-in was not kept", got)
	}
}

func mustClaims(t *testing.T, s *Server, token string) auth.Claims {
	t.Helper()
	claims, err := s.tokens.ExpiredAccess(token)
	if err != nil {
		t.Fatal(err)
	}
	return claims
}

// A device may remember a profile's PIN for as long as the owner allows. What it keeps works
// only on that device and only until the PIN changes, and entering the main profile with it
// does not let anyone manage the account: that asks for the PIN, and for an hour at a time.
func TestRememberPIN(t *testing.T) {
	s := newTestServer(t)
	now := time.Now()
	s.now = func() time.Time { return now }
	owner := onMainProfile(t, s)
	main := "/v1/profiles/" + get[Me](t, s, owner.AccessToken, "/v1/me").Profile.ID
	if w := call(t, s, "PUT", main+"/pin", owner.AccessToken, ProfilePIN{PIN: "2468"}, nil); w.Code != 200 {
		t.Fatalf("setting a PIN answered %d", w.Code)
	}
	selectWith := func(n int, req ProfileSelect) (int, Session, string) {
		t.Helper()
		in := signIn(t, s, n)
		var out Session
		var refused errorBody
		w := call(t, s, "POST", main+"/select", in.Session.AccessToken, req, nil)
		if w.Code == 200 {
			jsonInto(w, &out)
		} else {
			jsonInto(w, &refused)
		}
		return w.Code, out, refused.Error.Code
	}
	code, typed, _ := selectWith(3, ProfileSelect{PIN: "2468", RememberPIN: true})
	if code != 200 || typed.PINToken == "" || typed.PINTokenExpiresAt == nil || typed.PINTokenExpiresAt.Sub(now) != 30*24*time.Hour {
		t.Fatalf("typing the PIN with remember: %d, token %q until %v", code, typed.PINToken, typed.PINTokenExpiresAt)
	}
	if got := status(t, s, "GET", "/v1/devices", typed.AccessToken, nil); got != 200 {
		t.Errorf("managing straight after typing the PIN answered %d", got)
	}
	// An hour after the PIN was typed on a device that remembers it, managing asks for it
	// again, and typing it gives another hour.
	hour := signIn(t, s, 6)
	var first Session
	call(t, s, "POST", main+"/select", hour.Session.AccessToken, ProfileSelect{PIN: "2468", RememberPIN: true}, &first)
	now = now.Add(61 * time.Minute)
	_, later := renew(t, s, hour.Session.RefreshToken, 6, "an-hour-on")
	var lapsed errorBody
	if w := call(t, s, "GET", "/v1/devices", later.AccessToken, nil, &lapsed); w.Code != 403 || lapsed.Error.Code != "profile_pin_manage" {
		t.Errorf("managing an hour after the PIN was typed answered %d %s, want 403 profile_pin_manage", w.Code, lapsed.Error.Code)
	}
	var retyped Session
	call(t, s, "POST", main+"/select", later.AccessToken, ProfileSelect{PIN: "2468", RememberPIN: true}, &retyped)
	if got := status(t, s, "GET", "/v1/devices", retyped.AccessToken, nil); got != 200 {
		t.Errorf("managing after typing the PIN again answered %d", got)
	}
	code, remembered, _ := selectWith(3, ProfileSelect{PINToken: typed.PINToken})
	if code != 200 || remembered.Viewer.Role != "owner" {
		t.Fatalf("entering with the remembered PIN: %d %+v", code, remembered.Viewer)
	}
	var refused errorBody
	for _, path := range []string{"/v1/devices", "/v1/admin/members"} {
		if w := call(t, s, "GET", path, remembered.AccessToken, nil, &refused); w.Code != 403 || refused.Error.Code != "profile_pin_manage" {
			t.Errorf("%s from a remembered main profile answered %d %s, want 403 profile_pin_manage", path, w.Code, refused.Error.Code)
		}
	}
	if got := status(t, s, "GET", "/v1/libraries", remembered.AccessToken, nil); got != 200 {
		t.Errorf("watching from a remembered profile answered %d", got)
	}
	// What one device keeps is no use on another, and a made-up one counts against nobody.
	if code, _, why := selectWith(4, ProfileSelect{PINToken: typed.PINToken}); code != 403 || why != "profile_pin_reentry" {
		t.Errorf("another device with this device's memory: %d %s", code, why)
	}
	if code, _, _ := selectWith(4, ProfileSelect{PIN: "2468"}); code != 200 {
		t.Errorf("the right PIN after a refused memory answered %d: a stale memory must not count as a wrong PIN", code)
	}
	// Past the owner's time for remembering, and after the PIN changes, the device asks again.
	now = now.Add(31 * 24 * time.Hour)
	if code, _, why := selectWith(3, ProfileSelect{PINToken: typed.PINToken}); code != 403 || why != "profile_pin_reentry" {
		t.Errorf("a memory older than 30 days: %d %s", code, why)
	}
	_, fresh, _ := selectWith(3, ProfileSelect{PIN: "2468", RememberPIN: true})
	if w := call(t, s, "PUT", main+"/pin", fresh.AccessToken, ProfilePIN{PIN: "1357"}, nil); w.Code != 200 {
		t.Fatalf("changing the PIN answered %d", w.Code)
	}
	if code, _, why := selectWith(3, ProfileSelect{PINToken: fresh.PINToken}); code != 403 || why != "profile_pin_reentry" {
		t.Errorf("a memory of the old PIN: %d %s", code, why)
	}
	// With "always ask", nothing is handed out and nothing kept is honoured.
	_, again, _ := selectWith(3, ProfileSelect{PIN: "1357", RememberPIN: true})
	setSignIn(t, s, again.AccessToken, SignInSettingsEdit{PINRememberDays: to(0)})
	if code, _, _ := selectWith(3, ProfileSelect{PINToken: again.PINToken}); code != 403 {
		t.Errorf("a memory used after the owner chose always ask answered %d", code)
	}
	if _, none, _ := selectWith(3, ProfileSelect{PIN: "1357", RememberPIN: true}); none.PINToken != "" {
		t.Error("a device was allowed to remember a PIN although the owner chose always ask")
	}
}

// A sign-in screen lists the accounts that chose to be remembered on that device, and only
// there; the owner can turn the list off.
func TestRememberedAccounts(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s)
	var in SignInResponse
	body := SignInRequest{Username: "justin", Password: testPassword, Device: device(3), RememberAccount: true}
	if w := call(t, s, "POST", "/v1/auth/sign-in", "", body, &in); w.Code != 200 || in.AccountToken == "" {
		t.Fatalf("signing in with remember: %d, token %q", w.Code, in.AccountToken)
	}
	list := func(installation string) []RememberedAccount {
		t.Helper()
		var out RememberedAccounts
		req := RememberedRequest{InstallationID: installation, AccountTokens: []string{in.AccountToken, "made-up"}}
		if w := call(t, s, "POST", "/v1/auth/remembered", "", req, &out); w.Code != 200 {
			t.Fatalf("listing remembered accounts answered %d: %s", w.Code, w.Body)
		}
		return out.Accounts
	}
	if got := list(device(3).InstallationID); len(got) != 1 || got[0].Username != "justin" || got[0].Name != "Justin" || got[0].AccountToken != in.AccountToken {
		t.Fatalf("this device's remembered accounts: %+v", got)
	}
	if got := list(device(4).InstallationID); len(got) != 0 {
		t.Errorf("another device was told %+v", got)
	}
	setSignIn(t, s, owner.AccessToken, SignInSettingsEdit{RememberAccounts: to(false)})
	if got := list(device(3).InstallationID); len(got) != 0 {
		t.Errorf("with the list turned off the device was told %+v", got)
	}
	body.Device, in = device(5), SignInResponse{}
	call(t, s, "POST", "/v1/auth/sign-in", "", body, &in)
	if in.AccountToken != "" || get[System](t, s, "", "/v1/system").RememberAccounts {
		t.Error("an account was remembered although the owner turned the list off")
	}
}

// An owner who is locked out asks for a code in a file only someone at the machine can
// read. The page is never shown the code and is told the same whatever was typed; a new file
// is written at most once a minute; five wrong codes cancel it; the right one sets a new
// password once and signs the owner out everywhere.
func TestOwnerResetFile(t *testing.T) {
	s := newTestServer(t)
	now := time.Now()
	s.now = func() time.Time { return now }
	owner := onMainProfile(t, s)
	file := filepath.Join(s.dataDir, ownerResetFile)
	ask := func(username string) (string, string) {
		t.Helper()
		w := call(t, s, "POST", "/v1/auth/owner-reset", "", OwnerResetStart{Username: username}, nil)
		if w.Code != 200 {
			t.Fatalf("asking for the file answered %d: %s", w.Code, w.Body)
		}
		raw, _ := os.ReadFile(file)
		return regexp.MustCompile(`(?m)^[A-Z2-9]{4}(-[A-Z2-9]{4}){3}$`).FindString(string(raw)), w.Body.String()
	}
	code, answer := ask("justin")
	info, err := os.Stat(file)
	if err != nil || info.Mode().Perm() != 0o600 || code == "" {
		t.Fatalf("the file: %v, mode %v, code %q", err, info.Mode(), code)
	}
	if strings.Contains(answer, code) || strings.Contains(answer, s.dataDir) {
		t.Fatalf("the page was told the code or where the data folder is: %s", answer)
	}
	// Asked again at once, for anybody: the same answer in kind, and no new code.
	if again, other := ask("nobody"); again != code || !strings.Contains(other, ownerResetFile) {
		t.Errorf("a second ask inside a minute wrote a new code or answered differently: %s", other)
	}
	finish := func(code, password string) int {
		return status(t, s, "POST", "/v1/auth/owner-reset/confirm", "", OwnerResetFinish{Code: code, NewPassword: password})
	}
	for range 5 {
		if got := finish("AAAA-AAAA-AAAA-AAAA", "a new password"); got != 401 {
			t.Fatalf("a wrong code answered %d, want 401", got)
		}
	}
	if got := finish(code, "a new password"); got != 401 {
		t.Fatalf("the right code after five wrong ones answered %d, want 401", got)
	}
	now = now.Add(61 * time.Second)
	second, _ := ask("justin")
	if second == "" || second == code {
		t.Fatalf("a minute later the file holds %q (before: %q)", second, code)
	}
	now = now.Add(16 * time.Minute)
	if got := finish(second, "a new password"); got != 401 {
		t.Fatalf("a code older than fifteen minutes answered %d, want 401", got)
	}
	third, _ := ask("justin")
	if got := finish(third, "a new password"); got != 204 {
		t.Fatalf("the right code answered %d, want 204", got)
	}
	if _, err := os.Stat(file); err == nil {
		t.Error("the file is still there after the code was used")
	}
	if got := finish(third, "another password"); got != 401 {
		t.Errorf("the code worked a second time (%d)", got)
	}
	if got := status(t, s, "GET", "/v1/me", owner.AccessToken, nil); got != 401 {
		t.Errorf("a session from before the reset answered %d, want 401", got)
	}
	old := SignInRequest{Username: "justin", Password: testPassword, Device: device(8)}
	if got := status(t, s, "POST", "/v1/auth/sign-in", "", old); got != 401 {
		t.Errorf("the old password answered %d, want 401", got)
	}
	old.Password = "a new password"
	if got := status(t, s, "POST", "/v1/auth/sign-in", "", old); got != 200 {
		t.Errorf("the new password answered %d, want 200", got)
	}
}
