package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/hosted"
	"github.com/PorticoMediaServer/portico/server-next/internal/hosted/hostedtest"
	"github.com/PorticoMediaServer/portico/server-next/internal/hosted/schedule"
	"github.com/PorticoMediaServer/portico/server-next/internal/hosted/trust"
)

// porticoRig is a server beside a fake Portico Services that follows the contract and checks
// every signature. The two share one clock, which the test moves; the server's background
// work is stopped and the test steps it instead.
type porticoRig struct {
	*testing.T
	s     *Server
	fake  *hostedtest.Services
	clock *rigClock
	path  string
	owner Session
	// told counts how often the certificate client's hook was called.
	told int
}

var (
	accountJustin = hostedtest.Account{ID: "acct_justin", Username: "Justin", DisplayName: "Justin E", Email: "justin@example.test"}
	accountSam    = hostedtest.Account{ID: "acct_sam", Username: "sam", DisplayName: "Sam", Email: "sam@example.test"}
	accountMal    = hostedtest.Account{ID: "acct_mallory", Username: "mallory", DisplayName: "Mallory", Email: "mallory@example.test"}
)

func newPorticoRig(t *testing.T) *porticoRig {
	p := &porticoRig{T: t, path: filepath.Join(t.TempDir(), "main.db"), clock: newRigClock(time.Now().UTC().Truncate(time.Second))}
	p.fake = hostedtest.New(t)
	p.fake.Now = func() time.Time { return p.clock.Now() }
	p.open()
	p.owner = onMain(t, p.s, setup(t, p.s))
	return p
}

// open starts the server on the rig's database, as a restart does.
func (p *porticoRig) open() {
	p.Helper()
	if p.s != nil {
		closeDB(p.s.db)
	}
	p.s = openTestServer(p.T, p.path)
	attach(p.s, p.fake.Client(), p.fake.URL, p.fake, func() time.Time { return p.clock.Now() })
	p.s.link.onLinked = func(time.Time) { p.told++ }
}

// attach stops a server's own background work and points it at a fake Portico Services.
func attach(s *Server, client *http.Client, origin string, fake *hostedtest.Services, now func() time.Time) {
	s.link.stop()
	<-s.link.done
	s.setClock(now)
	options := hosted.Options{Origin: origin, HTTP: client, ServerID: s.id, Key: s.identity.key, Now: now}
	if fake != nil {
		options.Root = fake.Root()
	}
	c := hosted.New(options)
	if link, linked, _ := s.db.Link(context.Background()); linked {
		c.Keep(parseCertificate(link.Certificate))
	}
	s.link.client, s.hosted, s.link.started = c, c, false
	// The server's own first pass may already have told the real certificate client of a
	// link. The test takes the hooks over, so that client is told to stop and the memory of
	// what was told starts again.
	s.portico.Unlinked()
	s.link.told, s.link.onLinked, s.link.onUnlinked = 0, nil, nil
	s.link.random = func() float64 { return 0.5 }
}

func (p *porticoRig) step() time.Time { return p.s.porticoStep(context.Background()) }

// run moves the clock forward by d, doing the background work whenever it falls due.
func (p *porticoRig) run(d time.Duration) {
	end := p.clock.Now().Add(d)
	for {
		next := p.step()
		if next.After(end) {
			break
		}
		if !next.After(p.clock.Now()) {
			next = p.clock.Now().Add(time.Second)
		}
		p.clock.Set(next)
	}
	p.clock.Set(end)
}

// approve gets an approval for this server from Portico Services as account would give it.
func (p *porticoRig) approve(account hostedtest.Account, purpose hosted.Purpose) hosted.Decision {
	p.Helper()
	ctx := context.Background()
	approval, err := p.s.hosted.StartApproval(ctx, hosted.ApprovalRequest{ServerID: p.s.id, ServerName: "Cabin", Purpose: purpose,
		ReturnURL: "https://cabin.example/setup"})
	if err != nil {
		p.Fatal(err)
	}
	decision, err := p.s.hosted.FinishApproval(ctx, p.s.id, p.fake.Decide(approval.ID, account, true))
	if err != nil {
		p.Fatal(err)
	}
	return decision
}

// link links the server to the owner's Portico Account, as finishing setup with a Portico
// Account does: the approval is asked for, given, checked, and stored with the owner.
func (p *porticoRig) link(account hostedtest.Account) {
	p.Helper()
	decision := p.approve(account, hosted.PurposeSetup)
	if err := p.s.db.LinkServer(context.Background(), db.NewLink{OwnerID: 1, AccountID: decision.Account.ID, IssuedAt: decision.IssuedAt, Now: p.clock.Now()}); err != nil {
		p.Fatal(err)
	}
	p.step()
}

func refusalCode(w *httptest.ResponseRecorder) string {
	var body errorBody
	json.Unmarshal(w.Body.Bytes(), &body)
	return body.Error.Code
}

func (p *porticoRig) challenge() string {
	p.Helper()
	var out PorticoChallenge
	if w := call(p.T, p.s, "POST", "/v1/auth/portico/challenge", "", PorticoChallengeRequest{}, &out); w.Code != 200 || out.ServerID != p.s.id {
		p.Fatalf("the challenge answered %d: %s", w.Code, w.Body)
	}
	return out.Challenge
}

// exchange brings a sign-in note back and answers the status, the refusal's code and the sign-in.
func (p *porticoRig) exchange(challenge string, note trust.Envelope, invitation string, n int) (int, string, SignInResponse) {
	p.Helper()
	raw, _ := json.Marshal(note)
	var out SignInResponse
	w := call(p.T, p.s, "POST", "/v1/auth/portico/exchange", "", PorticoExchange{Challenge: challenge, Identity: raw, Invitation: invitation, Device: device(n)}, nil)
	if w.Code == 200 {
		if err := jsonInto(w, &out); err != nil {
			p.Fatal(err)
		}
	}
	return w.Code, refusalCode(w), out
}

