package usermutation

// P2-62 receipt lifetime binding tests: the exact profile lifetime is captured
// server-side, a reused profile id under a new lifetime never replays an old
// result, and an optional mutation root must still be the live owner.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func profileLifetime(t *testing.T, db *sql.DB, account, profile string) string {
	t.Helper()
	var lifetime string
	if err := db.QueryRow(`SELECT lifetime FROM direct_profiles WHERE account_id=? AND id=?`, account, profile).Scan(&lifetime); err != nil {
		t.Fatalf("profile lifetime: %v", err)
	}
	return lifetime
}

func rotateProfileLifetime(t *testing.T, db *sql.DB, account, profile string) string {
	t.Helper()
	if _, err := db.Exec(`UPDATE direct_profiles SET lifetime=lower(hex(randomblob(16))) WHERE account_id=? AND id=?`, account, profile); err != nil {
		t.Fatalf("rotate profile lifetime: %v", err)
	}
	return profileLifetime(t, db, account, profile)
}

func membershipIncarnation(t *testing.T, db *sql.DB, account string) string {
	t.Helper()
	var incarnation string
	if err := db.QueryRow(`SELECT incarnation FROM direct_memberships WHERE account_id=?`, account).Scan(&incarnation); err != nil {
		t.Fatalf("membership incarnation: %v", err)
	}
	return incarnation
}

// mutationRoot writes the owner row a checked registration would have written,
// bound to the lifetimes the store resolves. Test shorthand only: roots are
// created by identity.RegisterMutationRootTx in production.
func mutationRoot(t *testing.T, db *sql.DB, b Binding, installation string, revision int) string {
	t.Helper()
	id := fmt.Sprintf("R%021d", revision)
	// The owner must name a genuinely enrolled, live installation lifetime.
	if _, err := db.Exec(`INSERT INTO identity_installations(installation_hash,incarnation) VALUES(?,?) ON CONFLICT(incarnation) DO NOTHING`, fmt.Sprintf("%064x", revision), installation); err != nil {
		t.Fatal(err)
	}
	_, err := db.Exec(`INSERT INTO user_mutation_roots(id,installation_incarnation,account_id,profile_id,account_incarnation,profile_incarnation,family_id,registration_revision,state) VALUES(?,?,?,?,?,?,?,?,'active')`,
		id, installation, b.AccountID, b.ProfileID, membershipIncarnation(t, db, b.AccountID), profileLifetime(t, db, b.AccountID, b.ProfileID), b.FamilyID, revision)
	if err != nil {
		t.Fatalf("create mutation root: %v", err)
	}
	return id
}

