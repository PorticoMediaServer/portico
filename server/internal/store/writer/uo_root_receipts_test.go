package writer

import (
	"errors"
	"testing"
)

// UO-01 is the first copy of the shared lifetime-root pattern. Followers
// (UO-02, UO-03, UO-05+) copy exactly:
//  1. uo_root_binding.go: bind at entry, check before the first retained lookup,
//     and recheck before releasing a payload or digest.
//  2. uo_root_receipts.go: reference/owner read, classifyReceipt, exact header
//     bounds, fresh-only persist, and sole-codec restore.
//  3. this fixture shape, these negative controls, and one own plan golden.
//
// They add only their own closed result type (one result_digest.go case), their
// statements, and their purpose ID; they do not widen the dispatcher or add an
// authority issuer.
func uoRootTurn(t *testing.T, w *executor) (*commandTurn, *uoRootBinding) {
	t.Helper()
	turn := wc008NativeTurn(t, w, SetPersonalFlags, uoRootDefinitions())
	binding, err := bindUORoot(turn, nil)
	if err != nil {
		t.Fatal(err)
	}
	return turn, &binding
}

func uoRootReferenceFixture(root string) uoRootReference {
	return uoRootReference{root: root, kind: SetPersonalFlags, identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}, request: RequestDigest{3}}
}