// signIn is a whole sign-in with a Portico Account, as an app makes it.
func (p *porticoRig) signIn(account hostedtest.Account, invitation string, n int) (int, string, SignInResponse) {
	p.Helper()
	challenge := p.challenge()
	return p.exchange(challenge, p.fake.Identity(account, p.s.id, challenge), invitation, n)
}

// notice delivers a note to the server's open route and answers the status and the acknowledgement.
func (p *porticoRig) notice(note trust.Envelope) (int, trust.SignedAck) {
	p.Helper()
	raw, _ := json.Marshal(note)
	var out HostedNoticeAck
	w := call(p.T, p.s, "POST", "/v1/hosted/notices", "", HostedNotice{Note: raw}, nil)
	if w.Code == 200 {
		jsonInto(w, &out)
	}
	return w.Code, out.Ack
}

func (p *porticoRig) invite() string {
	p.Helper()
	var out Invitation
	if w := call(p.T, p.s, "POST", "/v1/admin/invitations", p.owner.AccessToken, InvitationCreate{}, &out); w.Code >= 300 || out.Code == "" {
		p.Fatalf("the invitation answered %d: %s", w.Code, w.Body)
	}
	return out.Code
}

// works says whether a sign-in's access token is still let in.
func (p *porticoRig) works(in SignInResponse) bool {
	p.Helper()
	return status(p.T, p.s, "GET", "/v1/me", in.Session.AccessToken, nil) == 200
}

// refuseAll is a transport that fails the test if anything is sent through it.
type refuseAll struct {
	t     *testing.T
	calls int
}

func (r *refuseAll) RoundTrip(req *http.Request) (*http.Response, error) {
	r.calls++
	r.t.Errorf("a server with local sign-in sent %s %s", req.Method, req.URL)
	return nil, http.ErrHandlerTimeout
}

// A server set up with local sign-in never contacts Portico Services: not when it is set up,
// when people sign in, when members change, over days of background work, across a restart,
// or when someone calls the routes a Portico Accounts server answers.
func TestALocalServerNeverContactsPorticoServices(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.db")
	clock := newRigClock(time.Now().UTC())
	transport := &refuseAll{t: t}
	var s *Server
	open := func() {
		if s != nil {
			closeDB(s.db)
		}
		s = openTestServer(t, path)
		attach(s, &http.Client{Transport: transport}, "", nil, clock.Now)
	}
	days := func(n int) {
		for end := clock.Now().Add(time.Duration(n) * 24 * time.Hour); clock.Now().Before(end); clock.Add(time.Minute) {
			s.porticoStep(context.Background())
		}
	}
	open()
	owner := onMain(t, s, setup(t, s))
	addMember(t, s, owner.AccessToken, "sam", "member")
	signInAs(t, s, "sam", testPassword, 1)
	var state PorticoLinkState
	if w := call(t, s, "GET", "/v1/admin/portico", owner.AccessToken, nil, &state); w.Code != 200 || state.State != "local" {
		t.Errorf("the link state of a local server answered %d %+v", w.Code, state)
	}
	for route, want := range map[string]int{
		"/v1/auth/portico/challenge":         409,
		"/v1/hosted/notices":                 404,
		"/v1/hosted/approvals/apr_1/poll":    409,
		"/v1/admin/portico/link":             409,
		"/v1/admin/portico/link/finish":      409,
		"/v1/auth/portico/exchange":          409,
		"/v1/hosted/approvals/anything/poll": 409,
	} {
		var body any = map[string]any{"returnUrl": "https://cabin.example/back", "approval": "x", "note": map[string]any{}, "device": device(3)}
		if strings.HasSuffix(route, "/challenge") {
			body = PorticoChallengeRequest{}
		}
		if w := call(t, s, "POST", route, owner.AccessToken, body, nil); w.Code != want {
			t.Errorf("POST %s on a local server answered %d, want %d: %s", route, w.Code, want, w.Body)
		}
	}
	days(3)
	open()
	signIn(t, s, 2)
	days(3)
	if transport.calls != 0 {
		t.Errorf("a local server made %d calls to Portico Services, want none", transport.calls)
	}
	// The certificate client is the other thing that can call Portico Services. It is never
	// told of a link on a local server, so it has never begun.
	if state := s.portico.Status().State; state != "off" {
		t.Errorf("on a local server the certificate client is %q, want off", state)
	}
}

