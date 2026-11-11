package api

import (
	"encoding/base32"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/hosted"
)

// porticoServer is a server set up with local sign-in, with members sam and alex, that the
// owner switched to Portico Accounts through a stand-in for Portico Services. Sam has moved to
// his Portico Account with his old password, as sammy; alex still waits. It returns the
// owner's sign-in and sam's.
func porticoServer(t *testing.T) (*Server, string, string) {
	t.Helper()
	s := newTestServer(t)
	fake := &fakeHosted{t: t}
	s.hosted = fake
	owner := onMain(t, s, setup(t, s)).AccessToken
	for _, name := range []string{"sam", "alex"} {
		if w := call(t, s, "POST", "/v1/admin/members", owner, MemberCreate{Username: name, Password: testPassword}, nil); w.Code != 200 {
			t.Fatalf("adding %s answered %d: %s", name, w.Code, w.Body)
		}
	}
	var preview SignInPreview
	call(t, s, "POST", "/v1/admin/sign-in/preview", owner, SignInPreviewRequest{To: "portico", ReturnURL: "https://den.example/back"}, &preview)
	if preview.ApprovalURL == "" || preview.SignInAgain != 2 {
		t.Fatalf("the preview to Portico Accounts says %+v", preview)
	}
	fake.decision = hosted.Decision{Purpose: hosted.PurposeSwitch, Account: hosted.Account{ID: "pa_1", Username: "justin"}}
	approval := "approved-for-" + s.id
	var switched SignInResponse
	body := SignInSwitchRequest{To: "portico", CurrentPassword: testPassword, Approval: approval, Device: device(1)}
	if w := call(t, s, "POST", "/v1/admin/sign-in/switch", owner, body, &switched); w.Code != 200 {
		t.Fatalf("switching to Portico Accounts answered %d: %s", w.Code, w.Body)
	}
	owner = onMain(t, s, switched).AccessToken

	// A waiting person's old password says what to do next, and with their Portico Account
	// it brings them back; that Portico Account then cannot be used by anyone else.
	w := call(t, s, "POST", "/v1/auth/sign-in", "", SignInRequest{Username: "sam", Password: testPassword, Device: device(5)}, nil)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "now uses Portico Accounts") {
		t.Fatalf("a waiting person's old password answered %d: %s", w.Code, w.Body)
	}
	var check MoveCheck
	if w := call(t, s, "POST", "/v1/auth/move/check", "", MoveCheckRequest{Username: "sam", Password: testPassword}, &check); w.Code != 200 || check.Next != "portico_account" {
		t.Fatalf("checking an old password answered %d: %s", w.Code, w.Body)
	}
	fake.decision = hosted.Decision{Purpose: hosted.PurposeLink, Account: hosted.Account{ID: "pa_2", Username: "sammy"}}
	var moved SignInResponse
	done := MoveCompleteRequest{Username: "sam", Password: testPassword, Approval: approval, Device: device(5)}
	if w := call(t, s, "POST", "/v1/auth/move/complete", "", done, &moved); w.Code != 200 || moved.Account.Username != "sammy" {
		t.Fatalf("moving to a Portico Account answered %d: %s", w.Code, w.Body)
	}
	again := MoveCompleteRequest{Username: "alex", Password: testPassword, Approval: approval, Device: device(6)}
	if w := call(t, s, "POST", "/v1/auth/move/complete", "", again, nil); w.Code != 409 || !strings.Contains(w.Body.String(), "as sammy") {
		t.Fatalf("a Portico Account already here answered %d: %s", w.Code, w.Body)
	}
	if got := memberState(t, s, owner, "alex"); got != "waiting" {
		t.Fatalf("alex is %q", got)
	}
	// Nobody on a Portico Accounts server has a password here.
	if w := call(t, s, "POST", "/v1/admin/members", owner, MemberCreate{Username: "kim", Password: testPassword}, nil); w.Code != 409 {
		t.Fatalf("adding a password account to a Portico Accounts server answered %d: %s", w.Code, w.Body)
	}
	return s, owner, moved.Session.AccessToken
}

