package writer

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// UO-01 is the first copy of the shared lifetime-root pattern. Followers
// (UO-02, UO-03, UO-05+) copy exactly:
//  1. uo_root_binding.go: bind at entry, check current authority and root
//     identity/state before the first retained lookup, and recheck both at
//     final release. NORMAL needs the private us_current_fence proof; a FULL
//     security consumer consumes the reviewed US-01 stamp.
//  2. uo_root_receipts.go: root PK, owner read, classifyReceipt, exact header
//     bounds, fresh-only persist with an active-root gate, and sole-codec
//     restore.
//  3. this fixture shape, these negative controls, and one own plan golden.
//
// They add only their own closed result type (one result_digest.go case), their
// statements, and their purpose ID; they do not widen the dispatcher or add an
// authority issuer.
func uoRootSecurityRef(root string) uoRootReference {
	return uoRootReference{root: root, kind: USMemberLibraries, identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}, request: RequestDigest{3}}
}

func uoRootPersisted(t *testing.T) (*executor, *usOwnerAuthorityProof, string, uoRootReference) {
	t.Helper()
	w, proof, root := uoRootSecurityFixture(t)
	q := uoRootSecurityRef(root)
	turn, binding := uoRootSecurityTurn(t, w, proof)
	lookup, err := lookupUORootCommand(t.Context(), turn, binding, q)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := persistUORootOperation(t.Context(), turn, lookup, uoOperationResult{status: 2, revision: 1, appliedMS: 100, target: "operation"}); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	return w, proof, root, q
}