// Linking, then the life of a linked server: the certificate client is told, Portico Services
// gets the server's document, the owner signs in on a new device with a Portico Account and
// nothing else, a person joins with an invitation and their Portico Account, and the server
// keeps each person's email address and username as Portico Services gave them.
func TestLinkingAndSigningInWithAPorticoAccount(t *testing.T) {
	p := newPorticoRig(t)
	if code := status(t, p.s, "POST", "/v1/auth/portico/challenge", "", PorticoChallengeRequest{}); code != 409 {
		t.Fatalf("a server not yet linked gave a challenge: %d", code)
	}
	p.link(accountJustin)
	if p.told != 1 {
		t.Errorf("the certificate client was told of the link %d times, want once", p.told)
	}
	if linked, members := p.fake.Linked(p.s.id); !linked || members[accountJustin.ID] != "owner" || len(members) != 1 {
		t.Fatalf("after linking, Portico Services holds linked=%v members=%v", linked, members)
	}
	if doc := p.fake.Document(p.s.id); doc["name"] != "Portico" || doc["ownerAccountId"] != accountJustin.ID {
		t.Errorf("unexpected document: %v", doc)
	}
	var state PorticoLinkState
	if w := call(t, p.s, "GET", "/v1/admin/portico", p.owner.AccessToken, nil, &state); w.Code != 200 || state.State != "linked" || !state.Listed ||
		state.AccountID != accountJustin.ID {
		t.Errorf("the link state answered %d %+v", w.Code, state)
	}

	// The owner on a new device: the Portico Account alone signs in, as the owner.
	code, _, in := p.signIn(accountJustin, "", 1)
	if code != 200 || in.Account.Role != "owner" || in.Account.Email != accountJustin.Email {
		t.Fatalf("the owner's sign-in answered %d %+v", code, in.Account)
	}

	// A stranger with a real Portico Account is not let in, with or without a wrong invitation.
	if code, refused, _ := p.signIn(accountSam, "", 2); code != 403 || refused != "not_a_member" {
		t.Errorf("a Portico Account that is not on the server answered %d %s, want 403 not_a_member", code, refused)
	}
	if code, refused, _ := p.signIn(accountSam, "AAAA-BBBB-CCCC", 2); code != 404 || refused != "invitation_not_found" {
		t.Errorf("a wrong invitation answered %d %s, want 404 invitation_not_found", code, refused)
	}

	// With an invitation they join, once, and Portico Services is sent the new member list.
	invitation := p.invite()
	code, _, in = p.signIn(accountSam, invitation, 2)
	if code != 200 || in.Account.Role != "member" || in.Account.Username != "sam" || in.Account.Email != accountSam.Email {
		t.Fatalf("joining with an invitation answered %d %+v", code, in.Account)
	}
	if code, refused, _ := p.signIn(accountMal, invitation, 3); code != 404 {
		t.Errorf("a used invitation let a second Portico Account in: %d %s", code, refused)
	}
	p.run(documentGap)
	if _, members := p.fake.Linked(p.s.id); members[accountSam.ID] != "member" || len(members) != 2 {
		t.Errorf("after a person joined, Portico Services holds %v", members)
	}

	// The username and email address follow the Portico Account at the next sign-in; the
	// account id is the identity, so a changed name is still the same person.
	renamed := accountSam
	renamed.Username, renamed.Email = "samantha", "samantha@example.test"
	if code, _, in = p.signIn(renamed, "", 2); code != 200 || in.Account.Username != "samantha" || in.Account.Email != renamed.Email {
		t.Errorf("after the Portico Account changed its name: %d %+v", code, in.Account)
	}
	// A Portico Account that takes a username someone here already has does not become them.
	impostor := accountMal
	impostor.Username = "samantha"
	if code, _, in := p.signIn(impostor, p.invite(), 3); code != 200 || in.Account.Username == "samantha" || in.Account.ID == "" {
		t.Errorf("a second Portico Account with a member's username answered %d %+v", code, in.Account)
	}

	// A restart keeps the link and calls nobody: starting is not a reason to call.
	before := len(p.fake.Calls())
	p.open()
	p.run(documentGap)
	if after := len(p.fake.Calls()); after != before+1 || p.fake.Count("PUT", "/document") != 3 {
		// The one call is the document with the third member, which was still waiting.
		t.Errorf("a restart made %d calls, want only the waiting document", after-before)
	}
	if p.told != 2 {
		t.Errorf("after a restart the certificate client was told %d times in all, want twice", p.told)
	}
	if code, _, _ := p.signIn(accountJustin, "", 4); code != 200 {
		t.Errorf("the owner could not sign in after a restart: %d", code)
	}
}

// A sign-in note is good for this server, this link and this moment, once. One made for
// another server, under an earlier link, past its time, signed by anyone but Portico
// Services, or used before signs nobody in.
func TestASignInNoteIsBoundToThisServerThisLinkAndThisMoment(t *testing.T) {
	p := newPorticoRig(t)
	p.link(accountJustin)
	refused := func(what string, challenge string, note trust.Envelope) {
		t.Helper()
		if code, reason, _ := p.exchange(challenge, note, "", 5); code != 401 || reason != "portico_sign_in_invalid" {
			t.Errorf("%s answered %d %s, want 401 portico_sign_in_invalid", what, code, reason)
		}
	}

	challenge := p.challenge()
	refused("a note made for another server", challenge, p.fake.Identity(accountJustin, "srv_"+strings.Repeat("A", 43), challenge))
	refused("a note answering another challenge", challenge, p.fake.Identity(accountJustin, p.s.id, p.challenge()))
	refused("a challenge this server never gave", "made-up-challenge-of-enough-length", p.fake.Identity(accountJustin, p.s.id, "made-up-challenge-of-enough-length"))

	other := hostedtest.New(t)
	refused("a note signed by someone else's root", challenge, other.Identity(accountJustin, p.s.id, challenge))

	// Past its time: the note lasts five minutes, and so does the challenge.
	note := p.fake.Identity(accountJustin, p.s.id, challenge)
	p.clock.Add(6 * time.Minute)
	refused("a note past its time", challenge, note)

	// Used once, a note and its challenge are spent: the copy changes nothing.
	challenge = p.challenge()
	note = p.fake.Identity(accountJustin, p.s.id, challenge)
	if code, _, _ := p.exchange(challenge, note, "", 5); code != 200 {
		t.Fatalf("a good note answered %d", code)
	}
	var sessions, after Page[SessionInfo]
	call(t, p.s, "GET", "/v1/me/sessions", p.owner.AccessToken, nil, &sessions)
	refused("the same note a second time", challenge, note)
	if call(t, p.s, "GET", "/v1/me/sessions", p.owner.AccessToken, nil, &after); len(after.Items) != len(sessions.Items) || len(after.Items) == 0 {
		t.Errorf("a replayed note changed the owner's sign-ins from %d to %d", len(sessions.Items), len(after.Items))
	}

	// An earlier link: a challenge given, and its note signed, before the server was linked
	// again are no use under the new link.
	challenge = p.challenge()
	note = p.fake.Identity(accountJustin, p.s.id, challenge)
	p.clock.Add(time.Minute)
	p.relink(accountJustin)
	refused("a note made under an earlier link", challenge, note)

	// A key Portico Services has withdrawn signs nothing, even with its own valid certificate.
	challenge = p.challenge()
	note = p.fake.Identity(accountJustin, p.s.id, challenge)
	withdrawn := p.fake.NewSigningKey(2, nil)
	p.fake.NewSigningKey(3, []string{withdrawn})
	p.fake.RaiseFleet()
	if code, _ := p.notice(p.fake.Note(p.s.id)); code != 200 {
		t.Fatalf("the note carrying the new certificate answered %d", code)
	}
	refused("a note signed by a withdrawn key", challenge, note)
	p.open()
	refused("a note signed by a withdrawn key, after a restart", challenge, note)
	if code, _, _ := p.signIn(accountJustin, "", 5); code != 200 {
		t.Errorf("a sign-in under the new signing key answered %d", code)
	}
}

