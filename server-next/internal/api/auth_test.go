package api

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func raceRuns() int {
	if n, err := strconv.Atoi(os.Getenv("PORTICO_RACE_RUNS")); err == nil && n > 0 {
		return n
	}
	return 30
}

const testPassword = "correct horse"

func device(n int) DeviceInfo {
	return DeviceInfo{InstallationID: fmt.Sprintf("install-%027d", n), Name: fmt.Sprintf("Device %d", n)}
}

// setup creates the owner "justin" and returns the first session.
func setup(t *testing.T, s *Server) SignInResponse {
	t.Helper()
	var out SignInResponse
	body := SetupOwnerRequest{SetupCode: s.setupCode, Username: "Justin", Password: testPassword, Device: device(0)}
	if w := call(t, s, "POST", "/v1/setup/owner", "", body, &out); w.Code != 200 {
		t.Fatalf("setup answered %d: %s", w.Code, w.Body)
	}
	return out
}

// onMain puts a new sign-in on the account's main profile, which has no PIN, and returns
// its session: the one place the account can be managed from.
func onMain(t *testing.T, s *Server, in SignInResponse) Session {
	t.Helper()
	var out Session
	if w := call(t, s, "POST", "/v1/profiles/"+in.Profiles[0].ID+"/select", in.Session.AccessToken, ProfileSelect{}, &out); w.Code != 200 {
		t.Fatalf("choosing the main profile answered %d: %s", w.Code, w.Body)
	}
	out.RefreshToken = in.Session.RefreshToken
	return out
}

func signIn(t *testing.T, s *Server, n int) SignInResponse {
	t.Helper()
	var out SignInResponse
	if w := call(t, s, "POST", "/v1/auth/sign-in", "", SignInRequest{Username: "justin", Password: testPassword, Device: device(n)}, &out); w.Code != 200 {
		t.Fatalf("sign-in answered %d: %s", w.Code, w.Body)
	}
	return out
}

// renew sends one renewal and returns the status and the new session.
func renew(t *testing.T, s *Server, refreshToken string, n int, requestID string) (int, Session) {
	t.Helper()
	var out Session
	w := call(t, s, "POST", "/v1/auth/refresh", "", RefreshRequest{RefreshToken: refreshToken, InstallationID: device(n).InstallationID, RequestID: requestID}, nil)
	if w.Code == 200 {
		if err := jsonInto(w, &out); err != nil {
			t.Fatal(err)
		}
	}
	return w.Code, out
}

func status(t *testing.T, s *Server, method, path, token string, body any) int {
	t.Helper()
	return call(t, s, method, path, token, body, nil).Code
}