func memberState(t *testing.T, s *Server, token, username string) string {
	t.Helper()
	var page Page[Member]
	call(t, s, "GET", "/v1/admin/members", token, nil, &page)
	for _, m := range page.Items {
		if m.Username == username {
			return m.State
		}
	}
	return "gone"
}

// A server that uses Portico Accounts switches to local sign-in without Portico Services:
// the preview counts what changes, the switch signs everyone out at once and leaves the others
// waiting, a waiting person comes back with the owner's code and a new username, the code
// works once, and Finish now turns off whoever is left as "Didn't move".
func TestSwitchToLocalSignIn(t *testing.T) {
	s, owner, sam := porticoServer(t)
	s.hosted = hosted.NotConnected{}

	var preview SignInPreview
	if w := call(t, s, "POST", "/v1/admin/sign-in/preview", owner, SignInPreviewRequest{To: "local"}, &preview); w.Code != 200 {
		t.Fatalf("the preview answered %d: %s", w.Code, w.Body)
	}
	if preview.SignInAgain != 1 || preview.SignedOut < 2 || !preview.PorticoAddressRemoved || preview.Removed != 0 {
		t.Fatalf("the preview says %+v", preview)
	}
	var switched SignInResponse
	body := SignInSwitchRequest{To: "local", Username: "justin", Password: testPassword, Device: device(1)}
	if w := call(t, s, "POST", "/v1/admin/sign-in/switch", owner, body, &switched); w.Code != 200 {
		t.Fatalf("the switch answered %d: %s", w.Code, w.Body)
	}
	for who, token := range map[string]string{"the owner's old sign-in": owner, "sam's sign-in": sam} {
		if code := status(t, s, "GET", "/v1/me", token, nil); code != 401 {
			t.Errorf("%s answered %d after the switch, want 401", who, code)
		}
	}
	owner = onMain(t, s, switched).AccessToken
	var system System
	call(t, s, "GET", "/v1/system", "", nil, &system)
	if system.SignIn != "local" || system.Move != "to_local" {
		t.Fatalf("after the switch the server says %+v", system)
	}
	if code := status(t, s, "POST", "/v1/auth/sign-in", "", SignInRequest{Username: "alex", Password: testPassword, Device: device(6)}); code != 401 {
		t.Fatalf("a waiting person's password sign-in answered %d", code)
	}

	var state SignInState
	call(t, s, "GET", "/v1/admin/sign-in", owner, nil, &state)
	if state.Kind != "local" || state.Move == nil || len(state.Waiting) != 2 {
		t.Fatalf("the Sign-in page says %+v", state)
	}
	var code MoveCodeOut
	samID := state.Waiting[1].ID // by username: alex, sammy
	if w := call(t, s, "POST", "/v1/admin/members/"+samID+"/move-code", owner, nil, &code); w.Code != 200 || code.Code == "" {
		t.Fatalf("a move code answered %d: %s", w.Code, w.Body)
	}
	if w := call(t, s, "POST", "/v1/auth/move/check", "", MoveCheckRequest{Code: "AAAA-AAAA-AAAA"}, nil); w.Code != 401 {
		t.Fatalf("a wrong move code answered %d", w.Code)
	}
	var check MoveCheck
	if w := call(t, s, "POST", "/v1/auth/move/check", "", MoveCheckRequest{Code: code.Code}, &check); w.Code != 200 || check.Username != "sammy" || check.Next != "choose_password" {
		t.Fatalf("checking the move code answered %d: %s", w.Code, w.Body)
	}
	done := MoveCompleteRequest{Code: code.Code, Username: "Samuel", Password: "a new password", Device: device(7)}
	var moved SignInResponse
	if w := call(t, s, "POST", "/v1/auth/move/complete", "", done, &moved); w.Code != 200 || moved.Account.Username != "samuel" {
		t.Fatalf("finishing the move answered %d: %s", w.Code, w.Body)
	}
	if w := call(t, s, "POST", "/v1/auth/move/complete", "", done, nil); w.Code != 401 {
		t.Fatalf("a used move code answered %d", w.Code)
	}
	signInAs(t, s, "samuel", "a new password", 8)

	var finished FinishedMove
	if w := call(t, s, "POST", "/v1/admin/sign-in/finish", owner, nil, &finished); w.Code != 200 || finished.NotMoved != 1 {
		t.Fatalf("Finish now answered %d: %s", w.Code, w.Body)
	}
	if got := memberState(t, s, owner, "alex"); got != "not_moved" {
		t.Fatalf("alex is %q after Finish now", got)
	}
	var after System
	call(t, s, "GET", "/v1/system", "", nil, &after)
	if after.Move != "" {
		t.Fatalf("the server still says a move is under way: %+v", after)
	}
}