func TestRootlessReceiptCapturesProfileLifetimeAndNeverReplaysAcrossRotation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db := fixture(t)
	first := profileLifetime(t, db, "account", "profile")
	e := envelope(700)
	if err := apply(t, db, testBinding, e, Result{Status: "applied", Revision: 1}, testNow); err != nil {
		t.Fatal(err)
	}
	var stored, installation, root string
	if err := db.QueryRow(`SELECT profile_incarnation,installation_incarnation,root_id FROM user_mutation_receipts WHERE key=?`, e.Key).Scan(&stored, &installation, &root); err != nil {
		t.Fatal(err)
	}
	if stored != first || installation != "" || root != "" {
		t.Fatalf("stored bindings %q %q %q, want profile lifetime %q and empty root", stored, installation, root, first)
	}
	if r, err := Lookup(context.Background(), db, testBinding, e.Key, testNow); err != nil || r == nil {
		t.Fatalf("current lifetime could not read its own receipt: %v %v", r, err)
	}
	rotated := rotateProfileLifetime(t, db, "account", "profile")
	if rotated == first {
		t.Fatal("the lifetime did not rotate")
	}
	// The same textual profile id under a new lifetime is a different owner: the
	// old committed result is neither replayed nor applied again.
	if err := apply(t, db, testBinding, e, Result{Status: "applied", Revision: 1}, testNow); !errors.Is(err, ErrConflict) {
		t.Fatalf("old result crossed a lifetime rotation: %v", err)
	}
	if r, err := Lookup(context.Background(), db, testBinding, e.Key, testNow); err != nil || r != nil {
		t.Fatalf("old result was readable across a rotation: %v %v", r, err)
	}
	fresh := envelope(701)
	if err := apply(t, db, testBinding, fresh, Result{Status: "applied"}, testNow); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT profile_incarnation FROM user_mutation_receipts WHERE key=?`, fresh.Key).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != rotated {
		t.Fatalf("fresh receipt bound %q, want %q", stored, rotated)
	}
}

func TestReceiptWithoutLifetimeBindingNeverReplaysUnderOne(t *testing.T) {
	db := fixture(t)
	if err := apply(t, db, testBinding, envelope(710), Result{Status: "applied"}, testNow); err != nil {
		t.Fatal(err)
	}
	var scope int64
	if err := db.QueryRow(`SELECT scope_id FROM user_mutation_receipts WHERE key=?`, envelope(710).Key).Scan(&scope); err != nil {
		t.Fatal(err)
	}
	// A receipt committed by a build before the lifetime binding existed.
	legacy := envelope(711)
	if _, err := db.Exec(`INSERT INTO user_mutation_receipts(key,scope_id,profile_id,family_id,operation,digest,is_stop,issued_ms,status) VALUES(?,?,?,?,?,?,?,?, 'applied')`, legacy.Key, scope, "profile", "family", legacy.Operation, legacy.Digest, 0, testNow); err != nil {
		t.Fatal(err)
	}
	if err := apply(t, db, testBinding, legacy, Result{Status: "applied"}, testNow); !errors.Is(err, ErrConflict) {
		t.Fatalf("a receipt with no lifetime binding replayed under one: %v", err)
	}
	if r, err := Lookup(context.Background(), db, testBinding, legacy.Key, testNow); err != nil || r != nil {
		t.Fatalf("a receipt with no lifetime binding was readable: %v %v", r, err)
	}
}

func TestMutationRootBindsReceiptAndNeverReappearsAfterDeletion(t *testing.T) {
	db := fixture(t)
	installation := strings.Repeat("a", 32)
	root := mutationRoot(t, db, testBinding, installation, 1)
	b := testBinding
	b.RootID = root
	e := envelope(720)
	if err := apply(t, db, b, e, Result{Status: "applied", Revision: 3}, testNow); err != nil {
		t.Fatal(err)
	}
	var storedInstallation, storedRoot string
	if err := db.QueryRow(`SELECT installation_incarnation,root_id FROM user_mutation_receipts WHERE key=?`, e.Key).Scan(&storedInstallation, &storedRoot); err != nil {
		t.Fatal(err)
	}
	if storedInstallation != installation || storedRoot != root {
		t.Fatalf("stored root binding %q %q, want %q %q", storedInstallation, storedRoot, installation, root)
	}
	if r, err := Lookup(context.Background(), db, b, e.Key, testNow); err != nil || r == nil {
		t.Fatalf("root-bound receipt was not readable: %v %v", r, err)
	}
	// The same key and digest under another root is another owner's receipt.
	other := testBinding
	other.RootID = mutationRoot(t, db, testBinding, strings.Repeat("b", 32), 2)
	if err := apply(t, db, other, e, Result{Status: "applied"}, testNow); !errors.Is(err, ErrConflict) {
		t.Fatalf("another root read a receipt it did not own: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM user_mutation_roots WHERE id=?`, root); err != nil {
		t.Fatal(err)
	}
	if r, err := Lookup(context.Background(), db, b, e.Key, testNow); !errors.Is(err, ErrRootGone) {
		t.Fatalf("deleted root still served a result: %v %v", r, err)
	}
	if err := apply(t, db, b, envelope(721), Result{Status: "applied"}, testNow); !errors.Is(err, ErrRootGone) {
		t.Fatalf("deleted root accepted a new intent: %v", err)
	}
	if n := count(t, db, `SELECT count(*) FROM user_mutation_roots WHERE id=?`, root); n != 0 {
		t.Fatal("a gone root was recreated")
	}
}

func TestDrainingRootKeepsRecordedResultsAndRefusesNewIntents(t *testing.T) {
	db := fixture(t)
	root := mutationRoot(t, db, testBinding, strings.Repeat("c", 32), 1)
	b := testBinding
	b.RootID = root
	e := envelope(730)
	if err := apply(t, db, b, e, Result{Status: "applied"}, testNow); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE user_mutation_roots SET state='draining' WHERE id=?`, root); err != nil {
		t.Fatal(err)
	}
	if r, err := Lookup(context.Background(), db, b, e.Key, testNow); err != nil || r == nil {
		t.Fatalf("a draining root dropped its recorded result: %v %v", r, err)
	}
	if r, err := applyResult(t, db, b, e, testNow); err != nil || r == nil {
		t.Fatalf("a draining root did not replay: %v %v", r, err)
	}
	if err := apply(t, db, b, envelope(731), Result{Status: "applied"}, testNow); !errors.Is(err, ErrConflict) {
		t.Fatalf("a draining root accepted a new intent: %v", err)
	}
}

// applyResult replays through Check without applying, to prove a recorded
// result is still returned exactly.
func applyResult(t *testing.T, db *sql.DB, b Binding, e Envelope, now int64) (*Receipt, error) {
	t.Helper()
	var out *Receipt
	err := write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		out, err = Check(ctx, tx, b, e, now)
		return err
	})
	return out, err
}

func TestMutationRootScopeMismatchIsGone(t *testing.T) {
	db := fixture(t)
	root := mutationRoot(t, db, testBinding, strings.Repeat("d", 32), 1)
	base := testBinding
	base.RootID = root
	for name, mutate := range map[string]func(*Binding){
		"family":  func(b *Binding) { b.FamilyID = "another-family" },
		"profile": func(b *Binding) { b.ProfileID = "another-profile" },
	} {
		b := base
		mutate(&b)
		if err := apply(t, db, b, envelope(740), Result{Status: "applied"}, testNow); !errors.Is(err, ErrRootGone) {
			t.Fatalf("%s mismatch was not gone: %v", name, err)
		}
	}
	// A replaced membership incarnation makes the old owner row stale.
	if _, err := db.Exec(`UPDATE direct_memberships SET incarnation=lower(hex(randomblob(16))) WHERE account_id='account'`); err != nil {
		t.Fatal(err)
	}
	if err := apply(t, db, base, envelope(741), Result{Status: "applied"}, testNow); !errors.Is(err, ErrRootGone) {
		t.Fatalf("a rotated membership incarnation kept its root: %v", err)
	}
	// An erased profile keeps its sanitized row but no live lifetime.
	if _, err := db.Exec(`UPDATE direct_memberships SET incarnation=? WHERE account_id='account'`, membershipIncarnationFromRoot(t, db, root)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE direct_profiles SET deleted=1 WHERE account_id='account' AND id='profile'`); err != nil {
		t.Fatal(err)
	}
	if err := apply(t, db, base, envelope(742), Result{Status: "applied"}, testNow); !errors.Is(err, ErrRootGone) {
		t.Fatalf("an erased profile kept its root: %v", err)
	}
}

