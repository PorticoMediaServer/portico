package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/hosted"
)

// refusing is what carries every test server's calls to Portico Services unless a test gives
// it a fake: nothing in this package's tests can reach the real service.
type refusing struct{}

func (refusing) RoundTrip(r *http.Request) (*http.Response, error) {
	return nil, errors.New("a test tried to reach Portico Services at " + r.URL.Host + " without a fake")
}

func init() { porticoTransport = refusing{} }

func TestATestServerCannotReachTheRealPorticoServices(t *testing.T) {
	s := newTestServer(t)
	_, err := s.hosted.StartApproval(context.Background(), hosted.ApprovalRequest{ServerID: s.id, ServerName: "Cabin", Purpose: hosted.PurposeSetup,
		ReturnURL: "https://cabin.example/setup"})
	if !errors.Is(err, hosted.ErrNotConnected) || !strings.Contains(err.Error(), "without a fake") {
		t.Fatalf("a test server's call to Portico Services answered %v, want the refusing transport's error", err)
	}
}

// One answer of "I do not know this server" ends nothing: Portico Services' own records can
// be lost. The owner is warned from the first, people stay signed in and can still sign in,
// and only the third check-in, days apart, ends the link. A good check-in in between clears it.
func TestOneUnknownServerAnswerDoesNotEndTheLink(t *testing.T) {
	p := newPorticoRig(t)
	ended := 0
	p.s.link.onUnlinked = func() { ended++ }
	p.link(accountJustin)
	state := func() PorticoLinkState {
		p.owner = onMain(t, p.s, signIn(t, p.s, 7))
		var out PorticoLinkState
		call(t, p.s, "GET", "/v1/admin/portico", p.owner.AccessToken, nil, &out)
		return out
	}
	p.run(24 * time.Hour)

	p.fake.Forget(p.s.id)
	calls := len(p.fake.Calls())
	p.run(24 * time.Hour)
	if got := state(); got.State != "linked" || got.Problem != msgPorticoUnknown {
		t.Fatalf("after one unknown answer: %+v", got)
	}
	if code := status(t, p.s, "POST", "/v1/auth/portico/challenge", "", PorticoChallengeRequest{}); code != 200 {
		t.Errorf("after one unknown answer the server gives no challenge: %d", code)
	}
	p.run(24 * time.Hour)
	if got := state(); got.State != "linked" || ended != 0 {
		t.Fatalf("after two unknown answers: %+v, told ended %d times", got, ended)
	}
	p.run(24 * time.Hour)
	p.step()
	if got := state(); got.State != "unlinked" || got.Problem != msgPorticoUnlinked || ended != 1 {
		t.Errorf("after three unknown answers on three days: %+v, told ended %d times", got, ended)
	}
	if made := len(p.fake.Calls()) - calls; made != 3 {
		t.Errorf("three days of not being known made %d calls, want the three check-ins", made)
	}
	if code := status(t, p.s, "POST", "/v1/auth/portico/challenge", "", PorticoChallengeRequest{}); code != 409 {
		t.Errorf("a server whose link has ended gave a challenge: %d", code)
	}

	// Known again after one unknown answer (the records came back): the count starts over.
	q := newPorticoRig(t)
	q.link(accountJustin)
	q.run(24 * time.Hour)
	q.fake.Forget(q.s.id)
	q.run(24 * time.Hour)
	q.clock.Add(time.Minute)
	q.approve(accountJustin, hosted.PurposeSwitch) // Portico Services has the server's row again
	q.run(24 * time.Hour)
	if link, _, _ := q.s.db.Link(context.Background()); link.Unknown != 0 || link.Unlinked {
		t.Errorf("after Portico Services knew the server again: %+v", link)
	}
}

