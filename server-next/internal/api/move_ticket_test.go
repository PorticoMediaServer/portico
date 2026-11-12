package api

import (
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/hosted"
)

// switchedToPortico is a server just switched to Portico Accounts with sam and alex waiting.
func switchedToPortico(t *testing.T) (*Server, *fakeHosted) {
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
	fake.decision = hosted.Decision{Purpose: hosted.PurposeSwitch, Account: hosted.Account{ID: "pa_1", Username: "justin"}}
	body := SignInSwitchRequest{To: "portico", CurrentPassword: testPassword, Approval: "approved-for-" + s.id, Device: device(1)}
	if w := call(t, s, "POST", "/v1/admin/sign-in/switch", owner, body, nil); w.Code != 200 {
		t.Fatalf("switching to Portico Accounts answered %d: %s", w.Code, w.Body)
	}
	return s, fake
}

func moveTicket(t *testing.T, s *Server, username string) string {
	t.Helper()
	var check MoveCheck
	if w := call(t, s, "POST", "/v1/auth/move/check", "", MoveCheckRequest{Username: username, Password: testPassword}, &check); w.Code != 200 || check.Ticket == "" {
		t.Fatalf("checking %s's old password answered %d: %s", username, w.Code, w.Body)
	}
	return check.Ticket
}

func withTicket(t *testing.T, s *Server, ticket string) MoveCompleteRequest {
	t.Helper()
	req := MoveCompleteRequest{Ticket: ticket, Device: device(7)}
	req.Challenge, req.Identity = porticoProof(t, s)
	return req
}

// The old password proves a waiting person on the server's own page; the ticket it answers lets
// the move finish where their Portico Account is, without the password leaving that page.
func TestAMoveTicketStandsInForTheOldPasswordOnceForItsOwnAccount(t *testing.T) {
	s, fake := switchedToPortico(t)

	// The server says when the old password stops moving accounts.
	var system System
	call(t, s, "GET", "/v1/system", "", nil, &system)
	if system.Move != "to_portico" || system.MoveEndsAt == nil || system.MoveEndsAt.Sub(time.Now().Add(db.OldPasswordWorks)).Abs() > time.Minute {
		t.Fatalf("the system answer says move %q ends %v", system.Move, system.MoveEndsAt)
	}

	// Only a right old password earns a ticket, and it names nobody.
	ticket := moveTicket(t, s, "sam")
	if len(ticket) < 32 || strings.Contains(ticket, "sam") {
		t.Fatalf("the ticket %q is not a random secret", ticket)
	}

	// A ticket from another server is not one of this server's.
	other, _ := switchedToPortico(t)
	foreign := moveTicket(t, other, "sam")
	fake.decision = hosted.Decision{Account: hosted.Account{ID: "pa_2", Username: "sammy"}}
	if code := status(t, s, "POST", "/v1/auth/move/complete", "", withTicket(t, s, foreign)); code != 401 {
		t.Fatalf("another server's ticket answered %d, want 401", code)
	}

	// The ticket moves exactly the account whose password made it, and only once.
	var moved SignInResponse
	if w := call(t, s, "POST", "/v1/auth/move/complete", "", withTicket(t, s, ticket), &moved); w.Code != 200 || moved.Account.Username != "sammy" {
		t.Fatalf("moving with the ticket answered %d: %s", w.Code, w.Body)
	}
	if code := status(t, s, "POST", "/v1/auth/move/complete", "", withTicket(t, s, ticket)); code != 401 {
		t.Fatalf("a used ticket answered %d, want 401", code)
	}
	// Alex's own ticket moves alex, never sam (already moved) or anyone else.
	alex := moveTicket(t, s, "alex")
	fake.decision = hosted.Decision{Account: hosted.Account{ID: "pa_3", Username: "alexandra"}}
	if w := call(t, s, "POST", "/v1/auth/move/complete", "", withTicket(t, s, alex), &moved); w.Code != 200 || moved.Account.Username != "alexandra" {
		t.Fatalf("alex's ticket answered %d: %s", w.Code, w.Body)
	}
}

