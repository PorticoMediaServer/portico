package api

import (
	"encoding/base32"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
)

// onMainProfile sets a server up and returns the owner's session on their main profile.
func onMainProfile(t *testing.T, s *Server) Session {
	t.Helper()
	return onMain(t, s, setup(t, s))
}

// Two-step sign-in from setting it up to turning it off: it is managed from the main
// profile, the password alone no longer signs in or changes the password, an authenticator
// code works once, a recovery code works once, a challenge gives up after five wrong codes,
// and wrong codes make the wait grow although the password was right.
func TestTwoStepSignIn(t *testing.T) {
	s := newTestServer(t)
	now := time.Now()
	s.now = func() time.Time { return now }
	first := setup(t, s)
	if code := status(t, s, "POST", "/v1/me/two-factor", first.Session.AccessToken, TwoFactorEnrol{CurrentPassword: testPassword}); code != 403 {
		t.Fatalf("a sign-in that is not on the main profile set up two-step sign-in (%d)", code)
	}
	token := onMain(t, s, first).AccessToken

	if code := status(t, s, "POST", "/v1/me/two-factor", token, TwoFactorEnrol{CurrentPassword: "not the password"}); code != 403 {
		t.Fatalf("enrolling with a wrong password answered %d, want 403", code)
	}
	var setupInfo TwoFactorSetup
	if w := call(t, s, "POST", "/v1/me/two-factor", token, TwoFactorEnrol{CurrentPassword: testPassword}, &setupInfo); w.Code != 200 {
		t.Fatalf("enrol answered %d: %s", w.Code, w.Body)
	}
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(setupInfo.Secret)
	if err != nil {
		t.Fatal(err)
	}
	app := func() string { return auth.TOTPCode(secret, auth.TOTPStep(now)) }
	wrong := func() string {
		if app() == "000000" {
			return "111111"
		}
		return "000000"
	}
	// Until the app is confirmed, the password still signs in on its own.
	if got := signIn(t, s, 1); got.Challenge != nil {
		t.Fatal("an unconfirmed authenticator already demands a second step")
	}
	if code := status(t, s, "POST", "/v1/me/two-factor/confirm", token, TwoFactorVerify{Code: wrong()}); code != 403 {
		t.Fatalf("confirming with a wrong code answered %d, want 403", code)
	}
	var recovery RecoveryCodes
	if w := call(t, s, "POST", "/v1/me/two-factor/confirm", token, TwoFactorVerify{Code: app()}, &recovery); w.Code != 200 || len(recovery.RecoveryCodes) != 10 {
		t.Fatalf("confirm answered %d with %d recovery codes", w.Code, len(recovery.RecoveryCodes))
	}

	challenge := func() string {
		t.Helper()
		got := signIn(t, s, 1)
		if got.Challenge == nil || got.Session.AccessToken != "" || got.Session.RefreshToken != "" {
			t.Fatalf("the password alone signed in to an account with two-step sign-in: %+v", got)
		}
		return got.Challenge.Token
	}
	finish := func(challenge, code string) int {
		var out SignInResponse
		w := call(t, s, "POST", "/v1/auth/two-factor", "", TwoFactorFinish{Challenge: challenge, Code: code}, nil)
		if w.Code == 200 {
			jsonInto(w, &out)
			if status(t, s, "GET", "/v1/me", out.Session.AccessToken, nil) != 200 {
				t.Fatal("the session from the second step does not work")
			}
			token = onMain(t, s, out).AccessToken
		}
		return w.Code
	}
	// The code that confirmed the app was used in this 30 seconds; it cannot be used again.
	if code := finish(challenge(), app()); code != 403 {
		t.Fatalf("a code that was already used answered %d, want 403", code)
	}
	now = now.Add(31 * time.Second)
	c := challenge()
	if code := finish(c, wrong()); code != 403 {
		t.Fatalf("a wrong code answered %d, want 403", code)
	}
	if code := finish(c, app()); code != 200 {
		t.Fatalf("the right code answered %d, want 200", code)
	}
	if code := finish(c, app()); code != 401 {
		t.Fatalf("a finished challenge answered %d, want 401", code)
	}
	// A recovery code works once, typed however the person types it.
	typed := " " + recovery.RecoveryCodes[0][:4] + " " + recovery.RecoveryCodes[0][5:] + " "
	if code := finish(challenge(), typed); code != 200 {
		t.Fatalf("a recovery code answered %d, want 200", code)
	}
	if code := finish(challenge(), recovery.RecoveryCodes[0]); code != 403 {
		t.Fatalf("a used recovery code answered %d, want 403", code)
	}
	// Five wrong codes and the challenge is gone, even for the right code.
	now = now.Add(31 * time.Second)
	c = challenge()
	for i := range 5 {
		if code := finish(c, wrong()); code != 403 {
			t.Fatalf("wrong code %d answered %d, want 403", i, code)
		}
		now = now.Add(3 * time.Second) // wrong codes are slowed like wrong passwords
	}
	if code := finish(c, app()); code != 401 {
		t.Fatalf("a challenge with five wrong codes still answered %d to the right one, want 401", code)
	}
	// Giving the right password again does not wipe that record: one more wrong code and
	// the wait is longer still, for the right code too.
	c = challenge()
	if code := finish(c, wrong()); code != 403 {
		t.Fatalf("a wrong code on a new challenge answered %d, want 403", code)
	}
	if code := finish(c, app()); code != 429 {
		t.Fatalf("after seven wrong codes the next try answered %d, want 429", code)
	}
	now = now.Add(31 * time.Second)
	if code := finish(c, app()); code != 200 {
		t.Fatalf("the right code after the wait answered %d, want 200", code)
	}

	// With two-step sign-in on, the password alone does not change the password.
	change := PasswordRequest{CurrentPassword: testPassword, NewPassword: testPassword}
	if code := status(t, s, "POST", "/v1/auth/password", token, change); code != 403 {
		t.Fatalf("a password change without the two-step code answered %d, want 403", code)
	}
	now = now.Add(31 * time.Second)
	change.Code = app()
	if code := status(t, s, "POST", "/v1/auth/password", token, change); code != 204 {
		t.Fatalf("a password change with the two-step code answered %d, want 204", code)
	}

	// Turning it off takes the password and a code. Then the password signs in alone again.
	now = now.Add(31 * time.Second)
	if code := status(t, s, "DELETE", "/v1/me/two-factor", token, TwoFactorProof{CurrentPassword: testPassword, Code: wrong()}); code != 403 {
		t.Fatalf("turning it off with a wrong code answered %d, want 403", code)
	}
	if code := status(t, s, "DELETE", "/v1/me/two-factor", token, TwoFactorProof{CurrentPassword: testPassword, Code: app()}); code != 204 {
		t.Fatalf("turning it off answered %d, want 204", code)
	}
	if got := signIn(t, s, 1); got.Challenge != nil || got.Session.AccessToken == "" {
		t.Fatal("two-step sign-in is still demanded after it was turned off")
	}
}