// relink links the server again through the owner's routes, as "Link again" does.
func (p *porticoRig) relink(account hostedtest.Account) (int, string) {
	p.Helper()
	var approval PorticoApproval
	if w := call(p.T, p.s, "POST", "/v1/admin/portico/link", p.owner.AccessToken, PorticoLinkRequest{ReturnURL: "https://cabin.example/back"}, &approval); w.Code != 200 {
		p.Fatalf("starting to link again answered %d: %s", w.Code, w.Body)
	}
	if approval.Code == "" || approval.ApprovalURL == "" || approval.PollAfterMs != 3000 {
		p.Fatalf("unexpected approval: %+v", approval)
	}
	returned := p.fake.Decide(approval.ApprovalID, account, true)
	w := call(p.T, p.s, "POST", "/v1/admin/portico/link/finish", p.owner.AccessToken, PorticoLinkFinish{Approval: returned}, nil)
	p.step()
	return w.Code, refusalCode(w)
}

// What Portico Services tells a server reaches it as a signed note, by a push, by an app or
// in the daily check-in. A note for another server, past its time or not signed by Portico
// Services is refused; a good one turns off the people who left and is acknowledged; a copy,
// a late one and one from an earlier link are acknowledged and change nothing, whatever
// their fleet number says.
func TestNotesAreAppliedOnceAndOnlyForThisLink(t *testing.T) {
	p := newPorticoRig(t)
	p.link(accountJustin)
	code, _, sam := p.signIn(accountSam, p.invite(), 1)
	if code != 200 {
		t.Fatal("sam could not join")
	}
	code, _, mallory := p.signIn(accountMal, p.invite(), 2)
	if code != 200 {
		t.Fatal("mallory could not join")
	}
	p.step()

	now := p.clock.Now()
	sign := func(n trust.Note) trust.Envelope {
		n.Kind = trust.NoteKind
		if n.IssuedAt.IsZero() {
			n.IssuedAt, n.ExpiresAt = now, now.Add(trust.NoteLifetime)
		}
		return p.fake.Sign(n)
	}
	link, _, _ := p.s.db.Link(context.Background())
	if code, _ := p.notice(sign(trust.Note{ServerID: "srv_" + strings.Repeat("B", 43), Revision: link.NoteRevision + 100, Left: []string{accountSam.ID}})); code != 403 {
		t.Errorf("a note for another server answered %d, want 403", code)
	}
	if code, _ := p.notice(sign(trust.Note{ServerID: p.s.id, Revision: link.NoteRevision + 100, Left: []string{accountSam.ID},
		IssuedAt: now.Add(-8 * 24 * time.Hour), ExpiresAt: now.Add(-24 * time.Hour)})); code != 403 {
		t.Errorf("a note past its time answered %d, want 403", code)
	}
	if code, _ := p.notice(hostedtest.New(t).Sign(trust.Note{Kind: trust.NoteKind, ServerID: p.s.id, Revision: link.NoteRevision + 100,
		Left: []string{accountSam.ID}, IssuedAt: now, ExpiresAt: now.Add(time.Hour)})); code != 403 {
		t.Errorf("a note signed by someone else answered %d, want 403", code)
	}
	if !p.works(sam) {
		t.Fatal("a refused note ended sam's sign-in")
	}

	// A note of an earlier link carries numbers below this link's first. It is acknowledged
	// and ignored, and a high fleet number does not carry it through.
	old := sign(trust.Note{ServerID: p.s.id, Revision: link.IssuedAt - 10, Fleet: now.Unix() + 500, Left: []string{accountSam.ID}, Unlinked: true})
	code, ack := p.notice(old)
	after, _, _ := p.s.db.Link(context.Background())
	if code != 200 || after.Unlinked || after.NoteRevision != link.NoteRevision || after.Fleet != now.Unix()+500 {
		t.Errorf("an earlier link's note answered %d and left the link at %+v", code, after)
	}
	if !p.works(sam) {
		t.Error("an earlier link's note ended sam's sign-in")
	}
	if err := p.fake.Ack(p.s.id, ack); err != nil {
		t.Errorf("the acknowledgement is not signed by the server's own key: %v", err)
	}

	// Sam leaves through their Portico Account. The real note turns them off, ends their
	// sign-ins, deletes nothing, and their next sign-in is refused in plain words.
	p.fake.MemberLeft(p.s.id, accountSam.ID)
	note := p.fake.Note(p.s.id)
	if code, ack = p.notice(note); code != 200 || p.fake.Ack(p.s.id, ack) != nil {
		t.Fatalf("the note answered %d", code)
	}
	if p.works(sam) {
		t.Error("after leaving, sam's sign-in still works")
	}
	if !p.works(mallory) {
		t.Error("mallory, who did not leave, was signed out")
	}
	if code, refused, _ := p.signIn(accountSam, "", 1); code != 403 || refused != "left_server" {
		t.Errorf("a person who left answered %d %s, want 403 left_server", code, refused)
	}

	// The same note again, by any road, is acknowledged with the same numbers and does nothing.
	applied, _, _ := p.s.db.Link(context.Background())
	if code, again := p.notice(note); code != 200 || again != ack {
		t.Errorf("a repeated note answered %d with a different acknowledgement", code)
	}
	if replayed, _, _ := p.s.db.Link(context.Background()); replayed.NoteRevision != applied.NoteRevision || replayed.Fleet != applied.Fleet || !p.works(mallory) {
		t.Errorf("a repeated note changed something")
	}

	// A person the owner removed is simply not on the server any more.
	var members Page[Member]
	call(t, p.s, "GET", "/v1/admin/members", p.owner.AccessToken, nil, &members)
	for _, m := range members.Items {
		if m.Username == "mallory" {
			if code := status(t, p.s, "DELETE", "/v1/admin/members/"+m.ID, p.owner.AccessToken, nil); code >= 300 {
				t.Fatalf("removing mallory answered %d", code)
			}
		}
	}
	if code, refused, _ := p.signIn(accountMal, "", 2); code != 403 || refused != "not_a_member" {
		t.Errorf("a removed person answered %d %s, want 403 not_a_member", code, refused)
	}
	p.step()
	if _, held := p.fake.Linked(p.s.id); len(held) != 1 {
		t.Errorf("after one person left and one was removed, Portico Services still holds %v", held)
	}

	// The owner removes the server from their Portico Account: the server is told, says so
	// to its owner, gives no more challenges and stops calling.
	p.fake.Unlink(p.s.id)
	if code, _ := p.notice(p.fake.Note(p.s.id)); code != 200 {
		t.Fatalf("the unlinked note answered %d", code)
	}
	var state PorticoLinkState
	call(t, p.s, "GET", "/v1/admin/portico", p.owner.AccessToken, nil, &state)
	if state.State != "unlinked" || state.Problem != msgPorticoUnlinked {
		t.Errorf("after the owner removed the server: %+v", state)
	}
	if code := status(t, p.s, "POST", "/v1/auth/portico/challenge", "", PorticoChallengeRequest{}); code != 409 {
		t.Errorf("an unlinked server gave a challenge: %d", code)
	}
	calls := len(p.fake.Calls())
	p.run(3 * 24 * time.Hour)
	if made := len(p.fake.Calls()) - calls; made != 0 {
		t.Errorf("an unlinked server made %d calls in three days, want none", made)
	}
	// Linking again, by the same Portico Account, brings it back with its people as they were.
	p.owner = onMain(t, p.s, signIn(t, p.s, 9))
	if code, refused := p.relink(accountJustin); code != 200 {
		t.Fatalf("linking again answered %d %s", code, refused)
	}
	if linked, _ := p.fake.Linked(p.s.id); !linked {
		t.Error("after linking again Portico Services does not hold the server as linked")
	}
	if code, _, _ := p.signIn(accountJustin, "", 3); code != 200 {
		t.Errorf("the owner could not sign in after linking again: %d", code)
	}
}

