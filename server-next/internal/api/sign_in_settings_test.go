package api

import (
	"encoding/base32"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
)

func saveSignIn(t *testing.T, s *Server, owner, change string, want int) {
	t.Helper()
	if w := call(t, s, "PUT", "/v1/admin/settings/sign-in", owner, json.RawMessage(change), nil); w.Code != want {
		t.Fatalf("saving sign-in settings %s answered %d, want %d: %s", change, w.Code, want, w.Body)
	}
}

// When the owner requires two-step sign-in of everyone, a person who has not set it up signs
// in to a session that can do nothing but set it up; once they have, the same device carries
// on, and they cannot turn it off. Someone it does not apply to signs in as before.
func TestRequiredTwoStepIsSetUpBeforeAnythingElse(t *testing.T) {
	s, owner, _, _ := twoMembers(t)
	now := time.Now()
	s.setClock(func() time.Time { return now })
	saveSignIn(t, s, owner, `{"requireTwoStep":"sometimes"}`, 422)
	saveSignIn(t, s, owner, `{"requireTwoStep":"admins"}`, 200)
	var free SignInResponse
	if w := call(t, s, "POST", "/v1/auth/sign-in", "", SignInRequest{Username: "eve", Password: testPassword, Device: device(3)}, &free); w.Code != 200 ||
		free.Session.TwoStepSetupRequired || status(t, s, "GET", "/v1/profiles", free.Session.AccessToken, nil) != 200 {
		t.Fatalf("a member, with two-step required of admins only, was held back: %d %s", w.Code, w.Body)
	}

	saveSignIn(t, s, owner, `{"requireTwoStep":"everyone"}`, 200)
	var in SignInResponse
	if w := call(t, s, "POST", "/v1/auth/sign-in", "", SignInRequest{Username: "max", Password: testPassword, Device: device(2)}, &in); w.Code != 200 || !in.Session.TwoStepSetupRequired {
		t.Fatalf("a member who must set up two-step signed in with %d %s", w.Code, w.Body)
	}
	token := in.Session.AccessToken
	choose := "/v1/profiles/" + in.Profiles[0].ID + "/select"
	for path, method := range map[string]string{"/v1/profiles": "GET", choose: "POST", "/v1/libraries": "GET", "/v1/devices": "GET"} {
		if w := call(t, s, method, path, token, nil, nil); w.Code != 403 || !strings.Contains(w.Body.String(), "two_step_setup_required") {
			t.Errorf("%s %s before two-step was set up answered %d %s", method, path, w.Code, w.Body)
		}
	}
	if code := status(t, s, "GET", "/v1/me", token, nil); code != 200 {
		t.Errorf("reading who I am before two-step was set up answered %d", code)
	}
	var setupInfo TwoFactorSetup
	if w := call(t, s, "POST", "/v1/me/two-factor", token, TwoFactorEnrol{CurrentPassword: testPassword}, &setupInfo); w.Code != 200 {
		t.Fatalf("starting two-step from a held sign-in answered %d: %s", w.Code, w.Body)
	}
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(setupInfo.Secret)
	if err != nil {
		t.Fatal(err)
	}
	if code := status(t, s, "POST", "/v1/me/two-factor/confirm", token, TwoFactorVerify{Code: auth.TOTPCode(secret, auth.TOTPStep(now))}); code != 200 {
		t.Fatalf("confirming two-step from a held sign-in answered %d", code)
	}
	// The device takes a new token and is an ordinary sign-in.
	code, renewed := renew(t, s, in.Session.RefreshToken, 2, "after-setup")
	if code != 200 || renewed.TwoStepSetupRequired {
		t.Fatalf("renewing after setting two-step up answered %d, still held: %v", code, renewed.TwoStepSetupRequired)
	}
	var onProfile Session
	if w := call(t, s, "POST", choose, renewed.AccessToken, ProfileSelect{}, &onProfile); w.Code != 200 {
		t.Fatalf("choosing a profile after setting two-step up answered %d: %s", w.Code, w.Body)
	}
	now = now.Add(time.Minute) // a code works once
	off := TwoFactorProof{CurrentPassword: testPassword, Code: auth.TOTPCode(secret, auth.TOTPStep(now))}
	if w := call(t, s, "DELETE", "/v1/me/two-factor", onProfile.AccessToken, off, nil); w.Code != 403 || !strings.Contains(w.Body.String(), "two_step_required") {
		t.Errorf("turning required two-step off answered %d %s", w.Code, w.Body)
	}
}

