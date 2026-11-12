package api

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/backup"
)

// Review desk A, #2891. The approval that proves a restore also makes a new link, at Portico
// Services and on the server. The restore then puts back a database that holds the link from
// before. After the restart the server must still hold the link the approval made: it is the
// one Portico Services holds, it is what "this server has left" must name to be acted on, and
// it is what makes the approval good only once.
func TestReviewAAfterARestoreThePorticoServerHoldsTheLinkItsApprovalMade(t *testing.T) {
	p := newPorticoRig(t)
	p.link(accountJustin)
	p.run(documentGap)
	var started BackupStarted
	call(t, p.s, "POST", "/v1/admin/backups", p.owner.AccessToken, nil, &started)
	for p.s.Backups().Running() != nil {
		time.Sleep(10 * time.Millisecond)
	}
	p.clock.Add(time.Minute)
	var approval PorticoApproval
	if w := call(t, p.s, "POST", "/v1/admin/portico/link", p.owner.AccessToken, PorticoLinkRequest{ReturnURL: "https://cabin.example/back"}, &approval); w.Code != 200 {
		t.Fatalf("starting the approval answered %d: %s", w.Code, w.Body)
	}
	mine := p.fake.Decide(approval.ApprovalID, accountJustin, true)
	if code := call(t, p.s, "POST", "/v1/admin/backups/restore", p.owner.AccessToken,
		BackupRestore{BackupInspect: BackupInspect{ID: started.ID}, Approval: mine}, nil).Code; code != 200 {
		t.Fatalf("the restore answered %d", code)
	}
	approved, _, _ := p.s.db.Link(t.Context())

	// The restart: the staged restore is made before the database opens, as main does.
	p.restart()

	restored, linked, _ := p.s.db.Link(t.Context())
	if !linked || restored.IssuedAt != approved.IssuedAt {
		t.Errorf("after the restore the server holds the link issued at %d; the approval, and Portico Services, made the one issued at %d",
			restored.IssuedAt, approved.IssuedAt)
	}
	// Portico Services made the server's row new when the approval was given: it lists the
	// server again only once it has the new link's first document.
	p.run(10 * time.Minute)
	if listed, _ := p.fake.Linked(p.s.id); !listed {
		t.Error("ten minutes after the restore Portico Services still has no document from this server, so it is in nobody's list of servers")
	}
	// The same approval proves nothing a second time: it is no newer than the link.
	p.owner = onMain(t, p.s, signIn(t, p.s, 9))
	if code := call(t, p.s, "POST", "/v1/admin/backups/restore", p.owner.AccessToken,
		BackupRestore{BackupInspect: BackupInspect{ID: started.ID}, Approval: mine}, nil).Code; code == 200 {
		t.Error("after the restart the same approval staged a second restore")
	}
}

// restart stops the rig's server and starts it again the way main does: a staged restore is
// made before the database opens.
func (p *porticoRig) restart() {
	p.Helper()
	closeDB(p.s.db)
	p.s = nil
	d, err := backup.OpenData(context.Background(), filepath.Dir(p.path), slog.New(slog.NewTextHandler(io.Discard, nil)), p.clock.Now)
	if err != nil {
		p.Fatal(err)
	}
	d.Close()
	p.clock.Add(time.Minute)
	p.open()
}

// A backup from before the server used Portico Accounts puts back a server with local
// sign-in. It has no link, and Portico Services is told once that the server has left, naming
// the link the approval made, so the server goes from people's lists as after a switch.
func TestARestoreToBeforePorticoAccountsTellsPorticoServicesTheServerLeft(t *testing.T) {
	p := newPorticoRig(t)
	var started BackupStarted
	call(t, p.s, "POST", "/v1/admin/backups", p.owner.AccessToken, nil, &started)
	for p.s.Backups().Running() != nil {
		time.Sleep(10 * time.Millisecond)
	}
	p.link(accountJustin)
	p.run(documentGap)
	if listed, _ := p.fake.Linked(p.s.id); !listed {
		t.Fatal("the linked server is not listed at Portico Services")
	}
	var approval PorticoApproval
	if w := call(t, p.s, "POST", "/v1/admin/portico/link", p.owner.AccessToken, PorticoLinkRequest{ReturnURL: "https://cabin.example/back"}, &approval); w.Code != 200 {
		t.Fatalf("starting the approval answered %d: %s", w.Code, w.Body)
	}
	if code := call(t, p.s, "POST", "/v1/admin/backups/restore", p.owner.AccessToken, BackupRestore{BackupInspect: BackupInspect{ID: started.ID},
		Approval: p.fake.Decide(approval.ApprovalID, accountJustin, true)}, nil).Code; code != 200 {
		t.Fatalf("the restore answered %d", code)
	}
	approved, _, _ := p.s.db.Link(t.Context())
	p.restart()

	if _, linked, _ := p.s.db.Link(t.Context()); linked {
		t.Fatal("a server restored to before it used Portico Accounts is linked")
	}
	if leave, queued, _ := p.s.db.Leave(t.Context()); !queued || leave.LinkIssuedAt != approved.IssuedAt {
		t.Fatalf("the queued leave is %+v (queued %v), want one naming the link issued at %d", leave, queued, approved.IssuedAt)
	}
	p.run(10 * time.Minute)
	if _, queued, _ := p.s.db.Leave(t.Context()); queued {
		t.Error("ten minutes after the restore the leave is still waiting to be sent")
	}
	if p.fake.Count("POST", "/leave") != 1 {
		t.Errorf("Portico Services was told %d times that the server left, want once", p.fake.Count("POST", "/leave"))
	}
	// The owner signs in with the password the backup holds.
	if code := status(t, p.s, "POST", "/v1/auth/sign-in", "", SignInRequest{Username: "justin", Password: testPassword, Device: device(9)}); code != 200 {
		t.Errorf("the owner's password from the backup answered %d", code)
	}
}