// Only the Portico Account that owns the standing link can link the server again. An
// approval from any other account, one for another purpose, an old one and a used one all
// leave the server exactly as it was.
func TestOnlyTheOwningAccountCanApproveALinkedServer(t *testing.T) {
	p := newPorticoRig(t)
	p.link(accountJustin)
	link, _, _ := p.s.db.Link(context.Background())
	finish := func(returned string) (int, string) {
		w := call(t, p.s, "POST", "/v1/admin/portico/link/finish", p.owner.AccessToken, PorticoLinkFinish{Approval: returned}, nil)
		return w.Code, refusalCode(w)
	}
	signed := func(a trust.Approval) string {
		a.Kind, a.ServerID, a.ServerName = trust.ApprovalKind, p.s.id, "Cabin"
		if a.IssuedAt.IsZero() {
			a.IssuedAt = p.clock.Now().Add(time.Minute)
		}
		a.ExpiresAt = a.IssuedAt.Add(10 * time.Minute)
		return trust.Returned(p.fake.Sign(a))
	}
	p.clock.Add(2 * time.Minute)

	// Portico Services itself refuses to let another account approve a linked server. Even
	// if such an approval existed, properly signed, the server would not take it.
	if code, refused := finish(signed(trust.Approval{ApprovalID: "apr_x1", Purpose: trust.PurposeSwitch, AccountID: accountMal.ID, Username: "mallory"})); code != 403 ||
		refused != "not_the_linked_account" {
		t.Errorf("an approval from an account that does not own the link answered %d %s, want 403 not_the_linked_account", code, refused)
	}
	for what, approval := range map[string]string{
		"an approval for another purpose":   signed(trust.Approval{ApprovalID: "apr_x2", Purpose: trust.PurposeSetup, AccountID: accountJustin.ID}),
		"an approval for a change of owner": signed(trust.Approval{ApprovalID: "apr_x3", Purpose: trust.PurposeChangeOwner, AccountID: accountMal.ID}),
		"an approval older than the link": signed(trust.Approval{ApprovalID: "apr_x4", Purpose: trust.PurposeSwitch, AccountID: accountJustin.ID,
			IssuedAt: time.Unix(link.IssuedAt, 0).Add(-time.Minute)}),
		"an approval signed by someone else": trust.Returned(hostedtest.New(t).Sign(trust.Approval{Kind: trust.ApprovalKind, ApprovalID: "apr_x5", ServerID: p.s.id,
			Purpose: trust.PurposeSwitch, AccountID: accountJustin.ID, IssuedAt: p.clock.Now(), ExpiresAt: p.clock.Now().Add(time.Minute)})),
		"an approval for another server": trust.Returned(p.fake.Sign(trust.Approval{Kind: trust.ApprovalKind, ApprovalID: "apr_x6", ServerID: "srv_" + strings.Repeat("C", 43),
			Purpose: trust.PurposeSwitch, AccountID: accountJustin.ID, IssuedAt: p.clock.Now(), ExpiresAt: p.clock.Now().Add(time.Minute)})),
		"nonsense": "bm9uc2Vuc2U",
	} {
		if code, refused := finish(approval); code != 409 || refused != "approval_declined" {
			t.Errorf("%s answered %d %s, want 409 approval_declined", what, code, refused)
		}
	}
	if after, _, _ := p.s.db.Link(context.Background()); after.AccountID != link.AccountID || after.IssuedAt != link.IssuedAt || after.NoteRevision != link.NoteRevision {
		t.Fatalf("a refused approval changed the link: %+v", after)
	}
	if owner, _ := p.s.db.Account(context.Background(), 1); owner.Role != "owner" {
		t.Fatalf("the owner changed: %+v", owner)
	}

	// The owner's own account can; the same approval a second time cannot.
	good := signed(trust.Approval{ApprovalID: "apr_good", Purpose: trust.PurposeSwitch, AccountID: accountJustin.ID, Username: "justin"})
	if code, refused := finish(good); code != 200 {
		t.Fatalf("the owning account's approval answered %d %s", code, refused)
	}
	relinked, _, _ := p.s.db.Link(context.Background())
	if relinked.IssuedAt <= link.IssuedAt || relinked.NoteRevision != relinked.IssuedAt {
		t.Errorf("the new link's numbers do not start at its approval: %+v", relinked)
	}
	if code, refused := finish(good); code != 409 || refused != "approval_declined" {
		t.Errorf("a used approval answered %d %s, want 409 approval_declined", code, refused)
	}

	// A person approving on another device: the page asks the server, which asks Portico
	// Services no faster than it was told to, and only about approvals it started itself.
	p.clock.Add(5 * time.Minute)
	var approval PorticoApproval
	call(t, p.s, "POST", "/v1/admin/portico/link", p.owner.AccessToken, PorticoLinkRequest{ReturnURL: "https://cabin.example/back"}, &approval)
	poll := func(id string) (int, PorticoApprovalPoll) {
		var out PorticoApprovalPoll
		w := call(t, p.s, "POST", "/v1/hosted/approvals/"+id+"/poll", "", PorticoApprovalPollRequest{PollToken: approval.PollToken}, nil)
		jsonInto(w, &out)
		return w.Code, out
	}
	asked := func() int { return p.fake.Count("GET", "/v1/approvals/"+approval.ApprovalID) }
	for range 5 {
		if code, out := poll(approval.ApprovalID); code != 200 || out.State != "waiting" {
			t.Fatalf("a poll of a waiting approval answered %d %+v", code, out)
		}
	}
	if asked() != 1 {
		t.Errorf("five polls in one moment asked Portico Services %d times, want once", asked())
	}
	if code, _ := poll("apr_not_ours"); code != 409 || p.fake.Count("GET", "/v1/approvals/apr_not_ours") != 0 {
		t.Errorf("a poll of an approval this server never started answered %d and may have asked Portico Services", code)
	}
	// Someone who saw only the approval's address cannot collect the approval.
	p.clock.Add(3 * time.Second)
	p.fake.Decide(approval.ApprovalID, accountJustin, true)
	if code := status(t, p.s, "POST", "/v1/hosted/approvals/"+approval.ApprovalID+"/poll", "", PorticoApprovalPollRequest{PollToken: "guess"}); code != 409 || asked() != 1 {
		t.Errorf("a poll without the page's token answered %d", code)
	}
	code, out := poll(approval.ApprovalID)
	if code != 200 || out.State != "approved" || out.Approval == "" {
		t.Fatalf("a poll of a given approval answered %d %+v", code, out)
	}
	if code, refused := finish(out.Approval); code != 200 {
		t.Errorf("finishing with the polled approval answered %d %s", code, refused)
	}
}