// Start over keeps the owner and removes everyone else, once the server's name is typed.
func TestStartOver(t *testing.T) {
	s, owner, _ := porticoServer(t)
	body := SignInSwitchRequest{To: "local", StartOver: true, ServerName: "Somewhere else", Username: "justin", Password: testPassword, Device: device(1)}
	if code := status(t, s, "POST", "/v1/admin/sign-in/switch", owner, body); code != 400 {
		t.Fatalf("Start over with the wrong name answered %d", code)
	}
	body.ServerName = "portico"
	var switched SignInResponse
	if w := call(t, s, "POST", "/v1/admin/sign-in/switch", owner, body, &switched); w.Code != 200 {
		t.Fatalf("Start over answered %d: %s", w.Code, w.Body)
	}
	owner = onMain(t, s, switched).AccessToken
	var page Page[Member]
	call(t, s, "GET", "/v1/admin/members", owner, nil, &page)
	if len(page.Items) != 1 || page.Items[0].Username != "justin" {
		t.Fatalf("after Start over the people are %+v", page.Items)
	}
	var system System
	if call(t, s, "GET", "/v1/system", "", nil, &system); system.Move != "" || system.SignIn != "local" {
		t.Fatalf("after Start over the server says %+v", system)
	}
}

// Change owner on a local server needs the owner's password; the new owner has the owner's
// power at their next sign-in and the previous owner keeps none of it.
func TestChangeOwner(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	var sam Member
	call(t, s, "POST", "/v1/admin/members", owner, MemberCreate{Username: "sam", Password: testPassword}, &sam)
	wrong := OwnerChangeRequest{Password: "not it at all", AccountID: sam.ID, Previous: "admin"}
	if code := status(t, s, "POST", "/v1/admin/owner", owner, wrong); code != 403 {
		t.Fatalf("a wrong password answered %d", code)
	}
	right := OwnerChangeRequest{Password: testPassword, AccountID: sam.ID, Previous: "admin"}
	if w := call(t, s, "POST", "/v1/admin/owner", owner, right, nil); w.Code != 204 {
		t.Fatalf("changing owner answered %d: %s", w.Code, w.Body)
	}
	if code := status(t, s, "GET", "/v1/admin/sign-in", owner, nil); code != 401 {
		t.Fatalf("the previous owner's sign-in answered %d on an owner page, want 401 (renew)", code)
	}
	again := onMain(t, s, signIn(t, s, 2)).AccessToken
	if code := status(t, s, "GET", "/v1/admin/sign-in", again, nil); code != 403 {
		t.Fatalf("the previous owner signed in again answered %d on an owner page, want 403", code)
	}
	newOwner := signInAs(t, s, "sam", testPassword, 3).AccessToken
	if code := status(t, s, "GET", "/v1/admin/sign-in", newOwner, nil); code != 200 {
		t.Fatalf("the new owner answered %d on an owner page", code)
	}
}