func membershipIncarnationFromRoot(t *testing.T, db *sql.DB, root string) string {
	t.Helper()
	var incarnation string
	if err := db.QueryRow(`SELECT account_incarnation FROM user_mutation_roots WHERE id=?`, root).Scan(&incarnation); err != nil {
		t.Fatal(err)
	}
	return incarnation
}

// TestReceiptRecordStaysWithinBoundWithLifetimeBindings is the focused worst-case
// record proof for the three added binding columns. Every retained field is at
// its schema maximum, including a 32-character installation incarnation and a
// 22-character root. The SQLite record must fit the 1024-byte bound; the
// aggregate table/index pages must also fit the existing receipt/root charges.
func TestReceiptRecordStaysWithinBoundWithLifetimeBindings(t *testing.T) {
	db := fixture(t)
	profile := strings.Repeat("p", 128)
	family := strings.Repeat("f", 128)
	if _, err := db.Exec(`INSERT INTO direct_profiles(id,account_id,name,lifetime) VALUES(?,'account','Worst case',lower(hex(randomblob(16))))`, profile); err != nil {
		t.Fatal(err)
	}
	b := Binding{Authority: "local", AccountID: "account", ProfileID: profile, FamilyID: family}
	b.RootID = mutationRoot(t, db, b, strings.Repeat("e", 32), 1)
	const n = 256
	err := write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		for i := 1; i <= n; i++ {
			e := envelope(i)
			e.Operation = strings.Repeat("o", 96)
			if _, err := Check(ctx, tx, b, e, testNow); err != nil {
				return err
			}
			r := Result{Status: "already_stopped", TargetID: strings.Repeat("t", 128), Revision: 9007199254740991, Generation: 2147483647, PositionMs: 9007199254740991, PositionStatus: "already_stopped"}
			if err := Save(ctx, tx, b, e, r, testNow); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE user_mutation_receipts SET stop_item_id=? WHERE key=?`, strings.Repeat("i", 128), e.Key); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var bytes, maxRecord int64
	if err := db.QueryRow(`SELECT coalesce(sum(pgsize),0),coalesce(max(CASE WHEN name='user_mutation_receipts' THEN mx_payload ELSE 0 END),0) FROM dbstat WHERE name LIKE 'user_mutation_%' OR name LIKE 'sqlite_autoindex_user_mutation_%'`).Scan(&bytes, &maxRecord); err != nil {
		t.Fatalf("physical accounting unavailable: %v", err)
	}
	if maxRecord > MaxReceiptRecordBytes {
		t.Fatalf("max record=%d > %d", maxRecord, MaxReceiptRecordBytes)
	}
	charged := count(t, db, `SELECT ordinary_bytes+stop_bytes FROM user_mutation_global`)
	if bytes > charged || charged > GlobalByteLimit {
		t.Fatalf("physical=%d charged=%d", bytes, charged)
	}
	t.Logf("receipts=%d physical=%d charged=%d max record=%d bound=%d", n, bytes, charged, maxRecord, MaxReceiptRecordBytes)
}

// TestRootBindingIsNotCarriedByRootlessReplay keeps the existing domain receipt
// shape honest: a rootless receipt records no installation and no root, and it
// matches only a rootless request.
func TestRootBindingIsNotCarriedByRootlessReplay(t *testing.T) {
	db := fixture(t)
	e := envelope(750)
	if err := apply(t, db, testBinding, e, Result{Status: "applied"}, testNow); err != nil {
		t.Fatal(err)
	}
	b := testBinding
	b.RootID = mutationRoot(t, db, testBinding, strings.Repeat("f", 32), 1)
	if err := apply(t, db, b, e, Result{Status: "applied"}, testNow); !errors.Is(err, ErrConflict) {
		t.Fatalf("a rootless receipt was reused as a root's result: %v", err)
	}
}