// The first person to set up a new server becomes its owner, exactly once, and can then
// use the session: read who they are, choose their profile and act as the owner.
func TestSetupSignInAndChooseAProfile(t *testing.T) {
	s := newTestServer(t)
	// Reaching a new server is not enough to own it: setup needs the code from its log or
	// its data folder, and the server tells nobody what that is.
	file, _ := os.ReadFile(s.setupFile)
	if strings.TrimSpace(string(file)) != s.setupCode || len(s.setupCode) < 9 {
		t.Fatalf("the setup code file holds %q, the server expects %q", file, s.setupCode)
	}
	for _, code := range []string{"", "AAAA-AAAA"} {
		body := SetupOwnerRequest{SetupCode: code, Username: "mallory", Password: testPassword, Device: device(9)}
		if got := status(t, s, "POST", "/v1/setup/owner", "", body); got != 403 {
			t.Fatalf("setup with the code %q answered %d, want 403", code, got)
		}
	}
	if w := call(t, s, "GET", "/v1/system", "", nil, nil); strings.Contains(w.Body.String(), s.setupCode) || strings.Contains(w.Body.String(), "version") {
		t.Fatalf("a stranger was told the setup code or the version: %s", w.Body)
	}
	long := SetupOwnerRequest{SetupCode: s.setupCode, Username: "justin", Password: strings.Repeat("long", 19), Device: device(0)}
	if w := call(t, s, "POST", "/v1/setup/owner", "", long, nil); w.Code != 400 || !strings.Contains(w.Body.String(), "Use a shorter password.") {
		t.Fatalf("a 76-character password answered %d %s", w.Code, w.Body)
	}
	first := setup(t, s)
	if _, err := os.Stat(s.setupFile); err == nil {
		t.Error("the setup code file is still there after setup")
	}
	if first.Account.Role != "owner" || len(first.Profiles) != 1 || !first.Profiles[0].IsPrimary || first.Session.Viewer.Role != "account" {
		t.Fatalf("unexpected setup answer: %+v", first)
	}
	again := SetupOwnerRequest{SetupCode: s.setupCode, Username: "mallory", Password: testPassword, Device: device(9)}
	if code := status(t, s, "POST", "/v1/setup/owner", "", again); code != 409 {
		t.Fatalf("a second setup answered %d, want 409", code)
	}
	var system System
	call(t, s, "GET", "/v1/system", "", nil, &system)
	if system.SetupRequired {
		t.Fatal("setup is still required after the owner was created")
	}
	var chosen Session
	if w := call(t, s, "POST", "/v1/profiles/"+first.Profiles[0].ID+"/select", first.Session.AccessToken, ProfileSelect{}, &chosen); w.Code != 200 {
		t.Fatalf("select answered %d: %s", w.Code, w.Body)
	}
	if chosen.Viewer.Role != "owner" || chosen.Viewer.ProfileID != first.Profiles[0].ID || chosen.RefreshToken != "" {
		t.Fatalf("unexpected session after choosing the main profile: %+v", chosen)
	}
	// The token from before the choice stops working, so one device is on one profile at a time.
	if code := status(t, s, "GET", "/v1/me", first.Session.AccessToken, nil); code != 401 {
		t.Errorf("the token from before the profile was chosen answered %d, want 401", code)
	}
	// The username is kept in lower case, however it was typed.
	var me Me
	if w := call(t, s, "GET", "/v1/me", chosen.AccessToken, nil, &me); w.Code != 200 || me.Profile == nil || me.Role != "owner" || me.Account.Username != "justin" {
		t.Fatalf("me answered %d %+v", w.Code, me)
	}
	call(t, s, "GET", "/v1/system", chosen.AccessToken, nil, &system)
	if system.Version != "test" {
		t.Errorf("a signed-in caller was told the version %q, want %q", system.Version, "test")
	}
}

// Many people racing to set up a new server get one owner between them.
func TestOnlyOneSetupWins(t *testing.T) {
	for run := range max(raceRuns()/10, 3) {
		s := newTestServer(t)
		var won atomic.Int32
		var wg sync.WaitGroup
		for i := range 8 {
			wg.Go(func() {
				body := SetupOwnerRequest{SetupCode: s.setupCode, Username: fmt.Sprintf("person%d", i), Password: testPassword, Device: device(i)}
				if status(t, s, "POST", "/v1/setup/owner", "", body) == 200 {
					won.Add(1)
				}
			})
		}
		wg.Wait()
		if won.Load() != 1 {
			t.Fatalf("run %d: %d setups succeeded; want exactly 1", run, won.Load())
		}
	}
}