// A TV shows a code, a signed-in phone approves it, and the TV is signed in to that
// person's account at the profile chooser, with no more rights than the approver. However
// many times the TV asks at once, one session is made.
func TestSignInOnATVByCode(t *testing.T) {
	s := newTestServer(t)
	phone := onMainProfile(t, s)
	start := func() CodeStarted {
		t.Helper()
		var out CodeStarted
		if w := call(t, s, "POST", "/v1/auth/codes", "", CodeStart{Device: DeviceInfo{InstallationID: device(7).InstallationID, Name: "Living room TV"}}, &out); w.Code != 200 {
			t.Fatalf("start answered %d: %s", w.Code, w.Body)
		}
		return out
	}
	collect := func(code, secret string) (int, CodeResult) {
		var out CodeResult
		w := call(t, s, "POST", "/v1/auth/codes/"+code+"/redeem", "", CodeCollect{DeviceSecret: secret}, nil)
		if w.Code == 200 {
			jsonInto(w, &out)
		}
		return w.Code, out
	}

	for run := range max(raceRuns()/3, 3) {
		tv := start()
		if code, got := collect(tv.Code, tv.DeviceSecret); code != 200 || got.Status != "pending" {
			t.Fatalf("before a decision the TV was told %d %q", code, got.Status)
		}
		var review CodeReview
		if w := call(t, s, "GET", "/v1/auth/codes/"+tv.Code, phone.AccessToken, nil, &review); w.Code != 200 || review.Device.Name != "Living room TV" {
			t.Fatalf("review answered %d %+v", w.Code, review)
		}
		if code := status(t, s, "POST", "/v1/auth/codes/"+tv.Code+"/approve", phone.AccessToken, CodeDecision{}); code != 204 {
			t.Fatalf("approve answered %d", code)
		}
		var approved atomic.Int32
		var signedIn SignInResponse
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				if code, got := collect(tv.Code, tv.DeviceSecret); code == 200 && got.Status == "approved" {
					approved.Add(1)
					signedIn = *got.SignIn
				}
			})
		}
		wg.Wait()
		if approved.Load() != 1 {
			t.Fatalf("run %d: one approval made %d sessions; want exactly 1", run, approved.Load())
		}
		// The TV asks who is watching. Approved from the main profile, it may go there as the owner.
		if v := signedIn.Session.Viewer; v.ProfileID != "" || v.Role != "account" || v.AccountID != phone.Viewer.AccountID {
			t.Fatalf("the TV did not start at the profile chooser of the approver's account: %+v", v)
		}
		if tvMain := onMain(t, s, signedIn); tvMain.Viewer.Role != "owner" {
			t.Fatalf("a TV approved from the main profile is %q there, want owner", tvMain.Viewer.Role)
		}
	}

	// Approved from another profile, the TV never has more rights than that profile had,
	// even on the main profile.
	var kid Profile
	call(t, s, "POST", "/v1/profiles", phone.AccessToken, ProfileCreate{Name: "Kid"}, &kid)
	var tablet Session
	call(t, s, "POST", "/v1/profiles/"+kid.ID+"/select", signIn(t, s, 8).Session.AccessToken, ProfileSelect{}, &tablet)
	limited := start()
	if code := status(t, s, "POST", "/v1/auth/codes/"+limited.Code+"/approve", tablet.AccessToken, CodeDecision{}); code != 204 {
		t.Fatalf("approving from the kid's profile answered %d", code)
	}
	_, got := collect(limited.Code, limited.DeviceSecret)
	if got.SignIn == nil {
		t.Fatal("the TV approved from the kid's profile was not signed in")
	}
	tvMain := onMain(t, s, *got.SignIn)
	if tvMain.Viewer.Role != "member" || status(t, s, "POST", "/v1/profiles", tvMain.AccessToken, ProfileCreate{Name: "Sneaky"}) != 403 {
		t.Fatalf("a TV approved from a child's profile got the owner's rights on the main profile: %+v", tvMain.Viewer)
	}

	denied := start()
	status(t, s, "POST", "/v1/auth/codes/"+denied.Code+"/approve", phone.AccessToken, CodeDecision{Deny: true})
	if code := status(t, s, "POST", "/v1/auth/codes/"+denied.Code+"/approve", phone.AccessToken, CodeDecision{}); code != 404 {
		t.Errorf("a second decision on one code answered %d, want 404", code)
	}
	if code, _ := collect(denied.Code, denied.DeviceSecret); code != 403 {
		t.Errorf("a denied TV was told %d, want 403", code)
	}
	if code, _ := collect(denied.Code, "no-such-secret"); code != 410 {
		t.Errorf("an unknown secret was told %d, want 410", code)
	}
	if code := status(t, s, "GET", "/v1/auth/codes/AAAA-AAAA", phone.AccessToken, nil); code != 404 {
		t.Errorf("a code nobody issued answered %d, want 404", code)
	}
	// Approving needs a signed-in person on a profile.
	other := start()
	if code := status(t, s, "POST", "/v1/auth/codes/"+other.Code+"/approve", "", CodeDecision{}); code != 401 {
		t.Errorf("approving without a sign-in answered %d, want 401", code)
	}
}
