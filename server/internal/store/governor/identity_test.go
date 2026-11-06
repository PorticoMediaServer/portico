package governor

import "testing"

type identityWrapper struct{ Governor }

func TestCompositionIdentityChecksAreExactAndNilSafe(t *testing.T) {
	g, a, _ := setup(t)
	other, b, _ := setup(t)
	var nilGovernor Governor
	var typedNil *ledger
	for _, value := range []Governor{nilGovernor, typedNil, other, identityWrapper{g}} {
		if a.Owns(value) {
			t.Fatal("foreign/nil/wrapped governor accepted")
		}
	}
	if !a.Owns(g) || (*Authority)(nil).Owns(g) || (&Authority{}).Owns(typedNil) {
		t.Fatal("authority identity not nil/zero safe")
	}
	s := &Snapshots{governor: g, authority: a}
	if !s.OwnedBy(a) || s.OwnedBy(b) || s.OwnedBy(nil) || (*Snapshots)(nil).OwnedBy(a) || (&Snapshots{}).OwnedBy(a) || (&Snapshots{}).OwnedBy(nil) {
		t.Fatal("pool authority identity not exact/nil safe")
	}
	// A different authority pointer to the same internal ledger is not the
	// capability supplied at pool construction.
	if s.OwnedBy(&Authority{ledger: a.ledger}) {
		t.Fatal("equivalent ledger replaced exact pool authority")
	}
	a.EnterStatic()
	if !a.Owns(g) || !s.OwnedBy(a) {
		t.Fatal("identity checks confused epoch with ownership")
	}
}