// A local server that starts over stays local and has nothing queued for Portico Services;
// one that leaves Portico Accounts has its one message queued.
func TestOnlyALeavingPorticoServerQueuesItsMessage(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	body := SignInSwitchRequest{To: "local", StartOver: true, ServerName: "Portico", CurrentPassword: testPassword, Username: "justin",
		Password: testPassword, Device: device(1)}
	if w := call(t, s, "POST", "/v1/admin/sign-in/switch", owner, body, nil); w.Code != 200 {
		t.Fatalf("Start over answered %d: %s", w.Code, w.Body)
	}
	if _, queued, _ := s.db.Setting(t.Context(), db.LeftMessageKey); queued {
		t.Fatal("a local server queued a message for Portico Services")
	}
	p, porticoOwner, _ := porticoServer(t)
	if w := call(t, p, "POST", "/v1/admin/sign-in/switch", porticoOwner, body, nil); w.Code != 200 {
		t.Fatalf("Start over answered %d: %s", w.Code, w.Body)
	}
	if _, queued, _ := p.db.Setting(t.Context(), db.LeftMessageKey); !queued {
		t.Fatal("a server leaving Portico Accounts did not queue its one message")
	}
}

// A switch or Start over on a server with local sign-in replaces the owner's password and can
// remove everyone else, so the owner's current password must be typed fresh: the owner's
// sign-in token and the public server name are not enough.
func TestSwitchNeedsTheOwnersPassword(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	call(t, s, "POST", "/v1/admin/members", owner, MemberCreate{Username: "sam", Password: testPassword}, nil)
	body := SignInSwitchRequest{To: "local", StartOver: true, ServerName: "Portico", Username: "mallory", Password: "a new password", Device: device(1)}
	for _, current := range []string{"", "not the password"} {
		body.CurrentPassword = current
		if code := status(t, s, "POST", "/v1/admin/sign-in/switch", owner, body); code != 403 {
			t.Fatalf("Start over with the current password %q answered %d, want 403", current, code)
		}
	}
	if got := memberState(t, s, owner, "sam"); got != "active" {
		t.Fatalf("a refused Start over still removed sam: %q", got)
	}
	body.CurrentPassword = testPassword
	if code := status(t, s, "POST", "/v1/admin/sign-in/switch", owner, body); code != 200 {
		t.Fatalf("Start over with the owner's password answered %d", code)
	}
}

// An account the owner turned off stays off through a switch: it does not wait, gets no move
// code, and neither a code nor its old password brings it back.
func TestTurnedOffStaysOffThroughASwitch(t *testing.T) {
	s := newTestServer(t)
	fake := &fakeHosted{t: t}
	s.hosted = fake
	owner := onMain(t, s, setup(t, s)).AccessToken
	var alex Member
	call(t, s, "POST", "/v1/admin/members", owner, MemberCreate{Username: "sam", Password: testPassword}, nil)
	call(t, s, "POST", "/v1/admin/members", owner, MemberCreate{Username: "alex", Password: testPassword}, &alex)
	off := true
	if w := call(t, s, "PATCH", "/v1/admin/members/"+alex.ID, owner, MemberEdit{Disabled: &off}, nil); w.Code != 200 {
		t.Fatalf("turning alex off answered %d: %s", w.Code, w.Body)
	}
	fake.decision = hosted.Decision{Purpose: hosted.PurposeSwitch, Account: hosted.Account{ID: "pa_1", Username: "justin"}}
	approval := "approved-for-" + s.id
	var switched SignInResponse
	body := SignInSwitchRequest{To: "portico", CurrentPassword: testPassword, Approval: approval, Device: device(1)}
	if w := call(t, s, "POST", "/v1/admin/sign-in/switch", owner, body, &switched); w.Code != 200 {
		t.Fatalf("the switch answered %d: %s", w.Code, w.Body)
	}
	owner = onMain(t, s, switched).AccessToken
	var codes Page[MoveCodeOut]
	call(t, s, "POST", "/v1/admin/sign-in/move-codes", owner, nil, &codes)
	if len(codes.Items) != 1 || codes.Items[0].Username != "sam" {
		t.Fatalf("codes were made for %+v, want sam alone", codes.Items)
	}
	if code := status(t, s, "POST", "/v1/admin/members/"+alex.ID+"/move-code", owner, nil); code != 409 {
		t.Fatalf("a move code for a turned-off account answered %d, want 409", code)
	}
	fake.decision = hosted.Decision{Purpose: hosted.PurposeLink, Account: hosted.Account{ID: "pa_9", Username: "alex"}}
	if code := status(t, s, "POST", "/v1/auth/move/check", "", MoveCheckRequest{Username: "alex", Password: testPassword}); code != 401 {
		t.Fatalf("a turned-off account's old password answered %d on the check, want 401", code)
	}
	done := MoveCompleteRequest{Username: "alex", Password: testPassword, Approval: approval, Device: device(2)}
	if code := status(t, s, "POST", "/v1/auth/move/complete", "", done); code != 401 {
		t.Fatalf("a turned-off account's old password moved it (%d)", code)
	}
	// Turned on after the switch, the account is waiting like everyone else and can move.
	on := false
	if w := call(t, s, "PATCH", "/v1/admin/members/"+alex.ID, owner, MemberEdit{Disabled: &on}, nil); w.Code != 200 {
		t.Fatalf("turning alex on answered %d: %s", w.Code, w.Body)
	}
	if got := memberState(t, s, owner, "alex"); got != "waiting" {
		t.Fatalf("alex turned on after the switch is %q, want waiting", got)
	}
	if code := status(t, s, "POST", "/v1/admin/members/"+alex.ID+"/move-code", owner, nil); code != 200 {
		t.Fatalf("a move code for alex turned on answered %d", code)
	}
	if code := status(t, s, "POST", "/v1/auth/move/complete", "", done); code != 200 {
		t.Fatalf("alex turned on could not move with the old password (%d)", code)
	}
}

