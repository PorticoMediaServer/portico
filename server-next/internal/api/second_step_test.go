package api

import (
	"encoding/base32"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
)

// Turning two-step on signs nobody out. A phone signed in before, with the password alone,
// stays signed in and does everything ordinary; approving a TV, making a token and making an
// app password ask it for the code once, and then it carries on. The device that set two-step
// up has shown its code already.
func TestAnEarlierSignInStaysAndIsAskedForTheCodeAtASensitiveAction(t *testing.T) {
	s, owner, _, _ := twoMembers(t)
	now := time.Now()
	s.setClock(func() time.Time { return now })
	phone := signInAs(t, s, "max", testPassword, 2)
	saveSignIn(t, s, owner, `{"requireTwoStep":"everyone"}`, 200)

	// Required of everyone, and not set up: the phone is still signed in, and is told to set it up.
	sensitive := func(token string) map[string]int {
		var tv CodeStarted
		if w := call(t, s, "POST", "/v1/auth/codes", "", CodeStart{Device: DeviceInfo{InstallationID: device(7).InstallationID, Name: "TV"}}, &tv); w.Code != 200 {
			t.Fatalf("start answered %d: %s", w.Code, w.Body)
		}
		return map[string]int{
			"approving a TV":         status(t, s, "POST", "/v1/auth/codes/"+tv.Code+"/approve", token, CodeDecision{}),
			"making a token":         status(t, s, "POST", "/v1/me/tokens", token, KeyCreate{Name: "Script", Scopes: []string{"read"}}),
			"making an app password": status(t, s, "POST", "/v1/me/app-passwords", token, KeyCreate{Name: "Tool"}),
		}
	}
	want := func(when string, got map[string]int, code int) {
		t.Helper()
		for what, answered := range got {
			if answered != code && !(code == 200 && answered == 204) {
				t.Errorf("%s, %s answered %d, want %d", when, what, answered, code)
			}
		}
	}
	want("before two-step is set up", sensitive(phone.AccessToken), 403)
	var refused errorBody
	if w := call(t, s, "POST", "/v1/me/tokens", phone.AccessToken, KeyCreate{Name: "Script", Scopes: []string{"read"}}, &refused); refused.Error.Code != "two_step_setup_required" {
		t.Errorf("an account that owes two-step was refused with %d %s, want two_step_setup_required", w.Code, refused.Error.Code)
	}

	// Max sets two-step up on a laptop.
	var laptop SignInResponse
	if w := call(t, s, "POST", "/v1/auth/sign-in", "", SignInRequest{Username: "max", Password: testPassword, Device: device(4)}, &laptop); w.Code != 200 || !laptop.Session.TwoStepSetupRequired {
		t.Fatalf("the laptop signed in with %d %s", w.Code, w.Body)
	}
	var setupInfo TwoFactorSetup
	if w := call(t, s, "POST", "/v1/me/two-factor", laptop.Session.AccessToken, TwoFactorEnrol{CurrentPassword: testPassword}, &setupInfo); w.Code != 200 {
		t.Fatalf("starting two-step answered %d: %s", w.Code, w.Body)
	}
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(setupInfo.Secret)
	if err != nil {
		t.Fatal(err)
	}
	if code := status(t, s, "POST", "/v1/me/two-factor/confirm", laptop.Session.AccessToken, TwoFactorVerify{Code: auth.TOTPCode(secret, auth.TOTPStep(now))}); code != 200 {
		t.Fatalf("confirming two-step answered %d", code)
	}

	// The phone was not signed out, and plays on.
	if code, _ := renew(t, s, phone.RefreshToken, 2, "phone"); code != 200 {
		t.Fatalf("the phone was signed out when two-step was set up elsewhere: renewal answered %d", code)
	}
	if code := status(t, s, "GET", "/v1/home", phone.AccessToken, nil); code != 200 {
		t.Errorf("the phone's Home answered %d after two-step was set up elsewhere, want 200", code)
	}
	// It has never shown a code, so the sensitive actions ask for one.
	want("on a sign-in that has shown no code", sensitive(phone.AccessToken), 403)
	if w := call(t, s, "POST", "/v1/me/tokens", phone.AccessToken, KeyCreate{Name: "Script", Scopes: []string{"read"}}, &refused); refused.Error.Code != "two_step_code_required" {
		t.Errorf("a sign-in that has shown no code was refused with %d %s, want two_step_code_required", w.Code, refused.Error.Code)
	}
	if code := status(t, s, "POST", "/v1/me/two-factor/verify", phone.AccessToken, TwoFactorVerify{Code: "000000"}); code != 403 {
		t.Errorf("a wrong code answered %d, want 403", code)
	}
	want("after a wrong code", sensitive(phone.AccessToken), 403)

	// The code, once, and it carries on.
	now = now.Add(time.Minute)
	if code := status(t, s, "POST", "/v1/me/two-factor/verify", phone.AccessToken, TwoFactorVerify{Code: auth.TOTPCode(secret, auth.TOTPStep(now))}); code != 204 {
		t.Fatalf("showing the code answered %d, want 204", code)
	}
	want("after showing the code", sensitive(phone.AccessToken), 200)
}

// The Sign-in settings are changed only from a sign-in that has shown its second step, where
// the owner's account has two-step sign-in.
func TestTheSignInSettingsAskAnEarlierSignInForTheCode(t *testing.T) {
	s := newTestServer(t)
	now := time.Now()
	s.setClock(func() time.Time { return now })
	in := setup(t, s)
	old := onMain(t, s, in).AccessToken
	fresh := signInAs(t, s, in.Account.Username, testPassword, 3)
	var setupInfo TwoFactorSetup
	if w := call(t, s, "POST", "/v1/me/two-factor", fresh.AccessToken, TwoFactorEnrol{CurrentPassword: testPassword}, &setupInfo); w.Code != 200 {
		t.Fatalf("starting two-step answered %d: %s", w.Code, w.Body)
	}
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(setupInfo.Secret)
	if err != nil {
		t.Fatal(err)
	}
	if code := status(t, s, "POST", "/v1/me/two-factor/confirm", fresh.AccessToken, TwoFactorVerify{Code: auth.TOTPCode(secret, auth.TOTPStep(now))}); code != 200 {
		t.Fatalf("confirming two-step answered %d", code)
	}
	saveSignIn(t, s, old, `{"staySignedInDays":365}`, 403)
	saveSignIn(t, s, fresh.AccessToken, `{"staySignedInDays":365}`, 200)
}
