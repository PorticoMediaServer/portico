package api

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/hosted"
)

// porticoServer sets up a server with Portico Accounts through a stand-in for Portico Services
// and returns the owner's sign-in on the main profile, with two members, sam and alex.
func porticoServer(t *testing.T) (*Server, string) {
	t.Helper()
	s := newTestServer(t)
	s.hosted = &fakeHosted{t: t, decision: hosted.Decision{Purpose: hosted.PurposeSetup, ServerName: "Cabin",
		Account: hosted.Account{ID: "pa_1", Username: "justin"}}}
	var out SignInResponse
	if w := call(t, s, "POST", "/v1/setup/portico/finish", "", SetupPorticoFinish{Approval: "approved-for-" + s.id, Device: device(0)}, &out); w.Code != 200 {
		t.Fatalf("Portico setup answered %d: %s", w.Code, w.Body)
	}
	owner := onMain(t, s, out).AccessToken
	for _, name := range []string{"sam", "alex"} {
		if w := call(t, s, "POST", "/v1/admin/members", owner, MemberCreate{Username: name, Password: testPassword}, nil); w.Code != 201 && w.Code != 200 {
			t.Fatalf("adding %s answered %d: %s", name, w.Code, w.Body)
		}
	}
	return s, owner
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
	s, owner := porticoServer(t)
	s.hosted = hosted.NotConnected{}
	sam := signInAs(t, s, "sam", testPassword, 5).AccessToken

	var preview SignInPreview
	if w := call(t, s, "POST", "/v1/admin/sign-in/preview", owner, SignInPreviewRequest{To: "local"}, &preview); w.Code != 200 {
		t.Fatalf("the preview answered %d: %s", w.Code, w.Body)
	}
	if preview.SignInAgain != 2 || preview.SignedOut < 2 || !preview.PorticoAddressRemoved || preview.Removed != 0 {
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
	if code := status(t, s, "POST", "/v1/auth/sign-in", "", SignInRequest{Username: "sam", Password: testPassword, Device: device(6)}); code != 401 {
		t.Fatalf("a waiting person's password sign-in answered %d", code)
	}

	var state SignInState
	call(t, s, "GET", "/v1/admin/sign-in", owner, nil, &state)
	if state.Kind != "local" || state.Move == nil || len(state.Waiting) != 2 {
		t.Fatalf("the Sign-in page says %+v", state)
	}
	var code MoveCodeOut
	samID := state.Waiting[1].ID // by username: alex, sam
	if w := call(t, s, "POST", "/v1/admin/members/"+samID+"/move-code", owner, nil, &code); w.Code != 200 || code.Code == "" {
		t.Fatalf("a move code answered %d: %s", w.Code, w.Body)
	}
	if w := call(t, s, "POST", "/v1/auth/move/check", "", MoveCheckRequest{Code: "AAAA-AAAA-AAAA"}, nil); w.Code != 401 {
		t.Fatalf("a wrong move code answered %d", w.Code)
	}
	var check MoveCheck
	if w := call(t, s, "POST", "/v1/auth/move/check", "", MoveCheckRequest{Code: code.Code}, &check); w.Code != 200 || check.Username != "sam" || check.Next != "choose_password" {
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
	s, owner := porticoServer(t)
	body := SignInSwitchRequest{To: "local", StartOver: true, ServerName: "Somewhere else", Username: "justin", Password: testPassword, Device: device(1)}
	if code := status(t, s, "POST", "/v1/admin/sign-in/switch", owner, body); code != 400 {
		t.Fatalf("Start over with the wrong name answered %d", code)
	}
	body.ServerName = "cabin"
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
	body := SignInSwitchRequest{To: "local", StartOver: true, ServerName: "Portico", Username: "justin", Password: testPassword, Device: device(1)}
	if w := call(t, s, "POST", "/v1/admin/sign-in/switch", owner, body, nil); w.Code != 200 {
		t.Fatalf("Start over answered %d: %s", w.Code, w.Body)
	}
	if _, queued, _ := s.db.Setting(t.Context(), db.LeftMessageKey); queued {
		t.Fatal("a local server queued a message for Portico Services")
	}
	p, porticoOwner := porticoServer(t)
	body.ServerName = "Cabin"
	if w := call(t, p, "POST", "/v1/admin/sign-in/switch", porticoOwner, body, nil); w.Code != 200 {
		t.Fatalf("Start over answered %d: %s", w.Code, w.Body)
	}
	if _, queued, _ := p.db.Setting(t.Context(), db.LeftMessageKey); !queued {
		t.Fatal("a server leaving Portico Accounts did not queue its one message")
	}
}