// A renewal whose reply was lost is repeated with the request id the app saved, and gets the
// very same tokens. Using a replaced token any other way means it leaked: the session ends.
func TestRenewalRepeatsSafelyAndReuseEndsTheSession(t *testing.T) {
	s := newTestServer(t)
	a := setup(t, s).Session

	code, b := renew(t, s, a.RefreshToken, 0, "request-1")
	if code != 200 || b.RefreshToken == a.RefreshToken || b.TokenGeneration != "2" || b.SessionID != a.SessionID {
		t.Fatalf("renewal answered %d %+v", code, b)
	}
	for range 3 {
		code, again := renew(t, s, a.RefreshToken, 0, "request-1")
		if code != 200 || again != b {
			t.Fatalf("the repeated renewal answered %d and different tokens:\n first %+v\n again %+v", code, b, again)
		}
	}
	// Another install cannot use the token, and trying does not end the session.
	if code, _ := renew(t, s, b.RefreshToken, 5, "request-2"); code != 401 {
		t.Fatalf("a renewal from another install answered %d, want 401", code)
	}
	code, c := renew(t, s, b.RefreshToken, 0, "request-2")
	if code != 200 || c.TokenGeneration != "3" {
		t.Fatalf("second renewal answered %d %+v", code, c)
	}
	if status(t, s, "GET", "/v1/me", c.AccessToken, nil) != 200 {
		t.Fatal("the renewed access token does not work")
	}
	// The first token has been replaced twice. Presenting it now ends the session for everyone.
	if code, _ := renew(t, s, a.RefreshToken, 0, "request-1"); code != 401 {
		t.Fatalf("an old refresh token answered %d, want 401", code)
	}
	if code, _ := renew(t, s, c.RefreshToken, 0, "request-3"); code != 401 {
		t.Errorf("the newest refresh token still works after an old one was reused (%d)", code)
	}
	if code := status(t, s, "GET", "/v1/me", c.AccessToken, nil); code != 401 {
		t.Errorf("the access token still works after the session was ended for reuse (%d)", code)
	}

	// The previous token with a different request id is not the same renewal repeated.
	d := signIn(t, s, 1).Session
	if code, _ := renew(t, s, d.RefreshToken, 1, "request-1"); code != 200 {
		t.Fatal("renewal failed")
	}
	if code, _ := renew(t, s, d.RefreshToken, 1, "some-other-request"); code != 401 {
		t.Errorf("the previous token with another request id answered %d, want 401", code)
	}
	if code := status(t, s, "GET", "/v1/me", d.AccessToken, nil); code != 401 {
		t.Errorf("the session survived its previous token being used with another request id (%d)", code)
	}
}

// Many copies of one renewal sent at once, as tabs of one browser do, all get the same
// tokens and the session lives on.
func TestTheSameRenewalSentTogetherGivesOneAnswer(t *testing.T) {
	s := newTestServer(t)
	current := setup(t, s).Session
	for run := range raceRuns() {
		answers := make([]Session, 8)
		var wg sync.WaitGroup
		for i := range answers {
			wg.Go(func() {
				if code, out := renew(t, s, current.RefreshToken, 0, fmt.Sprintf("request-%d", run)); code == 200 {
					answers[i] = out
				}
			})
		}
		wg.Wait()
		for i, got := range answers {
			if got != answers[0] || got.RefreshToken == "" {
				t.Fatalf("run %d: copy %d of one renewal got a different answer", run, i)
			}
		}
		current = answers[0]
	}
}

// Once ending a session has returned, nothing works with it again, however the ending
// raced with renewals and requests that were under way.
func TestNothingWorksAfterASessionIsEnded(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s))
	for run := range raceRuns() {
		session := signIn(t, s, run+1).Session
		var mu sync.Mutex
		latest := session
		stop := make(chan struct{})
		var wg sync.WaitGroup
		wg.Go(func() {
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				mu.Lock()
				token := latest.RefreshToken
				mu.Unlock()
				if code, next := renew(t, s, token, run+1, fmt.Sprintf("r%d", i)); code == 200 {
					mu.Lock()
					latest = next
					mu.Unlock()
				}
			}
		})
		// End it from the owner's other session, by id, or from itself; both paths must hold.
		if run%2 == 0 {
			if code := status(t, s, "DELETE", "/v1/me/sessions/"+session.SessionID, owner.AccessToken, nil); code != 204 {
				t.Fatalf("ending the session answered %d", code)
			}
		} else if code := status(t, s, "DELETE", "/v1/auth/session", session.AccessToken, nil); code != 204 {
			t.Fatalf("sign-out answered %d", code)
		}
		ended := time.Now()
		for time.Since(ended) < 2*time.Millisecond {
			mu.Lock()
			now := latest
			mu.Unlock()
			if code := status(t, s, "GET", "/v1/me", now.AccessToken, nil); code != 401 {
				t.Fatalf("run %d: an access token worked after its session was ended (%d)", run, code)
			}
		}
		close(stop)
		wg.Wait()
		if code, _ := renew(t, s, latest.RefreshToken, run+1, "after-the-end"); code != 401 {
			t.Fatalf("run %d: a renewal worked after its session was ended (%d)", run, code)
		}
		if code := status(t, s, "GET", "/v1/me", latest.AccessToken, nil); code != 401 {
			t.Fatalf("run %d: the last access token worked after its session was ended (%d)", run, code)
		}
	}
	// Signing out with the "{}" many HTTP libraries send as a body is fine.
	if code := status(t, s, "DELETE", "/v1/auth/session", signIn(t, s, 0).Session.AccessToken, struct{}{}); code != 204 {
		t.Errorf("sign-out with a {} body answered %d, want 204", code)
	}
	// Signing out twice, or with a token that has run out, is fine.
	last := signIn(t, s, 0).Session
	s.now = func() time.Time { return time.Now().Add(time.Hour) }
	for range 2 {
		if code := status(t, s, "DELETE", "/v1/auth/session", last.AccessToken, nil); code != 204 {
			t.Errorf("sign-out with an expired token answered %d, want 204", code)
		}
	}
	if code, _ := renew(t, s, last.RefreshToken, 0, "r"); code != 401 {
		t.Errorf("a renewal worked after sign-out with an expired token (%d)", code)
	}
}