// A sign-in lasts as long as the owner's setting says.
func TestHowLongASignInLasts(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s).AccessToken
	saveSignIn(t, s, owner, `{"staySignedInDays":8}`, 422)
	saveSignIn(t, s, owner, `{"staySignedInDays":7}`, 200)
	got := time.Until(signIn(t, s, 1).Session.SessionExpiresAt)
	if got < 7*24*time.Hour-time.Minute || got > 7*24*time.Hour+time.Minute {
		t.Errorf("with 7 days chosen a new sign-in lasts %v", got)
	}
}

// An owner who cannot sign in asks the sign-in page for a reset code. The server writes it to
// a file in its data folder and tells the page nothing but the file's name; with the code
// from the file the owner chooses a new password. The code works once, the file is removed,
// the old password and the old devices stop, and nobody else's account is touched. Without
// the file's contents nothing a request says changes the owner's password, and five wrong
// codes cancel the code.
func TestTheOwnerGetsBackInWithACodeFromTheDataFolder(t *testing.T) {
	s, owner, _, _ := twoMembers(t)
	now := time.Now()
	s.setClock(func() time.Time { return now })
	file := filepath.Join(s.dataDir, ownerResetFile)
	try := func(username, password string) int {
		return status(t, s, "POST", "/v1/auth/sign-in", "", SignInRequest{Username: username, Password: password, Device: device(7)})
	}
	ask := func(username string) string {
		t.Helper()
		w := call(t, s, "POST", "/v1/auth/owner-reset", "", OwnerResetStart{Username: username}, nil)
		if w.Code != 200 {
			t.Fatalf("asking for a reset code answered %d: %s", w.Code, w.Body)
		}
		return w.Body.String()
	}
	code := func() string {
		t.Helper()
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Split(string(raw), "\n")[2]
	}
	finish := func(code, password string) int {
		return status(t, s, "POST", "/v1/auth/owner-reset/confirm", "", OwnerResetFinish{Code: code, NewPassword: password})
	}
	const fresh = "a brand new password"

	// Whatever name is typed the answer is the same, and it never holds the code.
	answer := ask("justin")
	first := code()
	if info, err := os.Stat(file); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the code file is %v (%v), want one only the server's account reads", info, err)
	}
	if strings.Contains(answer, first) || !strings.Contains(answer, ownerResetFile) {
		t.Fatalf("the page was told %s", answer)
	}
	// While the code is live it is never replaced, however often and however much later
	// anyone asks, and the answer is always the same.
	now = now.Add(5 * time.Minute)
	if again := ask("nobody"); code() != first || again != answer {
		t.Fatalf("asking again while a code was live wrote a new code or answered differently: %s", again)
	}
	if finish("AAAA-AAAA-AAAA-AAAA", fresh) == 200 || try("justin", fresh) == 200 || try("justin", testPassword) != 200 {
		t.Fatal("a wrong code changed the owner's password")
	}
	// Five wrong codes cancel the code, the right one included.
	for n := 0; n < 4; n++ {
		finish("AAAA-AAAA-AAAA-AAAA", fresh)
	}
	if finish(first, fresh) == 200 || try("justin", testPassword) != 200 {
		t.Fatal("the code still worked after five wrong tries")
	}

	// A cancelled code is not replaced at once, only after a minute.
	if again := ask("justin"); code() != first || again != answer {
		t.Fatal("a new code was written straight after the old one was cancelled, or the answer changed")
	}
	now = now.Add(2 * time.Minute)
	ask("justin")
	second := code()
	if second == first {
		t.Fatal("a minute after the old code was cancelled no new one was written")
	}
	if got := finish(second, "short"); got != 400 {
		t.Fatalf("a password too short to be chosen answered %d", got)
	}
	if got := finish(second, fresh); got != 204 && got != 200 {
		t.Fatalf("the code from the file with a new password answered %d", got)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("the file is still there after use: %v", err)
	}
	if finish(second, "yet another password") == 200 || try("justin", "yet another password") == 200 {
		t.Error("the code worked twice")
	}
	if try("justin", fresh) != 200 || try("justin", testPassword) == 200 {
		t.Error("the owner signs in with the old password, or not with the new one")
	}
	if got := status(t, s, "GET", "/v1/me", owner, nil); got != 401 {
		t.Errorf("the owner's old device answers %d, want 401", got)
	}
	if try("max", testPassword) != 200 || try("max", fresh) == 200 {
		t.Error("a member's password was touched by the owner's reset")
	}
	// A code that ran out opens nothing.
	now = now.Add(2 * time.Minute)
	ask("justin")
	late := code()
	now = now.Add(16 * time.Minute)
	if finish(late, "one more password") == 200 {
		t.Error("a code more than 15 minutes old still worked")
	}
	// Both steps are in the owner's activity record.
	var again SignInResponse
	call(t, s, "POST", "/v1/auth/sign-in", "", SignInRequest{Username: "justin", Password: fresh, Device: device(0)}, &again)
	seen := map[string]int{}
	for _, entry := range get[Page[ActivityEntry]](t, s, onMain(t, s, again).AccessToken, "/v1/admin/audit").Items {
		seen[entry.Action]++
	}
	if seen["owner.password_reset"] != 1 || seen["owner.reset_code_written"] != 3 {
		t.Errorf("the activity record has %v, want one reset and three codes written, whatever was asked in between", seen)
	}
}