// A ticket works once whether the move finishes or the note is refused, and not past its minutes.
func TestAMoveTicketIsSpentByAFailedNoteAndEndsInMinutes(t *testing.T) {
	s, fake := switchedToPortico(t)
	fake.decision = hosted.Decision{Account: hosted.Account{ID: "pa_2", Username: "sammy"}}

	// Offered without a note: refused, and spent.
	ticket := moveTicket(t, s, "sam")
	noNote := withTicket(t, s, ticket)
	noNote.Identity = nil
	if code := status(t, s, "POST", "/v1/auth/move/complete", "", noNote); code < 400 || code >= 500 {
		t.Fatalf("a ticket with no note answered %d", code)
	}
	if code := status(t, s, "POST", "/v1/auth/move/complete", "", withTicket(t, s, ticket)); code != 401 {
		t.Fatalf("a ticket after a refused note answered %d, want 401: it works once", code)
	}

	// A note Portico Services refuses spends it too.
	refused := moveTicket(t, s, "sam")
	fake.decision = hosted.Decision{}
	if code := status(t, s, "POST", "/v1/auth/move/complete", "", withTicket(t, s, refused)); code < 400 || code >= 500 {
		t.Fatalf("a refused note answered %d", code)
	}
	fake.decision = hosted.Decision{Account: hosted.Account{ID: "pa_2", Username: "sammy"}}
	if code := status(t, s, "POST", "/v1/auth/move/complete", "", withTicket(t, s, refused)); code != 401 {
		t.Fatalf("a ticket after Services refused the note answered %d, want 401", code)
	}

	// Five minutes on, a fresh ticket has ended.
	late := moveTicket(t, s, "sam")
	start := time.Now()
	s.setClock(func() time.Time { return start.Add(moveTicketLasts + time.Second) })
	if code := status(t, s, "POST", "/v1/auth/move/complete", "", withTicket(t, s, late)); code != 401 {
		t.Fatalf("an expired ticket answered %d, want 401", code)
	}
	// Made a minute before the old password stops moving accounts, a ticket ends with it.
	s.setClock(func() time.Time { return start.Add(db.OldPasswordWorks - time.Minute) })
	edge := moveTicket(t, s, "sam")
	s.setClock(func() time.Time { return start.Add(db.OldPasswordWorks + time.Minute) })
	if code := status(t, s, "POST", "/v1/auth/move/complete", "", withTicket(t, s, edge)); code != 401 {
		t.Fatalf("a ticket used past the move's end answered %d, want 401", code)
	}
	s.setClock(time.Now)
	if got := memberStateByName(t, s, "sam"); got != "waiting" {
		t.Fatalf("after only refused tickets sam is %q, want waiting", got)
	}
}

// memberStateByName reads a member's state as stored.
func memberStateByName(t *testing.T, s *Server, username string) string {
	t.Helper()
	creds, err := s.db.CredentialsByUsername(t.Context(), username)
	if err != nil {
		t.Fatalf("reading %s: %v", username, err)
	}
	account, err := s.db.Account(t.Context(), creds.UserID)
	if err != nil {
		t.Fatalf("reading %s: %v", username, err)
	}
	return account.State
}

// The page finishing a move asks the server whose ticket it holds before asking anything of the
// person, so a link can never put words in its mouth; asking spends nothing.
func TestAMoveTicketNamesItsAccountWithoutBeingSpent(t *testing.T) {
	s, fake := switchedToPortico(t)
	fake.decision = hosted.Decision{Account: hosted.Account{ID: "pa_2", Username: "sammy"}}
	ticket := moveTicket(t, s, "sam")
	var peek MoveTicket
	if w := call(t, s, "POST", "/v1/auth/move/ticket", "", MoveTicketRequest{Ticket: ticket}, &peek); w.Code != 200 || peek.Username != "sam" ||
		peek.ExpiresAt.Sub(time.Now().Add(moveTicketLasts)).Abs() > time.Minute {
		t.Fatalf("peeking at sam's ticket answered %d: %s", w.Code, w.Body)
	}
	if body := call(t, s, "POST", "/v1/auth/move/ticket", "", MoveTicketRequest{Ticket: ticket}, nil).Body.String(); strings.Contains(body, "acc_") || strings.Contains(body, "role") {
		t.Fatalf("the peek says more than the username and its end: %s", body)
	}
	// Alex's ticket names alex, whoever holds it.
	alex := moveTicket(t, s, "alex")
	call(t, s, "POST", "/v1/auth/move/ticket", "", MoveTicketRequest{Ticket: alex}, &peek)
	if peek.Username != "alex" {
		t.Fatalf("alex's ticket was named %q", peek.Username)
	}
	// Peeking spent nothing: the ticket still finishes the move.
	if w := call(t, s, "POST", "/v1/auth/move/complete", "", withTicket(t, s, ticket), nil); w.Code != 200 {
		t.Fatalf("moving after a peek answered %d: %s", w.Code, w.Body)
	}
	// A wrong or used ticket is a wrong guess, and says nothing.
	for _, bad := range []string{"", "not-a-ticket", ticket} {
		if code := status(t, s, "POST", "/v1/auth/move/ticket", "", MoveTicketRequest{Ticket: bad}); code != 401 {
			t.Fatalf("peeking at %q answered %d, want 401", bad, code)
		}
	}
}