// Changing the password needs the main profile and the current password. It signs the
// account's other devices out at once and keeps the one that made the change; only the new
// password works afterwards.
func TestPasswordChangeEndsEveryOtherSession(t *testing.T) {
	s := newTestServer(t)
	one := onMain(t, s, setup(t, s))
	two := signIn(t, s, 1).Session
	change := PasswordRequest{CurrentPassword: testPassword, NewPassword: "a new password"}
	if code := status(t, s, "POST", "/v1/auth/password", two.AccessToken, change); code != 403 {
		t.Fatalf("a sign-in that is not on the main profile changed the password (%d)", code)
	}
	if code := status(t, s, "POST", "/v1/auth/password", one.AccessToken, PasswordRequest{CurrentPassword: "not the password", NewPassword: "a new password"}); code != 403 {
		t.Fatalf("a wrong current password answered %d, want 403", code)
	}
	if status(t, s, "GET", "/v1/me", two.AccessToken, nil) != 200 {
		t.Fatal("a refused password change ended a session")
	}
	if code := status(t, s, "POST", "/v1/auth/password", one.AccessToken, change); code != 204 {
		t.Fatalf("password change answered %d", code)
	}
	if code := status(t, s, "GET", "/v1/me", one.AccessToken, nil); code != 200 {
		t.Errorf("the device that changed the password was signed out (%d)", code)
	}
	if code, _ := renew(t, s, one.RefreshToken, 0, "r"); code != 200 {
		t.Errorf("the device that changed the password cannot renew (%d)", code)
	}
	if code := status(t, s, "GET", "/v1/me", two.AccessToken, nil); code != 401 {
		t.Errorf("another device's access token still works after the password changed (%d)", code)
	}
	if code, _ := renew(t, s, two.RefreshToken, 1, "r"); code != 401 {
		t.Errorf("another device still renews after the password changed (%d)", code)
	}
	old := SignInRequest{Username: "justin", Password: testPassword, Device: device(2)}
	if code := status(t, s, "POST", "/v1/auth/sign-in", "", old); code != 401 {
		t.Errorf("the old password answered %d, want 401", code)
	}
	old.Password = "a new password"
	if code := status(t, s, "POST", "/v1/auth/sign-in", "", old); code != 200 {
		t.Errorf("the new password answered %d, want 200", code)
	}
}