// The daily check-in is the backstop: it carries the server's numbers, brings the note the
// server missed, and happens at the server's own second of the day. When Portico Services
// is away the server backs off and keeps its place across a restart; when it returns, the
// server is back on its own second the next day.
func TestTheDailyCheckInAndAnOutage(t *testing.T) {
	p := newPorticoRig(t)
	p.link(accountJustin)
	if code, _, _ := p.signIn(accountSam, p.invite(), 1); code != 200 {
		t.Fatal("sam could not join")
	}
	p.step()
	checkIns := func() []hostedtest.Call {
		var out []hostedtest.Call
		for _, c := range p.fake.Calls() {
			if strings.HasSuffix(c.Path, "/check-in") {
				out = append(out, c)
			}
		}
		return out
	}

	// Three quiet days: one check-in a day, each on the server's own second.
	p.run(3 * 24 * time.Hour)
	made := checkIns()
	if len(made) != 3 {
		t.Fatalf("three days made %d check-ins, want 3", len(made))
	}
	for _, c := range made {
		if second := c.At.UTC().Hour()*3600 + c.At.UTC().Minute()*60 + c.At.UTC().Second(); second != schedule.Slot(p.s.id) {
			t.Errorf("a check-in at second %d of the day, want the server's own %d", second, schedule.Slot(p.s.id))
		}
	}
	if other := len(p.fake.Calls()) - 1 - 2 - len(made); other != 0 {
		// One approval asked for and two documents sent while setting up; nothing since.
		t.Errorf("three quiet days made %d calls besides the check-ins", other)
	}

	// Sam leaves while no push gets through: the next check-in brings the note.
	p.fake.MemberLeft(p.s.id, accountSam.ID)
	p.run(24 * time.Hour)
	if code, refused, _ := p.signIn(accountSam, "", 1); code != 403 || refused != "left_server" {
		t.Errorf("a day after sam left at Portico Services, sam's sign-in answers %d %s, want 403 left_server", code, refused)
	}

	// Two days with Portico Services away. The server backs off to hours between tries.
	p.fake.Down(true)
	start := len(checkIns())
	p.run(24 * time.Hour)
	p.open() // a restart in the middle of the outage keeps the wait
	p.run(24 * time.Hour)
	tried := checkIns()[start:]
	if len(tried) < 8 || len(tried) > 20 {
		t.Errorf("two days of outage saw %d tries, want a handful a day", len(tried))
	}
	// The waits only ever grow, to hours, and the restart does not start them again.
	for i := 2; i < len(tried); i++ {
		gap, before := tried[i].At.Sub(tried[i-1].At), tried[i-1].At.Sub(tried[i-2].At)
		if gap < before {
			t.Errorf("a wait of %s followed one of %s: the backoff started again", gap, before)
		}
	}
	if last := tried[len(tried)-1].At.Sub(tried[len(tried)-2].At); last < 3*time.Hour {
		t.Errorf("at the end of a two-day outage the tries were %s apart, want hours", last)
	}

	// It returns: one good check-in, then the server's own second again.
	p.fake.Down(false)
	start = len(checkIns())
	p.run(3 * 24 * time.Hour)
	back := checkIns()[start:]
	if len(back) != 4 && len(back) != 3 {
		t.Fatalf("three days after the outage made %d check-ins, want the catch-up and one a day", len(back))
	}
	for _, c := range back[1:] {
		if second := c.At.UTC().Hour()*3600 + c.At.UTC().Minute()*60 + c.At.UTC().Second(); second != schedule.Slot(p.s.id) {
			t.Errorf("after the outage a check-in came at second %d of the day, want the server's own %d", second, schedule.Slot(p.s.id))
		}
	}
}