func TestUORootNativeOriginalReplayAndSourceDrift(t *testing.T) {
	w, root := uoRootFixture(t)
	q := uoRootReferenceFixture(root)
	turn, binding := uoRootTurn(t, w)
	lookup, err := lookupUORootCommand(t.Context(), turn, binding, q)
	if err != nil || lookup.decision != receiptFresh {
		t.Fatal(lookup.decision, err)
	}
	want := uoOperationResult{status: 2, revision: 7, cursor: 2, done: 1, failed: 0, appliedMS: 100, target: "original-operation"}
	if _, err := persistUORootOperation(t.Context(), turn, lookup, want); err != nil {
		t.Fatal(err)
	}
	uoFlagsCommit(t, turn)

	// The current mutable source changes after the original result was stored.
	wc008NativeExec(t, w, `UPDATE user_mutation_roots SET family_id='drifted-family' WHERE id=?`, root)
	turn, binding = uoRootTurn(t, w)
	var before, after int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	replay, err := lookupUORootCommand(t.Context(), turn, binding, q)
	if err != nil || replay.decision != receiptReplay {
		t.Fatal(replay.decision, err)
	}
	got, err := restoreUORootOperation(t.Context(), turn, replay)
	if err != nil || got != want {
		t.Fatal(got, err)
	}
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil || before != after {
		t.Fatal("replay added effects", before, after, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	turn, binding = uoRootTurn(t, w)
	wrong := q
	wrong.request[0]++
	if _, err := lookupUORootCommand(t.Context(), turn, binding, wrong); !errors.Is(err, ErrConflict) {
		t.Fatal("different original request not conflict", err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
}

func uoRootPersisted(t *testing.T) (*executor, string, uoRootReference) {
	t.Helper()
	w, root := uoRootFixture(t)
	q := uoRootReferenceFixture(root)
	turn, binding := uoRootTurn(t, w)
	lookup, err := lookupUORootCommand(t.Context(), turn, binding, q)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := persistUORootOperation(t.Context(), turn, lookup, uoOperationResult{status: 2, revision: 1, appliedMS: 100, target: "operation"}); err != nil {
		t.Fatal(err)
	}
	uoFlagsCommit(t, turn)
	return w, root, q
}

func TestUORootRejectsWrongRootIncarnationPurposeVersionDigest(t *testing.T) {
	w, _, q := uoRootPersisted(t)
	for name, change := range map[string]func(*uoRootReference){
		"root":         func(v *uoRootReference) { v.root = "zyxwvutsrqponmlkjihgfe" },
		"incarnation":  func(v *uoRootReference) { v.identity.Incarnation = [16]byte{9} },
		"operation":    func(v *uoRootReference) { v.identity.OperationID = [16]byte{9} },
		"purpose":      func(v *uoRootReference) { v.kind = USMemberLibraries },
		"unregistered": func(v *uoRootReference) { v.kind = CommandID(699) },
	} {
		t.Run(name, func(t *testing.T) {
			v := q
			change(&v)
			turn, binding := uoRootTurn(t, w)
			if _, err := lookupUORootCommand(t.Context(), turn, binding, v); err == nil {
				t.Fatal("foreign original binding accepted")
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
		})
	}
	for name, update := range map[string]string{
		"version": `UPDATE user_mutation_command_receipts SET result_version=2`,
		"digest":  `UPDATE user_mutation_command_receipts SET result_digest=X'00'`,
	} {
		t.Run(name, func(t *testing.T) {
			broken, _, brokenRef := uoRootPersisted(t)
			// Simulate a damaged stored proof: the canonical CHECKs forbid this
			// state, so the fixture must bypass them to prove lookup refuses it.
			wc008NativeExec(t, broken, `PRAGMA ignore_check_constraints=ON`)
			wc008NativeExec(t, broken, update)
			wc008NativeExec(t, broken, `PRAGMA ignore_check_constraints=OFF`)
			turn, binding := uoRootTurn(t, broken)
			if _, err := lookupUORootCommand(t.Context(), turn, binding, brokenRef); !errors.Is(err, ErrResolveRequired) {
				t.Fatal("malformed original proof not Resolve-required", err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
		})
	}
	// A payload altered after the fact is still complete-looking to the header
	// preflight, so the sole-codec restore must refuse it by digest.
	tampered, _, tamperedRef := uoRootPersisted(t)
	wc008NativeExec(t, tampered, `PRAGMA ignore_check_constraints=ON`)
	wc008NativeExec(t, tampered, `UPDATE user_mutation_command_receipts SET result_payload=CAST(result_payload||X'00' AS BLOB)`)
	wc008NativeExec(t, tampered, `PRAGMA ignore_check_constraints=OFF`)
	turn, binding := uoRootTurn(t, tampered)
	lookup, err := lookupUORootCommand(t.Context(), turn, binding, tamperedRef)
	if err != nil || lookup.decision != receiptReplay {
		t.Fatal(lookup.decision, err)
	}
	if _, err := restoreUORootOperation(t.Context(), turn, lookup); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("tampered payload not Resolve-required", err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	// A row that is intact but belongs to a different stored purpose conflicts.
	foreign, _, foreignRef := uoRootPersisted(t)
	wc008NativeExec(t, foreign, `UPDATE user_mutation_command_receipts SET command_kind=540`)
	turn, binding = uoRootTurn(t, foreign)
	if _, err := lookupUORootCommand(t.Context(), turn, binding, foreignRef); !errors.Is(err, ErrConflict) {
		t.Fatal("foreign stored purpose not conflict", err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
}

func TestUORootMissingAboveFloorProofNeedsResolve(t *testing.T) {
	w, root := uoRootFixture(t)
	q := uoRootReferenceFixture(root)
	// The owner claims an unacknowledged sequence whose receipt is absent; it
	// cannot be relabelled Applied or silently treated as fresh.
	wc008NativeExec(t, w, `UPDATE user_mutation_command_owners SET last_command_sequence=1 WHERE root_id=?`, root)
	turn, binding := uoRootTurn(t, w)
	if _, err := lookupUORootCommand(t.Context(), turn, binding, q); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("missing above-floor proof not Resolve-required", err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	var rows, bytes int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT receipt_rows,receipt_variable_bytes FROM user_mutation_command_owners WHERE root_id=?`, root).Scan(&rows, &bytes); err != nil || rows != 0 || bytes != 0 {
		t.Fatal("failed lookup moved debt", rows, bytes, err)
	}
}

func TestUORootLookupCannotCrossNativeTurns(t *testing.T) {
	w, root := uoRootFixture(t)
	q := uoRootReferenceFixture(root)
	turn, binding := uoRootTurn(t, w)
	lookup, err := lookupUORootCommand(t.Context(), turn, binding, q)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	next, nextBinding := uoRootTurn(t, w)
	if _, err := persistUORootOperation(t.Context(), next, lookup, uoOperationResult{status: 2, appliedMS: 100, target: "job"}); err == nil {
		t.Fatal("stale native frame reused")
	}
	if _, err := lookupUORootCommand(t.Context(), next, nextBinding, q); err != nil {
		t.Fatal(err)
	}
}

func TestUORootRetainsDebtUntilActualDelete(t *testing.T) {
	w, root := uoRootFixture(t)
	q := uoRootReferenceFixture(root)
	turn, binding := uoRootTurn(t, w)
	lookup, err := lookupUORootCommand(t.Context(), turn, binding, q)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := persistUORootOperation(t.Context(), turn, lookup, uoOperationResult{status: 2, revision: 1, appliedMS: 100, target: "operation"}); err != nil {
		t.Fatal(err)
	}
	uoFlagsCommit(t, turn)
	var rows, bytes int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT receipt_rows,receipt_variable_bytes FROM user_mutation_command_owners WHERE root_id=?`, root).Scan(&rows, &bytes); err != nil || rows != 1 || bytes <= 0 {
		t.Fatal("receipt debt not retained", rows, bytes, err)
	}
	if _, err := w.db.ExecContext(t.Context(), `DELETE FROM user_mutation_command_receipts WHERE root_id=?`, root); err != nil {
		t.Fatal(err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT receipt_rows,receipt_variable_bytes FROM user_mutation_command_owners WHERE root_id=?`, root).Scan(&rows, &bytes); err != nil || rows == 0 || bytes == 0 {
		t.Fatal("deleting a receipt row did not leave owner debt for a real reclaim", rows, bytes, err)
	}
}