// An owner with no sign-in at all has a way back that needs nobody: a file in the data
// folder, a restart, and the setup code the server then writes switch the server to local
// sign-in. Without the file the route does nothing, and a wrong code does nothing.
func TestTheOwnerCanSwitchToLocalSignInFromTheDataFolder(t *testing.T) {
	p := newPorticoRig(t)
	p.link(accountJustin)
	code, _, sam := p.signIn(accountSam, p.invite(), 1)
	if code != 200 {
		t.Fatal("sam could not join")
	}
	p.step()
	dir := filepath.Dir(p.path)
	reset := func(setupCode string) (int, string, SignInResponse) {
		var out SignInResponse
		w := call(t, p.s, "POST", "/v1/setup/local-sign-in", "", LocalSignInReset{SetupCode: setupCode, Username: "Rescued", Password: testPassword, Device: device(8)}, nil)
		if w.Code == 200 {
			jsonInto(w, &out)
		}
		return w.Code, refusalCode(w), out
	}
	if code, refused, _ := reset("AAAA-BBBB-CCCC-DDDD"); code != 409 || refused != "reset_not_asked" {
		t.Fatalf("with no file in the data folder the reset answered %d %s", code, refused)
	}

	// Portico Services is away for all of it.
	p.fake.Down(true)
	if err := os.WriteFile(filepath.Join(dir, resetSignInFile), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	p.open()
	ended := 0
	p.s.link.onUnlinked = func() { ended++ }
	p.step()
	if code, _, sam = p.signIn(accountSam, "", 1); code != 200 {
		t.Fatal("sam could not sign in after the restart")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "setup-code.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if code, refused, _ := reset("AAAA-BBBB-CCCC-DDDD"); code != 403 || refused != "setup_code_invalid" {
		t.Errorf("a wrong code answered %d %s, want 403 setup_code_invalid", code, refused)
	}
	if !p.works(sam) {
		t.Fatal("a wrong code changed something")
	}
	code, refused, in := reset(strings.TrimSpace(string(raw)))
	if code != 200 || in.Account.Role != "owner" || in.Account.Username != "rescued" {
		t.Fatalf("the reset answered %d %s %+v", code, refused, in.Account)
	}
	if p.works(sam) {
		t.Error("after the switch a sign-in made with a Portico Account still works")
	}
	var move SignInState
	call(t, p.s, "GET", "/v1/admin/sign-in", onMain(t, p.s, in).AccessToken, nil, &move)
	if move.Move == nil || move.Move.Total == nil || *move.Move.Total != 1 || len(move.Waiting) != 1 {
		t.Fatalf("data-folder reset move: %+v, waiting %+v", move.Move, move.Waiting)
	}
	if code := status(t, p.s, "POST", "/v1/auth/portico/challenge", "", PorticoChallengeRequest{}); code != 409 {
		t.Errorf("after the switch the server still gives Portico challenges: %d", code)
	}
	if w := call(t, p.s, "POST", "/v1/auth/sign-in", "", SignInRequest{Username: "rescued", Password: testPassword, Device: device(9)}, nil); w.Code != 200 {
		t.Errorf("the owner's new password does not sign in: %d %s", w.Code, w.Body)
	}
	for _, name := range []string{resetSignInFile, "setup-code.txt"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			t.Errorf("%s was left in the data folder", name)
		}
	}
	if code, refused, _ := reset(strings.TrimSpace(string(raw))); code != 409 || refused != "reset_not_asked" {
		t.Errorf("the code a second time answered %d %s", code, refused)
	}
	p.step()
	if ended != 1 {
		t.Errorf("the certificate client was told the link ended %d times, want once", ended)
	}

	// Portico Services returns: it is told once that the server left, and then nothing.
	p.fake.Down(false)
	p.run(3 * time.Hour)
	if linked, _ := p.fake.Linked(p.s.id); linked {
		t.Error("Portico Services still holds the server as linked")
	}
	calls := len(p.fake.Calls())
	p.run(5 * 24 * time.Hour)
	if made := len(p.fake.Calls()) - calls; made != 0 {
		t.Errorf("after the leave the server made %d more calls", made)
	}

	// On a server that already uses local sign-in the file asks for nothing.
	if err := os.WriteFile(filepath.Join(dir, resetSignInFile), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	p.open()
	if _, err := os.Stat(filepath.Join(dir, resetSignInFile)); err == nil {
		t.Error("the file was kept on a server with local sign-in")
	}
	if code, refused, _ := reset("AAAA-BBBB-CCCC-DDDD"); code != 409 || refused != "reset_not_asked" {
		t.Errorf("on a local server the reset answered %d %s", code, refused)
	}
}