// Wrong passwords slow down the network they come from. A username nobody has is answered
// exactly like a wrong password for a real one, guess after guess, including when the wait
// starts. Trying another username does not skip the wait, and the same account can still
// be signed in to from another network. The wait survives a restart, is refused even with
// the right password, never locks the account for good, and ends by itself.
func TestGuessingIsSlowedTheSameForKnownAndUnknownNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.db")
	s := openTestServer(t, path)
	setup(t, s)
	now := time.Now()
	s.now = func() time.Time { return now }

	guess := func(s *Server, from, username, password string) (int, string) {
		r := newRequest("POST", "/v1/auth/sign-in", "", SignInRequest{Username: username, Password: password, Device: device(3)})
		r.RemoteAddr = from + ":4000"
		w := serve(s, r)
		var body errorBody
		jsonInto(w, &body)
		body.Error.RequestID = ""
		return w.Code, fmt.Sprintf("%+v retry-after=%s", body.Error, w.Header().Get("Retry-After"))
	}
	const cafe, library, elsewhere = "198.51.100.7", "198.51.100.8", "198.51.100.9"
	sawWait := false
	for i := range 8 {
		knownCode, known := guess(s, cafe, "justin", "wrong password")
		unknownCode, unknown := guess(s, library, "nobody-here", "wrong password")
		if knownCode != unknownCode || known != unknown {
			t.Fatalf("guess %d: a real and an unknown username were answered differently:\n real    %d %s\n unknown %d %s", i, knownCode, known, unknownCode, unknown)
		}
		if knownCode == 429 {
			sawWait = true
			now = now.Add(2 * time.Minute) // wait it out and guess again
		} else if knownCode != 401 {
			t.Fatalf("guess %d answered %d", i, knownCode)
		}
	}
	if !sawWait {
		t.Fatal("eight wrong guesses were never asked to wait")
	}
	// One more wrong guess starts a wait. Restart the server inside it.
	if code, _ := guess(s, cafe, "justin", "wrong password"); code != 401 {
		t.Fatalf("the guess after waiting answered %d, want 401", code)
	}
	closeDB(s.db)
	s = openTestServer(t, path)
	s.now = func() time.Time { return now }
	if code, _ := guess(s, cafe, "justin", testPassword); code != 429 {
		t.Fatalf("after a restart the right password answered %d inside the wait, want 429", code)
	}
	if code, _ := guess(s, cafe, "someone-else", "wrong password"); code != 429 {
		t.Fatalf("trying another username from the waiting network answered %d, want 429", code)
	}
	if code, _ := guess(s, elsewhere, "justin", testPassword); code != 200 {
		t.Fatalf("the right password from another network answered %d while one network waits, want 200", code)
	}
	now = now.Add(2 * time.Minute)
	if code, _ := guess(s, cafe, "justin", testPassword); code != 200 {
		t.Fatalf("after the wait the right password answered %d, want 200", code)
	}
}

// A burst of guesses sent together gets no more tries than the same guesses sent in turn.
func TestGuessesSentTogetherGainNothing(t *testing.T) {
	s := newTestServer(t)
	setup(t, s)
	var refused, waited atomic.Int32
	var wg sync.WaitGroup
	for range 40 {
		wg.Go(func() {
			switch status(t, s, "POST", "/v1/auth/sign-in", "", SignInRequest{Username: "justin", Password: "wrong password", Device: device(3)}) {
			case 401:
				refused.Add(1)
			case 429:
				waited.Add(1)
			}
		})
	}
	wg.Wait()
	if refused.Load() > 5 || refused.Load()+waited.Load() != 40 {
		t.Fatalf("%d of 40 guesses sent together were checked (%d told to wait); at most 5 may be", refused.Load(), waited.Load())
	}
}