func TestUORootNativeOriginalReplayAndSourceDrift(t *testing.T) {
	w, proof, _, q := uoRootPersisted(t)
	want := uoOperationResult{status: 2, revision: 1, appliedMS: 100, target: "operation"}
	// The current mutable profile source changes after the original result was
	// stored. No identity field moves; the retained original still replays.
	wc008NativeExec(t, w, `UPDATE direct_profiles SET name='drifted' WHERE id='issuer-profile'`)
	turn, binding := uoRootSecurityTurn(t, w, proof)
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

	turn, binding = uoRootSecurityTurn(t, w, proof)
	wrong := q
	wrong.request[0]++
	if _, err := lookupUORootCommand(t.Context(), turn, binding, wrong); !errors.Is(err, ErrConflict) {
		t.Fatal("different original request not conflict", err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
}

func TestUORootRejectsWrongRootIncarnationPurposeVersionDigest(t *testing.T) {
	w, proof, _, q := uoRootPersisted(t)
	for name, tc := range map[string]struct {
		change func(*uoRootReference)
		target error
	}{
		"root":         {func(v *uoRootReference) { v.root = "zyxwvutsrqponmlkjihgfe" }, ErrUORootGone},
		"incarnation":  {func(v *uoRootReference) { v.identity.Incarnation = [16]byte{9} }, ErrResolveRequired},
		"operation":    {func(v *uoRootReference) { v.identity.OperationID = [16]byte{9} }, ErrConflict},
		"purpose":      {func(v *uoRootReference) { v.kind = SetPersonalFlags }, ErrInvalid},
		"unregistered": {func(v *uoRootReference) { v.kind = CommandID(699) }, ErrInvalid},
	} {
		t.Run(name, func(t *testing.T) {
			v := q
			tc.change(&v)
			turn, binding := uoRootSecurityTurn(t, w, proof)
			if _, err := lookupUORootCommand(t.Context(), turn, binding, v); !errors.Is(err, tc.target) {
				t.Fatal("wrong error class", err)
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
			broken, brokenProof, _, brokenRef := uoRootPersisted(t)
			// Simulate a damaged stored proof: the canonical CHECKs forbid this
			// state, so the fixture must bypass them to prove lookup refuses it.
			wc008NativeExec(t, broken, `PRAGMA ignore_check_constraints=ON`)
			wc008NativeExec(t, broken, update)
			wc008NativeExec(t, broken, `PRAGMA ignore_check_constraints=OFF`)
			turn, binding := uoRootSecurityTurn(t, broken, brokenProof)
			if _, err := lookupUORootCommand(t.Context(), turn, binding, brokenRef); !errors.Is(err, ErrResolveRequired) {
				t.Fatal("malformed original proof not Resolve-required", err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
		})
	}
	// A payload altered after the fact still looks complete to the header
	// preflight, so the sole-codec restore must refuse it by digest.
	tampered, tamperedProof, _, tamperedRef := uoRootPersisted(t)
	wc008NativeExec(t, tampered, `PRAGMA ignore_check_constraints=ON`)
	wc008NativeExec(t, tampered, `UPDATE user_mutation_command_receipts SET result_payload=CAST(result_payload||X'00' AS BLOB)`)
	wc008NativeExec(t, tampered, `PRAGMA ignore_check_constraints=OFF`)
	turn, binding := uoRootSecurityTurn(t, tampered, tamperedProof)
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
	foreign, foreignProof, _, foreignRef := uoRootPersisted(t)
	wc008NativeExec(t, foreign, `UPDATE user_mutation_command_receipts SET command_kind=600`)
	turn, binding = uoRootSecurityTurn(t, foreign, foreignProof)
	if _, err := lookupUORootCommand(t.Context(), turn, binding, foreignRef); !errors.Is(err, ErrConflict) {
		t.Fatal("foreign stored purpose not conflict", err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
}

func TestUORootMissingAboveFloorProofNeedsResolve(t *testing.T) {
	w, proof, root := uoRootSecurityFixture(t)
	q := uoRootSecurityRef(root)
	// The owner claims an unacknowledged sequence whose receipt is absent; it
	// cannot be relabelled Applied or silently treated as fresh.
	wc008NativeExec(t, w, `UPDATE user_mutation_command_owners SET last_command_sequence=1 WHERE root_id=?`, root)
	turn, binding := uoRootSecurityTurn(t, w, proof)
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
	w, proof, root := uoRootSecurityFixture(t)
	q := uoRootSecurityRef(root)
	turn, binding := uoRootSecurityTurn(t, w, proof)
	lookup, err := lookupUORootCommand(t.Context(), turn, binding, q)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	next, nextBinding := uoRootSecurityTurn(t, w, proof)
	if _, err := persistUORootOperation(t.Context(), next, lookup, uoOperationResult{status: 2, appliedMS: 100, target: "job"}); err == nil {
		t.Fatal("stale native frame reused")
	}
	if _, err := lookupUORootCommand(t.Context(), next, nextBinding, q); err != nil {
		t.Fatal(err)
	}
}

func TestUORootRetainsDebtUntilActualDelete(t *testing.T) {
	w, _, root, _ := uoRootPersisted(t)
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

// A second command with the same original identity must never overwrite the
// saved result: removing the fresh gate or using OR REPLACE fails here.
func TestUORootFreshGatePreventsOriginalOverwrite(t *testing.T) {
	w, proof, _, q := uoRootPersisted(t)
	turn, binding := uoRootSecurityTurn(t, w, proof)
	replay, err := lookupUORootCommand(t.Context(), turn, binding, q)
	if err != nil || replay.decision != receiptReplay {
		t.Fatal(replay.decision, err)
	}
	overwrite := uoOperationResult{status: 2, revision: 99, appliedMS: 200, target: "overwrite"}
	if _, err := persistUORootOperation(t.Context(), turn, replay, overwrite); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("replay was allowed to persist a new result", err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	turn, binding = uoRootSecurityTurn(t, w, proof)
	replay, err = lookupUORootCommand(t.Context(), turn, binding, q)
	if err != nil {
		t.Fatal(err)
	}
	got, err := restoreUORootOperation(t.Context(), turn, replay)
	if err != nil || got.target != "operation" {
		t.Fatal("original result was overwritten", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
}

// A fresh sequence that reuses an operation identity violates the closed native
// UNIQUE and must surface as an identity conflict, not a generic SQL failure.
func TestUORootDuplicateOperationConflicts(t *testing.T) {
	w, proof, root, q := uoRootPersisted(t)
	// Simulate the post-ACK owner state so the next sequence classifies Fresh.
	wc008NativeExec(t, w, `UPDATE user_mutation_command_owners SET acknowledged_command_sequence=1 WHERE root_id=?`, root)
	next := q
	next.identity.Sequence = 2
	turn, binding := uoRootSecurityTurn(t, w, proof)
	lookup, err := lookupUORootCommand(t.Context(), turn, binding, next)
	if err != nil || lookup.decision != receiptFresh {
		t.Fatal(lookup.decision, err)
	}
	if _, err := persistUORootOperation(t.Context(), turn, lookup, uoOperationResult{status: 2, revision: 2, appliedMS: 101, target: "duplicate"}); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate operation not a conflict", err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
}

// Cancellation is a lifetime failure, never a committed or replayable result.
func TestUORootCancellationLeavesNoResult(t *testing.T) {
	w, proof, root := uoRootSecurityFixture(t)
	q := uoRootSecurityRef(root)
	turn, binding := uoRootSecurityTurn(t, w, proof)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := lookupUORootCommand(ctx, turn, binding, q); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled lookup not cancelled", err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	turn, binding = uoRootSecurityTurn(t, w, proof)
	lookup, err := lookupUORootCommand(t.Context(), turn, binding, q)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithCancel(t.Context())
	cancel()
	if _, err := persistUORootOperation(ctx, turn, lookup, uoOperationResult{status: 2, appliedMS: 100, target: "cancelled"}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled persist not cancelled", err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	var rows, bytes int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT receipt_rows,receipt_variable_bytes FROM user_mutation_command_owners WHERE root_id=?`, root).Scan(&rows, &bytes); err != nil || rows != 0 || bytes != 0 {
		t.Fatal("cancelled persist retained debt", rows, bytes, err)
	}
}

// A failure after the receipt INSERT must roll the nonempty prefix back.
func TestUORootRollbackAfterNonemptyPrefix(t *testing.T) {
	w, proof, root := uoRootSecurityFixture(t)
	q := uoRootSecurityRef(root)
	turn, binding := uoRootSecurityTurn(t, w, proof)
	lookup, err := lookupUORootCommand(t.Context(), turn, binding, q)
	if err != nil {
		t.Fatal(err)
	}
	// Move the owner revision after the lookup so only the final CAS fails.
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE user_mutation_command_owners SET revision=revision+1 WHERE root_id=?`, root); err != nil {
		t.Fatal(err)
	}
	if _, err := persistUORootOperation(t.Context(), turn, lookup, uoOperationResult{status: 2, appliedMS: 100, target: "prefix"}); err == nil {
		t.Fatal("stale owner revision accepted")
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	var receipts, rows, bytes int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM user_mutation_command_receipts),receipt_rows,receipt_variable_bytes FROM user_mutation_command_owners WHERE root_id=?`, root).Scan(&receipts, &rows, &bytes); err != nil || receipts != 0 || rows != 0 || bytes != 0 {
		t.Fatal("nonempty prefix survived rollback", receipts, rows, bytes, err)
	}
}

// A payload that vanished after a matching header is an incomplete original,
// never a raw driver error or a leaked row.
func TestUORootMissingPayloadNeedsResolve(t *testing.T) {
	w, proof, _, q := uoRootPersisted(t)
	turn, binding := uoRootSecurityTurn(t, w, proof)
	lookup, err := lookupUORootCommand(t.Context(), turn, binding, q)
	if err != nil || lookup.decision != receiptReplay {
		t.Fatal(lookup.decision, err)
	}
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `DELETE FROM user_mutation_command_receipts`); err != nil {
		t.Fatal(err)
	}
	if _, err := restoreUORootOperation(t.Context(), turn, lookup); !errors.Is(err, ErrResolveRequired) || errors.Is(err, sql.ErrNoRows) {
		t.Fatal("missing payload not Resolve-required", err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
}
