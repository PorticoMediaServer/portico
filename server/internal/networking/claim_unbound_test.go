package networking

import (
	"errors"
	"testing"
	"time"
)

// Web approval binds the account (Plex-style): a claim prepared without one
// adopts the approver's account from the signed approval, and only then.
func TestUnboundClaimAdoptsTheApproversAccount(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := fixtureSQL(t)
	ctx := fixtureClaimContext(t)
	unbound := f.binding
	unbound.AccountID = UnboundAccount
	v, e := f.store.Prepare(ctx, unbound, f.owner)
	if e != nil {
		t.Fatal(e)
	}
	// An approval for a different server key is refused even though the account is open.
	other := f.binding
	other.ServerID = "server_two"
	if _, e = f.store.Approve(ctx, v, f.approval(t, other, 1, f.now, f.now.Add(5*time.Minute))); e == nil {
		t.Fatal("approval for another server bound this claim")
	}
	approved, e := f.store.Approve(ctx, v, f.approval(t, f.binding, 1, f.now, f.now.Add(5*time.Minute)))
	if e != nil {
		t.Fatal(e)
	}
	if approved.AccountID != f.binding.AccountID || approved.Stage != Approved {
		t.Fatalf("account %q stage %v", approved.AccountID, approved.Stage)
	}
	// Once bound, a different account's approval can never rebind it.
	thief := f.binding
	thief.AccountID = "someone_else"
	if _, e = f.store.Approve(ctx, approved, f.approval(t, thief, 2, f.now, f.now.Add(5*time.Minute))); e == nil || errors.Is(e, nil) {
		t.Fatal("a bound claim was rebound")
	}
}