// Profiles: at most eight, only the main profile may change them, a PIN guards selection
// and slows guessing on the device that guesses, changing a PIN sends other devices back to
// the chooser, and a forgotten PIN is replaced with the account's password.
func TestProfilesPINsAndWhoMayManage(t *testing.T) {
	s := newTestServer(t)
	now := time.Now()
	s.now = func() time.Time { return now }
	first := setup(t, s)
	mainID := first.Profiles[0].ID
	// A sign-in that has not chosen the main profile manages nothing, PIN or no PIN.
	if code := status(t, s, "POST", "/v1/profiles", first.Session.AccessToken, ProfileCreate{Name: "Too soon"}); code != 403 {
		t.Fatalf("a sign-in still at the profile chooser created a profile (%d)", code)
	}
	account := onMain(t, s, first).AccessToken

	var kid Profile
	r := call(t, s, "POST", "/v1/profiles", account, ProfileCreate{Name: "Kid", Color: "mint"}, &kid)
	if r.Code != 200 || kid.Name != "Kid" || kid.IsPrimary {
		t.Fatalf("create answered %d %+v", r.Code, kid)
	}
	for i := range 6 {
		if code := status(t, s, "POST", "/v1/profiles", account, ProfileCreate{Name: fmt.Sprintf("Guest %d", i)}); code != 200 {
			t.Fatalf("creating profile %d answered %d", i+3, code)
		}
	}
	if code := status(t, s, "POST", "/v1/profiles", account, ProfileCreate{Name: "One too many"}); code != 409 {
		t.Errorf("a ninth profile answered %d, want 409", code)
	}
	if code := status(t, s, "DELETE", "/v1/profiles/"+mainID, account, nil); code != 409 {
		t.Errorf("deleting the main profile answered %d, want 409", code)
	}
	// A name is measured in characters: 120 of any script fit.
	if code := status(t, s, "PATCH", "/v1/profiles/"+kid.ID, account, map[string]string{"name": strings.Repeat("é", 120)}); code != 200 {
		t.Errorf("a profile name of 120 accented characters answered %d, want 200", code)
	}

	// A second device goes onto the kid's profile. From there the account cannot be managed.
	tablet := signIn(t, s, 1).Session
	var onKid Session
	call(t, s, "POST", "/v1/profiles/"+kid.ID+"/select", tablet.AccessToken, ProfileSelect{}, &onKid)
	if onKid.Viewer.Role != "member" {
		t.Fatalf("the owner's second profile has role %q, want member", onKid.Viewer.Role)
	}
	for _, c := range []struct{ method, path string }{
		{"POST", "/v1/profiles"}, {"PATCH", "/v1/profiles/" + mainID}, {"DELETE", "/v1/profiles/" + kid.ID},
		{"PUT", "/v1/profiles/" + mainID + "/pin"}, {"DELETE", "/v1/profiles/" + mainID + "/pin"},
		{"GET", "/v1/me/sessions"}, {"GET", "/v1/devices"}, {"DELETE", "/v1/devices/" + tablet.DeviceID},
	} {
		var body any
		if c.method == "POST" || c.method == "PATCH" || c.method == "PUT" {
			body = map[string]string{"name": "Mine now", "pin": "1111"}
		}
		if code := status(t, s, c.method, c.path, onKid.AccessToken, body); code != 403 {
			t.Errorf("%s %s from a child profile answered %d, want 403", c.method, c.path, code)
		}
	}

	// The parent puts a PIN on the main profile.
	pin := "4821"
	if code := status(t, s, "PUT", "/v1/profiles/"+mainID+"/pin", account, ProfilePIN{PIN: pin}); code != 200 {
		t.Fatalf("setting a PIN answered %d", code)
	}
	// Wrong PINs are refused and then slowed; inside the wait even the right PIN is refused
	// on that device. Another device is not made to wait by it.
	fresh := signIn(t, s, 2).Session
	sel := "/v1/profiles/" + mainID + "/select"
	if code := status(t, s, "POST", sel, fresh.AccessToken, ProfileSelect{PIN: "0000"}); code != 403 {
		t.Fatalf("a wrong PIN answered %d, want 403", code)
	}
	if code := status(t, s, "POST", sel, fresh.AccessToken, ProfileSelect{PIN: pin}); code != 429 {
		t.Fatalf("the right PIN inside the wait answered %d, want 429", code)
	}
	if code := status(t, s, "POST", sel, signIn(t, s, 4).Session.AccessToken, ProfileSelect{PIN: pin}); code != 200 {
		t.Fatalf("the right PIN on another device answered %d while one device waits, want 200", code)
	}
	now = now.Add(3 * time.Second)
	var main Session
	if w := call(t, s, "POST", sel, fresh.AccessToken, ProfileSelect{PIN: pin}, &main); w.Code != 200 || main.Viewer.Role != "owner" {
		t.Fatalf("the right PIN after the wait answered %d %+v", w.Code, main)
	}

	// The kid's profile gets a PIN: the tablet on it must renew, and lands back at the chooser.
	if code := status(t, s, "PUT", "/v1/profiles/"+kid.ID+"/pin", main.AccessToken, ProfilePIN{PIN: pin}); code != 200 {
		t.Fatalf("setting the kid's PIN answered %d", code)
	}
	if code := status(t, s, "GET", "/v1/me", onKid.AccessToken, nil); code != 401 {
		t.Errorf("the tablet's token still works after its profile got a PIN (%d)", code)
	}
	code, renewed := renew(t, s, tablet.RefreshToken, 1, "r1")
	if code != 200 || renewed.Viewer.ProfileID != "" || renewed.Viewer.Role != "account" {
		t.Errorf("after its profile got a PIN the tablet renewed to %d %+v; want a session with no profile", code, renewed.Viewer)
	}

	// Forgot PIN: from the chooser, the account's password sets a new PIN on the main
	// profile. The tablet stays where it was, and devices on the main profile are asked again.
	forgot := "/v1/profiles/" + mainID + "/pin"
	if code := status(t, s, "PUT", forgot, renewed.AccessToken, ProfilePIN{PIN: "7777", CurrentPassword: "not the password"}); code != 403 {
		t.Errorf("replacing a forgotten PIN with a wrong password answered %d, want 403", code)
	}
	if code := status(t, s, "PUT", forgot, renewed.AccessToken, ProfilePIN{PIN: "7777", CurrentPassword: testPassword}); code != 200 {
		t.Fatalf("replacing a forgotten PIN with the password answered %d, want 200", code)
	}
	if code := status(t, s, "GET", "/v1/me", renewed.AccessToken, nil); code != 200 {
		t.Errorf("the device that replaced the PIN was moved (%d)", code)
	}
	if code := status(t, s, "GET", "/v1/me", main.AccessToken, nil); code != 401 {
		t.Errorf("a device on the main profile was not asked for the new PIN (%d)", code)
	}
	if w := call(t, s, "POST", sel, renewed.AccessToken, ProfileSelect{PIN: "7777"}, &main); w.Code != 200 {
		t.Errorf("the new PIN answered %d, want 200", w.Code)
	}
	// Another account's profile id is simply not there.
	if code := status(t, s, "POST", "/v1/profiles/prf_someoneelses/select", main.AccessToken, ProfileSelect{}); code != 404 {
		t.Errorf("selecting an unknown profile answered %d, want 404", code)
	}
}

