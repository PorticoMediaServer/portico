package api

// Tests written by the security review of this area (public #2520), kept as it wrote them.

import (
	"context"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/hosted"
)

func TestReviewTheCertificateClientIsToldTheLinkAndItsEnd(t *testing.T) {
	p := newPorticoRig(t)
	var linked []time.Time
	unlinked := 0
	hook := func() {
		p.s.link.onLinked = func(at time.Time) { linked = append(linked, at) }
		p.s.link.onUnlinked = func() { unlinked++ }
	}
	hook()
	p.link(accountJustin)
	link, _, _ := p.s.db.Link(context.Background())
	if len(linked) != 1 || linked[0].Unix() != link.IssuedAt {
		t.Fatalf("after linking, the certificate client was told %v, want once with the link's issue time %d", linked, link.IssuedAt)
	}

	// The owner removes the server from their account: the certificate client must stop too.
	p.fake.Unlink(p.s.id)
	if code, _ := p.notice(p.fake.Note(p.s.id)); code != 200 {
		t.Fatal("the note was not taken")
	}
	p.step()
	if unlinked != 1 {
		t.Errorf("after the link ended, the certificate client was told to stop %d times, want once", unlinked)
	}

	// A restart of a server whose link has ended tells it nothing.
	p.open()
	linked, unlinked = nil, 0
	hook()
	p.step()
	if len(linked) != 0 {
		t.Error("a server whose link has ended told the certificate client it is linked")
	}

	// Linking again tells it the new link's time.
	p.clock.Add(time.Minute)
	decision := p.approve(accountJustin, hosted.PurposeSwitch)
	if err := p.s.db.LinkServer(context.Background(), db.NewLink{OwnerID: 1, AccountID: decision.Account.ID, IssuedAt: decision.IssuedAt, Now: p.clock.Now()}); err != nil {
		t.Fatal(err)
	}
	p.step()
	if len(linked) != 1 || !linked[0].Equal(time.Unix(decision.IssuedAt.Unix(), 0)) {
		t.Errorf("after linking again, the certificate client was told %v, want the new link's issue time", linked)
	}
}
