package writer

import (
	"errors"
	"testing"
)

func TestUORootNativeOriginalReplayAndRetiredFloor(t *testing.T) {
	w, root := uoRootFixture(t)
	q := uoRootReference{root: root, kind: BeginPersonalJob, identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}, request: RequestDigest{3}}
	turn := wc008NativeTurn(t, w, BeginPersonalJob, append(uoRootDefinitions(), uoRootAckDefinitions()...))
	lookup, err := lookupUORootCommand(t.Context(), turn, q)
	if err != nil || lookup.decision != receiptFresh {
		t.Fatal(lookup.decision, err)
	}
	want := uoOperationResult{status: 2, revision: 7, cursor: 2, done: 1, appliedMS: 100, target: "original-job"}
	if _, err := persistUORootOperation(t.Context(), turn, lookup, want); err != nil {
		t.Fatal(err)
	}
	uoFlagsCommit(t, turn)
	turn = wc008NativeTurn(t, w, BeginPersonalJob, append(uoRootDefinitions(), uoRootAckDefinitions()...))
	var before, after int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	replay, err := lookupUORootCommand(t.Context(), turn, q)
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
	turn = wc008NativeTurn(t, w, BeginPersonalJob, append(uoRootDefinitions(), uoRootAckDefinitions()...))
	wrong := q
	wrong.request[0]++
	if _, err := lookupUORootCommand(t.Context(), turn, wrong); !errors.Is(err, ErrConflict) {
		t.Fatal("different original request not conflict", err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	var rows, bytes int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT receipt_rows,receipt_variable_bytes FROM user_mutation_command_owners WHERE root_id=?`, root).Scan(&rows, &bytes); err != nil {
		t.Fatal(err)
	}
	turn = wc008NativeTurn(t, w, BeginPersonalJob, append(uoRootDefinitions(), uoRootAckDefinitions()...))
	ackLookup, err := lookupUORootCommand(t.Context(), turn, q)
	if err != nil {
		t.Fatal(err)
	}
	if floor, err := acknowledgeUORootOperation(t.Context(), turn, ackLookup, 200); err != nil || floor != 1 {
		t.Fatal(floor, err)
	}
	uoFlagsCommit(t, turn)
	var keptRows, keptBytes int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT receipt_rows,receipt_variable_bytes FROM user_mutation_command_owners WHERE root_id=?`, root).Scan(&keptRows, &keptBytes); err != nil || keptRows != rows || keptBytes != bytes {
		t.Fatal("ACK refunded debt", keptRows, keptBytes, err)
	}
	turn = wc008NativeTurn(t, w, BeginPersonalJob, append(uoRootDefinitions(), uoRootAckDefinitions()...))
	retired, err := lookupUORootCommand(t.Context(), turn, q)
	if err != nil || retired.decision != receiptRetired || retired.payloadBytes != 0 {
		t.Fatal(retired.decision, err)
	}
	if _, err := restoreUORootOperation(t.Context(), turn, retired); err == nil {
		t.Fatal("retired receipt returned Applied output")
	}
}

func TestUORootLookupCannotCrossNativeTurns(t *testing.T) {
	w, root := uoRootFixture(t)
	q := uoRootReference{root: root, kind: BeginPersonalJob, identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}, request: RequestDigest{3}}
	turn := wc008NativeTurn(t, w, BeginPersonalJob, append(uoRootDefinitions(), uoRootAckDefinitions()...))
	lookup, err := lookupUORootCommand(t.Context(), turn, q)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	next := wc008NativeTurn(t, w, BeginPersonalJob, append(uoRootDefinitions(), uoRootAckDefinitions()...))
	if _, err := persistUORootOperation(t.Context(), next, lookup, uoOperationResult{status: 2, appliedMS: 100, target: "job"}); err == nil {
		t.Fatal("stale native frame reused")
	}
}