// Creating a profile twice with one Idempotency-Key creates one profile; the key with a
// different request is refused.
func TestProfileCreateIsSafeToRepeat(t *testing.T) {
	s := newTestServer(t)
	token := onMain(t, s, setup(t, s)).AccessToken
	create := func(name string) (int, Profile) {
		r := newRequest("POST", "/v1/profiles", token, ProfileCreate{Name: name})
		r.Header.Set("Idempotency-Key", "key-1")
		w := serve(s, r)
		var p Profile
		jsonInto(w, &p)
		return w.Code, p
	}
	_, first := create("Guest")
	code, again := create("Guest")
	if code != 200 || again.ID != first.ID {
		t.Fatalf("the repeat answered %d with profile %q, want the first profile %q", code, again.ID, first.ID)
	}
	if code, _ := create("Someone else"); code != 422 {
		t.Errorf("the key reused for another request answered %d, want 422", code)
	}
	var list Page[Profile]
	call(t, s, "GET", "/v1/profiles", token, nil, &list)
	if len(list.Items) != 2 {
		t.Errorf("%d profiles exist, want 2", len(list.Items))
	}
}

// Forgetting a device ends the sessions on it and takes it off the list; renaming sticks
// through the next sign-in.
func TestDevicesAndSessions(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s))
	tv := signIn(t, s, 1).Session
	var renamed Device
	if w := call(t, s, "PATCH", "/v1/devices/"+tv.DeviceID, owner.AccessToken, DeviceEdit{Name: "Living room TV"}, &renamed); w.Code != 200 || renamed.Name != "Living room TV" {
		t.Fatalf("rename answered %d %+v", w.Code, renamed)
	}
	tvAgain := signIn(t, s, 1).Session
	// The lists say home or away and never give an address.
	var sessions Page[SessionInfo]
	listed := call(t, s, "GET", "/v1/me/sessions", owner.AccessToken, nil, &sessions).Body.String()
	listed += call(t, s, "GET", "/v1/devices", owner.AccessToken, nil, nil).Body.String()
	if strings.Contains(listed, "192.0.2.1") || strings.Count(listed, `"network":"away"`) != 5 {
		t.Errorf("the session and device lists should say away five times and give no address: %s", listed)
	}
	names := ""
	for _, v := range sessions.Items {
		names += v.DeviceName + ";"
	}
	if len(sessions.Items) != 3 || strings.Count(names, "Living room TV;") != 2 {
		t.Fatalf("sessions: %s", names)
	}
	if code := status(t, s, "DELETE", "/v1/devices/"+tv.DeviceID, owner.AccessToken, nil); code != 204 {
		t.Fatalf("forget answered %d", code)
	}
	for _, session := range []Session{tv, tvAgain} {
		if code := status(t, s, "GET", "/v1/me", session.AccessToken, nil); code != 401 {
			t.Errorf("a session on the forgotten device still works (%d)", code)
		}
		if code, _ := renew(t, s, session.RefreshToken, 1, "r"); code != 401 {
			t.Errorf("a session on the forgotten device still renews (%d)", code)
		}
	}
	// "Sign out everywhere else" ends the others and keeps the one asking.
	phone := signIn(t, s, 2).Session
	if code := status(t, s, "DELETE", "/v1/me/sessions", owner.AccessToken, nil); code != 204 {
		t.Fatalf("ending the other sessions answered %d", code)
	}
	if code := status(t, s, "GET", "/v1/me", phone.AccessToken, nil); code != 401 {
		t.Errorf("another session still works after signing out everywhere else (%d)", code)
	}
	if code := status(t, s, "GET", "/v1/me", owner.AccessToken, nil); code != 200 {
		t.Errorf("signing out everywhere else ended the session that asked (%d)", code)
	}
	var devices Page[Device]
	call(t, s, "GET", "/v1/devices", owner.AccessToken, nil, &devices)
	for _, d := range devices.Items {
		if d.ID == tv.DeviceID || d.Current != (d.ID == owner.DeviceID) || d.SignedIn != d.Current {
			t.Errorf("after forgetting the TV and signing the phone out, the device list has %+v", d)
		}
	}
	if len(devices.Items) != 2 {
		t.Errorf("the device list has %d devices, want the owner's and the signed-out phone", len(devices.Items))
	}
}

