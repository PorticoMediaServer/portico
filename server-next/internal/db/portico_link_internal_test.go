package db

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// The rules of the link as setup and a switch use them, inside their own transactions: a new
// link's numbers start at its approval's issue time; only the owning account makes another;
// an approval no newer than a link already made, or already left, is refused; a new link
// cancels a queued leave; a change of owner moves no number; and leaving queues the one
// message only for a server that was linked.
func TestTheLinkIsMadeEndedAndChangedInsideOtherTransactions(t *testing.T) {
	ctx := context.Background()
	d, err := Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	now := time.Unix(1_800_000_000, 0)
	in := func(fn func(tx *sql.Tx) error) error { return d.write(ctx, User, fn) }
	link := func(account string, issued time.Time) error {
		return in(func(tx *sql.Tx) error { return linkServer(tx, account, issued, now) })
	}

	// A server that was never linked queues nothing when it "leaves".
	if err := in(func(tx *sql.Tx) error { return unlinkServer(tx, now.Add(time.Hour), now) }); err != nil {
		t.Fatal(err)
	}
	if _, leaving, _ := d.Leave(ctx); leaving {
		t.Fatal("a server that was never linked queued a leave: a local server must never contact Portico Services")
	}

	if err := link("acct_a", now); err != nil {
		t.Fatal(err)
	}
	l, linked, _ := d.Link(ctx)
	if !linked || l.AccountID != "acct_a" || l.IssuedAt != now.Unix() || l.NoteRevision != now.Unix() || l.DocumentSent != 0 {
		t.Fatalf("unexpected first link: %+v", l)
	}
	if err := link("acct_b", now.Add(time.Minute)); !errors.Is(err, ErrNotLinkOwner) {
		t.Errorf("an approval from another account answered %v, want ErrNotLinkOwner", err)
	}
	if err := link("acct_a", now); !errors.Is(err, ErrStaleApproval) {
		t.Errorf("the same approval again answered %v, want ErrStaleApproval", err)
	}
	if err := link("acct_a", now.Add(-time.Minute)); !errors.Is(err, ErrStaleApproval) {
		t.Errorf("an older approval answered %v, want ErrStaleApproval", err)
	}

	// The note numbers move up; a change of owner then leaves them where they are.
	if _, changed, err := d.ApplyNote(ctx, Note{Revision: now.Unix() + 50, Fleet: 7}, now); err != nil || !changed {
		t.Fatal(err)
	}
	if err := in(func(tx *sql.Tx) error { return setLinkOwner(tx, "acct_b", now) }); err != nil {
		t.Fatal(err)
	}
	if l, _, _ = d.Link(ctx); l.AccountID != "acct_b" || l.NoteRevision != now.Unix()+50 || l.IssuedAt != now.Unix() {
		t.Errorf("a change of owner moved the link's numbers: %+v", l)
	}

	// Leaving queues the one message, naming the link that was left.
	giveUp := now.Add(7 * 24 * time.Hour)
	if err := in(func(tx *sql.Tx) error { return unlinkServer(tx, giveUp, now) }); err != nil {
		t.Fatal(err)
	}
	leave, leaving, _ := d.Leave(ctx)
	if _, linked, _ := d.Link(ctx); linked || !leaving || leave.LinkIssuedAt != now.Unix() || leave.GiveUpAt != giveUp.Unix() {
		t.Fatalf("after leaving: linked %v, leave %+v", linked, leave)
	}
	// An approval from before the link that was left cannot make a link now.
	if err := link("acct_c", now.Add(-time.Second)); !errors.Is(err, ErrStaleApproval) {
		t.Errorf("an approval older than the link just left answered %v, want ErrStaleApproval", err)
	}
	// Linking again, to any account now, cancels the queued leave: sent later it would
	// otherwise be the server's own word against its new link.
	if err := link("acct_c", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, leaving, _ := d.Leave(ctx); leaving {
		t.Error("linking again did not cancel the queued leave")
	}
	if l, _, _ = d.Link(ctx); l.NoteRevision != now.Add(time.Hour).Unix() {
		t.Errorf("the new link's numbers start at %d, want its approval's issue time", l.NoteRevision)
	}
}