// Servers that failed together do not return together. A fleet whose check-ins all failed
// through a long outage comes back spread over hours, and a fleet that was switched off for
// days and starts in the same second spreads itself over the next hour.
func TestCheckInsAfterAnOutageStaySpread(t *testing.T) {
	const fleet = 2000
	random := rand.New(rand.NewPCG(7, 11))
	start := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	back := start.Add(30 * time.Hour)
	spread := func(arrivals []time.Duration, window, total time.Duration, share float64, what string) {
		t.Helper()
		bins := make([]int, int(total/window)+1)
		for _, a := range arrivals {
			if a < 0 || a > total {
				t.Fatalf("%s: an arrival %s after the start, outside %s", what, a, total)
			}
			bins[int(a/window)]++
		}
		for i, n := range bins {
			if float64(n) > share*float64(len(arrivals)) {
				t.Errorf("%s: %d of %d arrived in the %s starting at %s, more than %.0f%%", what, n, len(arrivals), window, time.Duration(i)*window, share*100)
			}
		}
	}

	var afterOutage []time.Duration
	for i := range fleet {
		id := "srv_" + strings.Repeat("x", 30) + strings.Repeat("0", 13-len(itoa(i))) + itoa(i)
		l := db.Link{LastContact: start.Add(-time.Hour).Unix()}
		planCheckIn(&l, id, start, random.Float64())
		at := time.Unix(l.CheckInAt, 0)
		tries := 0
		for at.Before(back) {
			checkedIn(&l, id, at, hosted.ErrNotConnected, random.Float64())
			at = time.Unix(l.CheckInAt, 0)
			if tries++; tries > 40 {
				t.Fatalf("a server tried more than 40 times in a 30-hour outage")
			}
		}
		afterOutage = append(afterOutage, at.Sub(back))
		// Once answered it is back on its own second.
		checkedIn(&l, id, at, nil, random.Float64())
		if l.CheckInAt != schedule.NextCheckIn(id, at).Unix() || l.CheckInTries != 0 {
			t.Fatalf("after a good check-in the next is at %d, want the server's own second", l.CheckInAt)
		}
	}
	// No five minutes sees more than 5% of the fleet (an even spread over six hours is 1.4%).
	spread(afterOutage, 5*time.Minute, 6*time.Hour, 0.05, "after a 30-hour outage")

	var afterPowerCut []time.Duration
	for i := range fleet {
		id := "srv_" + strings.Repeat("y", 30) + strings.Repeat("0", 13-len(itoa(i))) + itoa(i)
		l := db.Link{LastContact: start.Add(-72 * time.Hour).Unix(), CheckInAt: start.Add(-48 * time.Hour).Unix()}
		planCheckIn(&l, id, start, random.Float64())
		afterPowerCut = append(afterPowerCut, time.Unix(l.CheckInAt, 0).Sub(start))
	}
	// An even spread over the hour is 8.3% in five minutes.
	spread(afterPowerCut, 5*time.Minute, time.Hour, 0.15, "after three days switched off")

	// Portico Services saying "retry after" is obeyed as it stands and is not a failed try.
	l := db.Link{CheckInTries: 2}
	checkedIn(&l, "srv_z", start, &hosted.BusyError{Wait: 10 * time.Minute}, 0.5)
	if l.CheckInAt != start.Add(10*time.Minute).Unix() || l.CheckInTries != 2 {
		t.Errorf("after 'retry after 10 minutes' the next try is %s later with %d tries", time.Unix(l.CheckInAt, 0).Sub(start), l.CheckInTries)
	}
	checkedIn(&l, "srv_z", start, &hosted.BusyError{Wait: time.Second}, 0.5)
	if l.CheckInAt != start.Add(schedule.MinimumRetryAfter).Unix() {
		t.Errorf("a 'retry after' of one second was obeyed; the shortest wait is %s", schedule.MinimumRetryAfter)
	}
}

func itoa(n int) string {
	raw, _ := json.Marshal(n)
	return string(raw)
}

// A server that switched to local sign-in says "this server has left" once, naming the link
// it left, keeps trying while Portico Services is away, and after seven days gives up and
// never calls again.
func TestAServerThatLeftSaysSoOnceAndThenGivesUp(t *testing.T) {
	ctx := context.Background()
	queue := func(p *porticoRig) {
		// What the switch to local sign-in commits: the link gone, the one message queued.
		link, _, _ := p.s.db.Link(ctx)
		raw, _ := json.Marshal(db.QueuedLeave{LinkIssuedAt: link.IssuedAt, GiveUpAt: p.clock.Now().Add(schedule.LeaveGiveUp).Unix()})
		if err := p.s.db.SaveSetting(ctx, "hosted.leave", string(raw), p.clock.Now()); err != nil {
			t.Fatal(err)
		}
		raw2, err := sql.Open("sqlite", "file:"+url.PathEscape(p.path)+"?_pragma=busy_timeout(5000)")
		if err != nil {
			t.Fatal(err)
		}
		defer raw2.Close()
		if _, err := raw2.Exec(`DELETE FROM settings WHERE key = 'hosted.link'`); err != nil {
			t.Fatal(err)
		}
	}

	p := newPorticoRig(t)
	p.link(accountJustin)
	queue(p)
	p.run(time.Hour)
	if linked, _ := p.fake.Linked(p.s.id); linked || p.fake.Count("POST", "/leave") != 1 {
		t.Errorf("after leaving: still linked at Portico Services %v, %d leave calls, want one", linked, p.fake.Count("POST", "/leave"))
	}
	calls := len(p.fake.Calls())
	p.run(10 * 24 * time.Hour)
	if made := len(p.fake.Calls()) - calls; made != 0 {
		t.Errorf("a server that has left made %d more calls in ten days, want none", made)
	}

	// With Portico Services away for good: tries for seven days, spaced out, then silence.
	q := newPorticoRig(t)
	q.link(accountJustin)
	q.fake.Down(true)
	queue(q)
	q.run(8 * 24 * time.Hour)
	tries := q.fake.Count("POST", "/leave")
	if tries < 100 || tries > 300 {
		t.Errorf("seven days of trying to leave made %d tries, want about one an hour", tries)
	}
	if _, leaving, _ := q.s.db.Leave(ctx); leaving {
		t.Error("the leave was still queued after its give-up time")
	}
	q.fake.Down(false)
	calls = len(q.fake.Calls())
	q.open()
	q.run(10 * 24 * time.Hour)
	if made := len(q.fake.Calls()) - calls; made != 0 {
		t.Errorf("after giving up, the server made %d calls, want none ever", made)
	}
}

