package hosted_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/hosted"
	"github.com/PorticoMediaServer/portico/server-next/internal/hosted/hostedtest"
	"github.com/PorticoMediaServer/portico/server-next/internal/hosted/trust"
)

type rig struct {
	fake   *hostedtest.Services
	client *hosted.Client
	id     string
	// now is Portico Services' clock; the server's is slow by this much.
	now  time.Time
	slow time.Duration
}

func newRig(t *testing.T) *rig {
	r := &rig{now: time.Now().UTC().Truncate(time.Second)}
	r.fake = hostedtest.New(t)
	r.fake.Now = func() time.Time { return r.now }
	public, key, _ := ed25519.GenerateKey(rand.Reader)
	r.id, _ = trust.ServerID(public)
	r.client = hosted.New(hosted.Options{Origin: r.fake.URL, HTTP: r.fake.Client(), ServerID: r.id, Key: key, Root: r.fake.Root(),
		Now: func() time.Time { return r.now.Add(-r.slow) }})
	return r
}

var owner = hostedtest.Account{ID: "acct_1", Username: "justin", DisplayName: "Justin"}

func (r *rig) link(t *testing.T) hosted.Decision {
	t.Helper()
	ctx := context.Background()
	approval, err := r.client.StartApproval(ctx, hosted.ApprovalRequest{ServerID: r.id, ServerName: "Cabin", Purpose: hosted.PurposeSetup,
		ReturnURL: "https://cabin.example/setup"})
	if err != nil {
		t.Fatal(err)
	}
	decision, err := r.client.FinishApproval(ctx, r.id, r.fake.Decide(approval.ID, owner, true))
	if err != nil {
		t.Fatal(err)
	}
	return decision
}

func document(revision int64) hosted.Document {
	return hosted.Document{Revision: revision, Name: "Cabin", OwnerAccountID: owner.ID, Members: []hosted.Member{{AccountID: owner.ID, Role: "owner"}}}
}

// Every request is signed with the server's own key and Portico Services checks each one.
// A server whose clock is slow signs with the time of its approval until its own clock has
// passed it, so its first calls are not taken for copies of old ones.
func TestRequestsAreSignedAndASlowClockStillLinks(t *testing.T) {
	r := newRig(t)
	r.slow = 3 * time.Minute
	ctx := context.Background()
	decision := r.link(t)
	if decision.Account.ID != owner.ID || decision.ServerName != "Cabin" || decision.Purpose != hosted.PurposeSetup || !decision.IssuedAt.Equal(r.now) {
		t.Fatalf("unexpected decision: %+v", decision)
	}
	// Signed with the server's own slow time, the document is refused as older than the link.
	if _, err := r.client.SendDocument(ctx, time.Time{}, document(1)); err == nil {
		t.Error("a request signed before the link was made was accepted")
	}
	stored, err := r.client.SendDocument(ctx, decision.IssuedAt, document(1))
	if err != nil || stored.Revision != 1 {
		t.Fatalf("the first document of a server with a slow clock: %+v %v", stored, err)
	}
	if _, err := r.client.CheckIn(ctx, decision.IssuedAt, hosted.CheckIn{DocumentRevision: 1, NoteRevision: decision.IssuedAt.Unix()}); err != nil {
		t.Errorf("the check-in of a server with a slow clock: %v", err)
	}

	// Another server's key cannot speak for this one.
	_, strangerKey, _ := ed25519.GenerateKey(rand.Reader)
	stranger := hosted.New(hosted.Options{Origin: r.fake.URL, HTTP: r.fake.Client(), ServerID: r.id, Key: strangerKey, Root: r.fake.Root(),
		Now: func() time.Time { return r.now }})
	if _, err := stranger.SendDocument(ctx, decision.IssuedAt, document(2)); err == nil {
		t.Error("a document signed by another key was accepted")
	}
	if err := stranger.Leave(ctx, decision.IssuedAt); err == nil {
		t.Error("a leave signed by another key was accepted")
	}
	if linked, _ := r.fake.Linked(r.id); !linked {
		t.Fatal("another key removed the server")
	}

	// A leave written for an earlier link does not remove this one; the link's own does.
	if err := r.client.Leave(ctx, decision.IssuedAt.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if linked, _ := r.fake.Linked(r.id); !linked {
		t.Error("a leave for an earlier link removed the present one")
	}
	if err := r.client.Leave(ctx, decision.IssuedAt); err != nil {
		t.Fatal(err)
	}
	if linked, _ := r.fake.Linked(r.id); linked {
		t.Error("the server is still linked after it left")
	}
	// Portico Services no longer knowing the server is also "done".
	if err := r.client.Leave(ctx, decision.IssuedAt); err != nil {
		t.Errorf("leaving again answered %v", err)
	}
	if _, err := r.client.CheckIn(ctx, decision.IssuedAt, hosted.CheckIn{}); !errors.Is(err, hosted.ErrUnknownServer) {
		t.Errorf("a check-in by a server that has left answered %v, want ErrUnknownServer", err)
	}
}

// The three ways asking for an approval fails are told apart, because the setup page says
// something different for each.
func TestTheFailuresOfAskingForAnApproval(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	ask := func() error {
		_, err := r.client.StartApproval(ctx, hosted.ApprovalRequest{ServerID: r.id, ServerName: "Cabin", Purpose: hosted.PurposeSetup,
			ReturnURL: "https://cabin.example/setup"})
		return err
	}
	r.fake.Down(true)
	if err := ask(); !errors.Is(err, hosted.ErrNotConnected) {
		t.Errorf("with Portico Services away: %v, want ErrNotConnected", err)
	}
	r.fake.Down(false)
	r.fake.Busy(90 * time.Second)
	var busy *hosted.BusyError
	if err := ask(); !errors.As(err, &busy) || busy.Wait != 90*time.Second {
		t.Errorf("with Portico Services busy: %v, want a wait of 90 seconds", err)
	}
	r.fake.Busy(0)
	r.slow = 3 * time.Hour
	var clock *hosted.ClockError
	if err := ask(); !errors.As(err, &clock) || clock.Off != -3*time.Hour {
		t.Errorf("with the server's clock three hours slow: %v", err)
	}
	r.slow = 0

	// A declined approval, and one that was given, as a poll sees them.
	approval, err := r.client.StartApproval(ctx, hosted.ApprovalRequest{ServerID: r.id, ServerName: "Cabin", Purpose: hosted.PurposeSetup,
		ReturnURL: "https://cabin.example/setup"})
	if err != nil {
		t.Fatal(err)
	}
	if returned, err := r.client.PollApproval(ctx, approval.ID, approval.PollToken); err != nil || returned != "" {
		t.Errorf("a waiting approval polled as %q %v", returned, err)
	}
	r.fake.Decide(approval.ID, owner, false)
	r.now = r.now.Add(approval.PollAfter)
	if _, err := r.client.PollApproval(ctx, approval.ID, approval.PollToken); !errors.Is(err, hosted.ErrDeclined) {
		t.Errorf("a declined approval polled as %v, want ErrDeclined", err)
	}
}