// Two-step sign-in is required of everyone. Someone who knows Max's
// password, and nothing else, signs in on another device and is held, as they should be. Max
// then signs in on his own phone and sets two-step up with his authenticator. The other
// device never showed a second step: it must not become an ordinary sign-in when Max
// finishes. It is signed out, or stays held; it does not get in.
func TestAHeldSignInOnAnotherDeviceDoesNotGetInWhenTwoStepIsSetUp(t *testing.T) {
	s, owner, _, _ := twoMembers(t)
	now := time.Now()
	s.setClock(func() time.Time { return now })
	saveSignIn(t, s, owner, `{"requireTwoStep":"everyone"}`, 200)

	var other, phone SignInResponse
	if w := call(t, s, "POST", "/v1/auth/sign-in", "", SignInRequest{Username: "max", Password: testPassword, Device: device(4)}, &other); w.Code != 200 || !other.Session.TwoStepSetupRequired {
		t.Fatalf("the other device signed in with %d %s", w.Code, w.Body)
	}
	if w := call(t, s, "POST", "/v1/auth/sign-in", "", SignInRequest{Username: "max", Password: testPassword, Device: device(2)}, &phone); w.Code != 200 || !phone.Session.TwoStepSetupRequired {
		t.Fatalf("Max's phone signed in with %d %s", w.Code, w.Body)
	}
	var setupInfo TwoFactorSetup
	if w := call(t, s, "POST", "/v1/me/two-factor", phone.Session.AccessToken, TwoFactorEnrol{CurrentPassword: testPassword}, &setupInfo); w.Code != 200 {
		t.Fatalf("starting two-step on the phone answered %d: %s", w.Code, w.Body)
	}
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(setupInfo.Secret)
	if err != nil {
		t.Fatal(err)
	}
	if code := status(t, s, "POST", "/v1/me/two-factor/confirm", phone.Session.AccessToken, TwoFactorVerify{Code: auth.TOTPCode(secret, auth.TOTPStep(now))}); code != 200 {
		t.Fatalf("confirming two-step on the phone answered %d", code)
	}

	// The other device, which has only ever shown the password, renews.
	code, renewed := renew(t, s, other.Session.RefreshToken, 4, "other-device")
	if code == 200 && !renewed.TwoStepSetupRequired {
		if got := status(t, s, "GET", "/v1/profiles", renewed.AccessToken, nil); got == 200 {
			t.Fatalf("a device that showed only the password became an ordinary sign-in when two-step was set up elsewhere: it reads the account's profiles (%d)", got)
		}
	}

	// Max's own phone, which did confirm, carries on as an ordinary sign-in.
	if code, mine := renew(t, s, phone.Session.RefreshToken, 2, "phone"); code != 200 || mine.TwoStepSetupRequired {
		t.Errorf("the phone that set two-step up renewed with %d, still held: %v", code, mine.TwoStepSetupRequired)
	}
}
