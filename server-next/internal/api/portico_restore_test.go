package api

import (
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/hosted/hostedtest"
)

// The owner of a Portico Accounts server has no password, so a restore is proven by a fresh
// approval from the Portico Account that owns the server, which works once. Whose approval
// counts is the link's own rule (TestOnlyTheOwningAccountCanApproveALinkedServer).
func TestARestoreOnAPorticoServerNeedsTheOwningAccountsApproval(t *testing.T) {
	p := newPorticoRig(t)
	p.link(accountJustin)
	var started BackupStarted
	call(t, p.s, "POST", "/v1/admin/backups", p.owner.AccessToken, nil, &started)
	for p.s.Backups().Running() != nil {
		time.Sleep(10 * time.Millisecond)
	}
	p.clock.Add(time.Minute)
	approved := func(by hostedtest.Account) string {
		t.Helper()
		var approval PorticoApproval
		if w := call(t, p.s, "POST", "/v1/admin/portico/link", p.owner.AccessToken, PorticoLinkRequest{ReturnURL: "https://cabin.example/back"}, &approval); w.Code != 200 {
			t.Fatalf("starting the approval answered %d: %s", w.Code, w.Body)
		}
		return p.fake.Decide(approval.ApprovalID, by, true)
	}
	restore := func(approval string) (int, string) {
		w := call(t, p.s, "POST", "/v1/admin/backups/restore", p.owner.AccessToken,
			BackupRestore{BackupInspect: BackupInspect{ID: started.ID}, CurrentPassword: testPassword, Approval: approval}, nil)
		return w.Code, refusalCode(w)
	}
	if code, refused := restore(""); code != 403 || refused != "portico_approval_required" {
		t.Errorf("a restore with no approval answered %d %s", code, refused)
	}
	mine := approved(accountJustin)
	if code, refused := restore(mine); code != 200 {
		t.Fatalf("a restore approved by the owning account answered %d %s", code, refused)
	}
	if code, _ := restore(mine); code == 200 {
		t.Error("the same approval staged a second restore")
	}
	if link, linked, _ := p.s.db.Link(t.Context()); !linked || link.AccountID != accountJustin.ID {
		t.Errorf("after the restore's approval the link is %+v", link)
	}
}