// An account with two-step sign-in never moves on its old password alone: it needs the owner's
// move code. Wrong old passwords count against the account, as at sign-in.
func TestOldPasswordMoveIsAsStrictAsSignIn(t *testing.T) {
	s := newTestServer(t)
	fake := &fakeHosted{t: t}
	s.hosted = fake
	owner := onMain(t, s, setup(t, s)).AccessToken
	call(t, s, "POST", "/v1/admin/members", owner, MemberCreate{Username: "sam", Password: testPassword}, nil)
	sam := signInAs(t, s, "sam", testPassword, 3).AccessToken
	var enrol TwoFactorSetup
	call(t, s, "POST", "/v1/me/two-factor", sam, TwoFactorEnrol{CurrentPassword: testPassword}, &enrol)
	secret, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(enrol.Secret)
	if w := call(t, s, "POST", "/v1/me/two-factor/confirm", sam, TwoFactorVerify{Code: auth.TOTPCode(secret, auth.TOTPStep(time.Now()))}, nil); w.Code != 200 {
		t.Fatalf("confirming two-step sign-in answered %d: %s", w.Code, w.Body)
	}
	fake.decision = hosted.Decision{Purpose: hosted.PurposeSwitch, Account: hosted.Account{ID: "pa_1", Username: "justin"}}
	body := SignInSwitchRequest{To: "portico", CurrentPassword: testPassword, Approval: "approved-for-" + s.id, Device: device(1)}
	if w := call(t, s, "POST", "/v1/admin/sign-in/switch", owner, body, nil); w.Code != 200 {
		t.Fatalf("the switch answered %d: %s", w.Code, w.Body)
	}
	if w := call(t, s, "POST", "/v1/auth/move/check", "", MoveCheckRequest{Username: "sam", Password: testPassword}, nil); w.Code != 409 ||
		!strings.Contains(w.Body.String(), "move_code_required") {
		t.Fatalf("a two-step account's old password answered %d: %s", w.Code, w.Body)
	}
	// Guesses from many networks at one account slow every new network down.
	for i := range 6 {
		r := newRequest("POST", "/v1/auth/move/check", "", MoveCheckRequest{Username: "sam", Password: "wrong guess"})
		r.RemoteAddr = fmt.Sprintf("203.0.113.%d:4000", i+1)
		serve(s, r)
	}
	// A new network's first wrong guess starts from the account's count, so its next waits.
	codes := []int{}
	for range 2 {
		r := newRequest("POST", "/v1/auth/move/check", "", MoveCheckRequest{Username: "sam", Password: "wrong guess"})
		r.RemoteAddr = "198.51.100.77:4000"
		codes = append(codes, serve(s, r).Code)
	}
	if codes[0] != 401 || codes[1] != 429 {
		t.Fatalf("a new network guessing at an account guessed at from many answered %v, want [401 429]", codes)
	}
}