// A home connection whose address flips every minute does not make the server call Portico
// Services every minute: after a document is taken, one sent only because the addresses
// changed waits five minutes, and what is sent then is the address that is true then. A
// first link, a change of members and a change the owner makes by hand still go at once.
func TestAnAddressThatKeepsChangingIsSentAtMostEveryFewMinutes(t *testing.T) {
	p := newPorticoRig(t)
	address := "203.0.113.1"
	p.s.link.published = func() []hosted.Address {
		return []hosted.Address{{Address: "192.168.1.20", Port: 32500}, {Address: address, Port: 40123}}
	}
	sent := func() int { return p.fake.Count("PUT", "/document") }
	held := func() string {
		addresses, _ := p.fake.Document(p.s.id)["addresses"].([]any)
		if len(addresses) != 2 {
			t.Fatalf("Portico Services holds the addresses %v, want the home one and the public one", addresses)
		}
		public, _ := addresses[1].(map[string]any)
		if public["port"] != float64(40123) {
			t.Fatalf("the public address was sent as %v, want it with its own port", public)
		}
		return public["address"].(string)
	}
	p.link(accountJustin)
	if sent() != 1 || held() != address {
		t.Fatalf("the first document: %d sent, holding %s", sent(), held())
	}

	// An hour of an address that flips every minute.
	for minute := 1; minute <= 60; minute++ {
		address = "203.0.113." + itoa(minute+1)
		p.clock.Add(time.Minute)
		p.step()
	}
	if flips := sent() - 1; flips < 10 || flips > 12 {
		t.Errorf("an hour of an address changing every minute sent %d documents, want one every five minutes", flips)
	}
	// It settles: within the gap Portico Services holds the address that is true now.
	p.run(addressGap)
	if held() != address {
		t.Errorf("after the address settled Portico Services holds %s, want %s", held(), address)
	}
	settled := sent()
	p.run(time.Hour)
	if sent() != settled {
		t.Errorf("an address that stopped changing sent %d more documents", sent()-settled)
	}

	// Inside the gap, a person joining and a change the owner makes by hand do not wait.
	address = "198.51.100.7"
	p.clock.Add(time.Minute)
	p.step() // more than the gap since the last document: goes at once and starts a new gap
	if sent() != settled+1 {
		t.Fatalf("a changed address long after the last document sent %d documents, want one at once", sent()-settled)
	}
	address = "198.51.100.8"
	p.clock.Add(time.Minute)
	p.step()
	if sent() != settled+1 {
		t.Fatal("a second change of address a minute later did not wait")
	}
	p.owner = onMain(t, p.s, signIn(t, p.s, 7))
	if code, _, _ := p.signIn(accountSam, p.invite(), 1); code != 200 {
		t.Fatal("sam could not join")
	}
	p.step()
	if _, members := p.fake.Linked(p.s.id); sent() != settled+2 || members[accountSam.ID] != "member" || held() != address {
		t.Errorf("a person joining inside the gap: %d documents, members %v", sent()-settled, members)
	}
	// A change the owner makes by hand does not wait out the addresses' gap either, but no
	// two documents of any kind go within a minute of each other, and the one that goes
	// then is the one that is true then.
	address = "198.51.100.9"
	p.s.porticoChangedByOwner()
	p.step()
	address = "198.51.100.10"
	p.s.porticoChangedByOwner()
	p.step()
	if sent() != settled+2 {
		t.Errorf("two changes by hand straight after a document sent %d more documents at once", sent()-settled-2)
	}
	p.run(documentGap)
	if sent() != settled+3 || held() != address {
		t.Errorf("a change the owner made by hand inside the gap sent %d documents, holding %s", sent()-settled, held())
	}
}

// A page on another site can make a browser send a POST with no body, or with one that is not
// JSON, without asking the server first. On a linked server the challenge route, which takes
// nothing, refuses each of those; an app's JSON request gets its challenge.
func TestAChallengeIsNotGivenToARequestAPageCanSendUnasked(t *testing.T) {
	p := newPorticoRig(t)
	p.link(accountJustin)
	for name, kind := range map[string]string{"no body": "", "a form": "application/x-www-form-urlencoded", "JSON as text": "text/plain",
		"an upload form": "multipart/form-data; boundary=x"} {
		r := httptest.NewRequest("POST", "/v1/auth/portico/challenge", strings.NewReader(""))
		if kind != "" {
			r.Header.Set("Content-Type", kind)
		}
		r.Header.Set("Origin", "https://evil.example")
		if w := serve(p.s, r); w.Code != 415 {
			t.Errorf("a challenge asked for with %s answered %d, want 415", name, w.Code)
		}
	}
	if p.challenge() == "" {
		t.Error("an app's request got no challenge")
	}
}
