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
	if w := call(t, s, "POST", "/v1/setup/owner", "", SetupOwnerRequest{Username: "Justin", Password: testPassword, Device: device(0)}, &out); w.Code != 200 {
		t.Fatalf("setup answered %d: %s", w.Code, w.Body)
	}
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
	first := setup(t, s)
	if first.Account.Role != "owner" || len(first.Profiles) != 1 || !first.Profiles[0].IsPrimary || first.Session.Viewer.Role != "account" {
		t.Fatalf("unexpected setup answer: %+v", first)
	}
	if code := status(t, s, "POST", "/v1/setup/owner", "", SetupOwnerRequest{Username: "mallory", Password: testPassword, Device: device(9)}); code != 409 {
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
	var me Me
	if w := call(t, s, "GET", "/v1/me", chosen.AccessToken, nil, &me); w.Code != 200 || me.Profile == nil || me.Role != "owner" || me.Account.Username != "Justin" {
		t.Fatalf("me answered %d %+v", w.Code, me)
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
				body := SetupOwnerRequest{Username: fmt.Sprintf("person%d", i), Password: testPassword, Device: device(i)}
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
	owner := setup(t, s).Session
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

// Changing the password signs the person out everywhere at once, and only the new password works.
func TestPasswordChangeEndsEverySession(t *testing.T) {
	s := newTestServer(t)
	one := setup(t, s).Session
	two := signIn(t, s, 1).Session
	if code := status(t, s, "POST", "/v1/auth/password", one.AccessToken, PasswordRequest{CurrentPassword: "not the password", NewPassword: "a new password"}); code != 403 {
		t.Fatalf("a wrong current password answered %d, want 403", code)
	}
	if status(t, s, "GET", "/v1/me", two.AccessToken, nil) != 200 {
		t.Fatal("a refused password change ended a session")
	}
	if code := status(t, s, "POST", "/v1/auth/password", one.AccessToken, PasswordRequest{CurrentPassword: testPassword, NewPassword: "a new password"}); code != 204 {
		t.Fatalf("password change answered %d", code)
	}
	for n, session := range []Session{one, two} {
		if code := status(t, s, "GET", "/v1/me", session.AccessToken, nil); code != 401 {
			t.Errorf("session %d: the access token still works after the password changed (%d)", n, code)
		}
		if code, _ := renew(t, s, session.RefreshToken, n, "r"); code != 401 {
			t.Errorf("session %d: a renewal still works after the password changed (%d)", n, code)
		}
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

// A username nobody has is answered exactly like a wrong password for a real one, guess
// after guess, including when the wait starts. The wait survives a restart, is refused even
// with the right password, never locks the account for good, and ends by itself.
func TestGuessingIsSlowedTheSameForKnownAndUnknownNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.db")
	s := openTestServer(t, path)
	setup(t, s)
	now := time.Now()
	s.now = func() time.Time { return now }

	guess := func(s *Server, username, password string) (int, string) {
		w := call(t, s, "POST", "/v1/auth/sign-in", "", SignInRequest{Username: username, Password: password, Device: device(3)}, nil)
		var body errorBody
		jsonInto(w, &body)
		body.Error.RequestID = ""
		return w.Code, fmt.Sprintf("%+v retry-after=%s", body.Error, w.Header().Get("Retry-After"))
	}
	sawWait := false
	for i := range 8 {
		knownCode, known := guess(s, "justin", "wrong password")
		unknownCode, unknown := guess(s, "nobody-here", "wrong password")
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
	if code, _ := guess(s, "justin", "wrong password"); code != 401 {
		t.Fatalf("the guess after waiting answered %d, want 401", code)
	}
	closeDB(s.db)
	s = openTestServer(t, path)
	s.now = func() time.Time { return now }
	if code, _ := guess(s, "justin", testPassword); code != 429 {
		t.Fatalf("after a restart the right password answered %d inside the wait, want 429", code)
	}
	now = now.Add(2 * time.Minute)
	if code, _ := guess(s, "justin", testPassword); code != 200 {
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

// Profiles: at most eight, only the account's manager may change them, a PIN guards
// selection and slows guessing, and changing a PIN sends other devices back to the chooser.
func TestProfilesPINsAndWhoMayManage(t *testing.T) {
	s := newTestServer(t)
	now := time.Now()
	s.now = func() time.Time { return now }
	first := setup(t, s)
	account := first.Session.AccessToken
	mainID := first.Profiles[0].ID

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

	// A second device goes onto the kid's profile. From there the account cannot be managed.
	tablet := signIn(t, s, 1).Session
	var onKid Session
	call(t, s, "POST", "/v1/profiles/"+kid.ID+"/select", tablet.AccessToken, ProfileSelect{}, &onKid)
	if onKid.Viewer.Role != "member" {
		t.Fatalf("the owner's second profile has role %q, want member", onKid.Viewer.Role)
	}
	for _, c := range []struct{ method, path string }{
		{"POST", "/v1/profiles"}, {"PATCH", "/v1/profiles/" + mainID}, {"DELETE", "/v1/profiles/" + kid.ID},
		{"GET", "/v1/me/sessions"}, {"GET", "/v1/devices"}, {"DELETE", "/v1/devices/" + tablet.DeviceID},
	} {
		var body any
		if c.method == "POST" || c.method == "PATCH" {
			body = map[string]string{"name": "Mine now"}
		}
		if code := status(t, s, c.method, c.path, onKid.AccessToken, body); code != 403 {
			t.Errorf("%s %s from a child profile answered %d, want 403", c.method, c.path, code)
		}
	}

	// The parent puts a PIN on the main profile. A fresh sign-in cannot manage until it passes the PIN.
	pin := "4821"
	if code := status(t, s, "PUT", "/v1/profiles/"+mainID+"/pin", account, ProfilePIN{PIN: pin}); code != 200 {
		t.Fatalf("setting a PIN answered %d", code)
	}
	fresh := signIn(t, s, 2).Session
	if code := status(t, s, "POST", "/v1/profiles", fresh.AccessToken, ProfileCreate{Name: "Sneaky"}); code != 403 {
		t.Errorf("a sign-in that has not passed the main profile's PIN managed the account (%d)", code)
	}
	// Wrong PINs are refused and then slowed; inside the wait even the right PIN is refused.
	sel := "/v1/profiles/" + mainID + "/select"
	if code := status(t, s, "POST", sel, fresh.AccessToken, ProfileSelect{PIN: "0000"}); code != 403 {
		t.Fatalf("a wrong PIN answered %d, want 403", code)
	}
	if code := status(t, s, "POST", sel, fresh.AccessToken, ProfileSelect{PIN: pin}); code != 429 {
		t.Fatalf("the right PIN inside the wait answered %d, want 429", code)
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
	// A forgotten PIN on the main profile is removed with the account's password, from the chooser.
	stuck := signIn(t, s, 3).Session
	reset := "/v1/profiles/" + mainID + "/pin"
	if code := status(t, s, "DELETE", reset, stuck.AccessToken, nil); code != 403 {
		t.Errorf("removing the main profile's PIN from the chooser without the password answered %d, want 403", code)
	}
	if code := status(t, s, "DELETE", reset, stuck.AccessToken, ProfilePINRemove{CurrentPassword: "not the password"}); code != 403 {
		t.Errorf("a PIN reset with a wrong password answered %d, want 403", code)
	}
	if code := status(t, s, "DELETE", reset, stuck.AccessToken, ProfilePINRemove{CurrentPassword: testPassword}); code != 204 {
		t.Errorf("a PIN reset with the password answered %d, want 204", code)
	}
	now = now.Add(time.Second)
	_, stuck = renew(t, s, stuck.RefreshToken, 3, "r1")
	if w := call(t, s, "POST", "/v1/profiles/"+mainID+"/select", stuck.AccessToken, ProfileSelect{}, &main); w.Code != 200 {
		t.Errorf("after the PIN reset the main profile still asks for a PIN (%d)", w.Code)
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
	token := setup(t, s).Session.AccessToken
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
	owner := setup(t, s).Session
	tv := signIn(t, s, 1).Session
	var renamed Device
	if w := call(t, s, "PATCH", "/v1/devices/"+tv.DeviceID, owner.AccessToken, DeviceEdit{Name: "Living room TV"}, &renamed); w.Code != 200 || renamed.Name != "Living room TV" {
		t.Fatalf("rename answered %d %+v", w.Code, renamed)
	}
	tvAgain := signIn(t, s, 1).Session
	var sessions Page[SessionInfo]
	call(t, s, "GET", "/v1/me/sessions", owner.AccessToken, nil, &sessions)
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