// A session ended moments before a restart stays ended after it, and a live one carries on
// by renewing: tokens from before a restart are not trusted, because the note of what was
// ended or changed since they were minted is kept in memory.
func TestARevokedSessionStaysRevokedAfterARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.db")
	s := openTestServer(t, path)
	session := setup(t, s).Session
	live := signIn(t, s, 1).Session
	if code := status(t, s, "DELETE", "/v1/auth/session", session.AccessToken, nil); code != 204 {
		t.Fatalf("sign-out answered %d", code)
	}
	closeDB(s.db)
	time.Sleep(2 * time.Millisecond)
	s = openTestServer(t, path)
	if code := status(t, s, "GET", "/v1/me", session.AccessToken, nil); code != 401 {
		t.Fatalf("after a restart a signed-out session's token answered %d, want 401", code)
	}
	if code, _ := renew(t, s, session.RefreshToken, 0, "r"); code != 401 {
		t.Fatalf("after a restart a signed-out session renewed (%d)", code)
	}
	if code := status(t, s, "GET", "/v1/me", live.AccessToken, nil); code != 401 {
		t.Fatalf("a token from before the restart answered %d, want 401 so the app renews", code)
	}
	code, renewed := renew(t, s, live.RefreshToken, 1, "r")
	if code != 200 || status(t, s, "GET", "/v1/me", renewed.AccessToken, nil) != 200 {
		t.Fatalf("a live session could not carry on after a restart (%d)", code)
	}
}
