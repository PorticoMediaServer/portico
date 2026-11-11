package api

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/hosted"
)

// fakeHosted stands in for Portico Services. With fail set, any question fails the test: a
// server that uses local sign-in must never ask Portico Services anything.
type fakeHosted struct {
	t        *testing.T
	fail     bool
	decision hosted.Decision
	started  []hosted.ApprovalRequest
}

func (f *fakeHosted) StartApproval(_ context.Context, req hosted.ApprovalRequest) (hosted.Approval, error) {
	if f.fail {
		f.t.Errorf("a local server asked Portico Services to start an approval: %+v", req)
	}
	f.started = append(f.started, req)
	return hosted.Approval{URL: "https://approve.example/a1", ExpiresAt: time.Now().Add(10 * time.Minute)}, nil
}

func (f *fakeHosted) FinishApproval(_ context.Context, serverID, returned string) (hosted.Decision, error) {
	if f.fail {
		f.t.Errorf("a local server asked Portico Services to check an approval")
	}
	if returned != "approved-for-"+serverID {
		return hosted.Decision{}, hosted.ErrDeclined
	}
	return f.decision, nil
}

// The setup code is asked on every network, the server's own machine included; it survives a
// restart until setup finishes; wrong codes slow that network down without locking setup; and
// the server's name chosen in setup is what every app is told.
func TestSetupCodeAndServerName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.db")
	s := openTestServer(t, path)
	code := s.setupCode
	closeDB(s.db)
	s = openTestServer(t, path)
	if s.setupCode != code || len(auth.CodeKey(code)) != 16 {
		t.Fatalf("the setup code is %q, then %q after a restart; want the same 16 characters", code, s.setupCode)
	}

	local := func(body any) int {
		r := newRequest("POST", "/v1/setup/owner", "", body)
		r.RemoteAddr = "127.0.0.1:40000"
		return serve(s, r).Code
	}
	wrong := SetupOwnerRequest{SetupCode: "AAAA-AAAA", Username: "mallory", Password: testPassword, Device: device(9)}
	for i := range 5 {
		if got := local(wrong); got != 403 {
			t.Fatalf("wrong code %d from this machine answered %d, want 403", i+1, got)
		}
	}
	if got := local(wrong); got != 429 {
		t.Fatalf("a sixth wrong code in a row answered %d, want 429", got)
	}
	// Behind a proxy everyone is one source: the right code gets through the wait strangers made.
	right := SetupOwnerRequest{SetupCode: code, ServerName: "  Den  ", Username: "Justin", Password: testPassword, Device: device(0)}
	if got := local(right); got != 200 {
		t.Fatalf("the right code during the wait answered %d, want 200", got)
	}
	var system System
	r := newRequest("GET", "/v1/system", "", nil)
	r.RemoteAddr = "127.0.0.1:40001"
	if w := serve(s, r); jsonInto(w, &system) != nil || system.Name != "Den" || system.SignIn != "local" || !system.AtHome || system.SetupRequired {
		t.Fatalf("after setup the server says %+v", system)
	}
	if call(t, s, "GET", "/v1/system", "", nil, &system); system.AtHome {
		t.Fatal("a caller from outside the home networks is told it is at home")
	}
	closeDB(s.db)
	if s = openTestServer(t, path); s.setupCode != "" {
		t.Fatal("a server with an owner made a setup code at start")
	}
}

// Setting up with Portico Accounts saves nothing until the approval comes back; then the
// approving account is the owner, with no password on this server, and the server says it
// uses Portico Accounts. An approval made for another server changes nothing.
func TestSetupWithPorticoAccounts(t *testing.T) {
	s := newTestServer(t)
	fake := &fakeHosted{t: t, decision: hosted.Decision{Purpose: hosted.PurposeSetup, ServerName: "Cabin",
		Account: hosted.Account{ID: "pa_1", Username: "justin", DisplayName: "Justin"}}}
	s.hosted = fake

	var start SetupPorticoApproval
	body := SetupPorticoRequest{SetupCode: s.setupCode, ServerName: "Cabin", ReturnURL: "https://cabin.example/setup"}
	if w := call(t, s, "POST", "/v1/setup/portico", "", body, &start); w.Code != 200 || start.ApprovalURL == "" {
		t.Fatalf("starting a Portico setup answered %d: %s", w.Code, w.Body)
	}
	if len(fake.started) != 1 || fake.started[0].ServerID != s.id || fake.started[0].ServerName != "Cabin" {
		t.Fatalf("Portico Services was asked %+v", fake.started)
	}
	if has, _ := s.db.HasUsers(t.Context()); has {
		t.Fatal("starting an approval made an account")
	}
	bad := SetupPorticoFinish{Approval: "approved-for-srv_someone_else", Device: device(0)}
	if w := call(t, s, "POST", "/v1/setup/portico/finish", "", bad, nil); w.Code != 409 {
		t.Fatalf("an approval for another server answered %d: %s", w.Code, w.Body)
	}
	var out SignInResponse
	good := SetupPorticoFinish{Approval: "approved-for-" + s.id, Device: device(0)}
	if w := call(t, s, "POST", "/v1/setup/portico/finish", "", good, &out); w.Code != 200 || out.Account.Role != "owner" {
		t.Fatalf("finishing a Portico setup answered %d: %s", w.Code, w.Body)
	}
	var system System
	call(t, s, "GET", "/v1/system", "", nil, &system)
	if system.SignIn != "portico" || system.Name != "Cabin" || len(system.SignInMethods) != 1 || system.SignInMethods[0] != "portico" {
		t.Fatalf("after a Portico setup the server says %+v", system)
	}
	if kind, _, _ := s.db.Setting(t.Context(), db.SignInKindKey); kind != db.SignInPortico {
		t.Fatalf("sign_in_kind is %q", kind)
	}
	// There is no password to guess.
	if code := status(t, s, "POST", "/v1/auth/sign-in", "", SignInRequest{Username: "justin", Password: "", Device: device(1)}); code != 401 {
		t.Fatalf("a password sign-in to a Portico owner answered %d", code)
	}
	if code := status(t, s, "POST", "/v1/setup/portico/finish", "", good); code != 409 {
		t.Fatalf("a second finish answered %d, want 409", code)
	}
}

// A server set up with local sign-in asks Portico Services nothing, and with no connection
// to it the Portico path says so plainly and changes nothing.
func TestLocalSetupNeverAsksPorticoServices(t *testing.T) {
	s := newTestServer(t)
	s.hosted = hosted.NotConnected{}
	body := SetupPorticoRequest{SetupCode: s.setupCode, ReturnURL: "https://den.example/setup"}
	if w := call(t, s, "POST", "/v1/setup/portico", "", body, nil); w.Code != 503 || w.Header().Get("Retry-After") == "" {
		t.Fatalf("Portico setup with no connection answered %d: %s", w.Code, w.Body)
	}
	s.hosted = &fakeHosted{t: t, fail: true}
	owner := onMain(t, s, setup(t, s))
	signIn(t, s, 1)
	for _, path := range []string{"/v1/system", "/v1/me", "/v1/admin/settings/network"} {
		if code := status(t, s, "GET", path, owner.AccessToken, nil); code != 200 {
			t.Fatalf("%s answered %d", path, code)
		}
	}
	if code := status(t, s, "POST", "/v1/setup/portico", "", body); code != 409 {
		t.Fatalf("Portico setup on a set-up server answered %d, want 409", code)
	}
}