// Moving to Portico Accounts, each person's pictures stored here go when that person moves:
// the owner's with the switch, everyone else's with their own move, and not before.
func TestPicturesGoWhenEachPersonMoves(t *testing.T) {
	s := newTestServer(t)
	fake := &fakeHosted{t: t}
	s.hosted = fake
	owner := onMain(t, s, setup(t, s)).AccessToken
	call(t, s, "POST", "/v1/admin/members", owner, MemberCreate{Username: "sam", Password: testPassword}, nil)
	ctx := t.Context()
	pictures := map[string]string{}
	for _, name := range []string{"justin", "sam"} {
		creds, _ := s.db.CredentialsByUsername(ctx, name)
		profiles, _ := s.db.Profiles(ctx, creds.UserID)
		if _, err := s.db.SetProfilePicture(ctx, creds.UserID, profiles[0].PublicID, "picture-of-"+name, 160, 160); err != nil {
			t.Fatal(err)
		}
		pictures[name] = profiles[0].PublicID
	}
	has := func(name string) bool {
		creds, _ := s.db.CredentialsByUsername(ctx, name)
		_, err := s.db.ProfilePictureName(ctx, creds.UserID, pictures[name])
		return err == nil
	}
	fake.decision = hosted.Decision{Purpose: hosted.PurposeSwitch, Account: hosted.Account{ID: "pa_1", Username: "justin"}}
	approval := "approved-for-" + s.id
	body := SignInSwitchRequest{To: "portico", CurrentPassword: testPassword, Approval: approval, Device: device(1)}
	if w := call(t, s, "POST", "/v1/admin/sign-in/switch", owner, body, nil); w.Code != 200 {
		t.Fatalf("the switch answered %d: %s", w.Code, w.Body)
	}
	if has("justin") || !has("sam") {
		t.Fatalf("after the switch: the owner's picture kept %v, sam's kept %v", has("justin"), has("sam"))
	}
	fake.decision = hosted.Decision{Purpose: hosted.PurposeLink, Account: hosted.Account{ID: "pa_2", Username: "sam"}}
	done := MoveCompleteRequest{Username: "sam", Password: testPassword, Approval: approval, Device: device(2)}
	if w := call(t, s, "POST", "/v1/auth/move/complete", "", done, nil); w.Code != 200 {
		t.Fatalf("sam's move answered %d: %s", w.Code, w.Body)
	}
	if has("sam") {
		t.Fatal("sam's picture is still stored here after he moved")
	}
}

// On a server whose owner is linked to a Portico Account, Start over (or a switch) to Portico
// Accounts takes only that account's approval: another account's is refused and nothing
// changes; the owning account's is accepted.
func TestOnlyTheLinkedAccountMayStartOver(t *testing.T) {
	s, owner, _ := porticoServer(t)
	fake := &fakeHosted{t: t}
	s.hosted = fake
	approval := "approved-for-" + s.id
	body := SignInSwitchRequest{To: "portico", StartOver: true, ServerName: "Portico", Approval: approval, Device: device(3)}
	fake.decision = hosted.Decision{Purpose: hosted.PurposeSwitch, Account: hosted.Account{ID: "pa_9", Username: "mallory"}}
	if w := call(t, s, "POST", "/v1/admin/sign-in/switch", owner, body, nil); w.Code != 403 || !strings.Contains(w.Body.String(), "not_the_linked_account") {
		t.Fatalf("another account's approval answered %d: %s", w.Code, w.Body)
	}
	if got := memberState(t, s, owner, "alex"); got != "waiting" {
		t.Fatalf("a refused Start over still changed the server: alex is %q", got)
	}
	fake.decision = hosted.Decision{Purpose: hosted.PurposeSwitch, Account: hosted.Account{ID: "pa_1", Username: "justin"}}
	if w := call(t, s, "POST", "/v1/admin/sign-in/switch", owner, body, nil); w.Code != 200 {
		t.Fatalf("the owning account's approval answered %d: %s", w.Code, w.Body)
	}
}
