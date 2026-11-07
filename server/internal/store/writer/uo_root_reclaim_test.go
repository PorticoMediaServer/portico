package writer

import (
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func uoRootAllDefinitions() []stmt.Definition {
	return append(append(uoRootDefinitions(), uoRootAckDefinitions()...), uoRootReclaimDefinitions()...)
}

func TestUORootNativeBoundedReclaimAndOriginalFixedSlot(t *testing.T) {
	w, root := uoRootFixture(t)
	ref := func(sequence int64, kind CommandID) uoRootReference {
		return uoRootReference{root: root, kind: kind, identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: sequence, OperationID: [16]byte{byte(sequence + 10)}}, request: RequestDigest{byte(sequence + 20)}}
	}
	for cycle := int64(0); cycle < 2; cycle++ {
		source := ref(1+2*cycle, BeginPersonalJob)
		turn := wc008NativeTurn(t, w, source.kind, uoRootAllDefinitions())
		lookup, err := lookupUORootCommand(t.Context(), turn, source)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := persistUORootOperation(t.Context(), turn, lookup, uoOperationResult{status: 2, revision: 1, appliedMS: 100 + cycle*100, target: "job"}); err != nil {
			t.Fatal(err)
		}
		uoFlagsCommit(t, turn)
		cleanup := ref(2+2*cycle, RootReceiptReclaim)
		// An unACKed successor remains proof, even though an older fixed cleanup
		// result can still be present on the same root owner.
		turn = wc008NativeTurn(t, w, cleanup.kind, uoRootAllDefinitions())
		if _, err := lookupUORootCommand(t.Context(), turn, cleanup); !errors.Is(err, ErrResolveRequired) {
			t.Fatal("reclaim passed unACKed source", err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
		turn = wc008NativeTurn(t, w, source.kind, uoRootAllDefinitions())
		lookup, err = lookupUORootCommand(t.Context(), turn, source)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := acknowledgeUORootOperation(t.Context(), turn, lookup, 200+cycle*100); err != nil {
			t.Fatal(err)
		}
		uoFlagsCommit(t, turn)
		var originalBytes int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT variable_bytes FROM user_mutation_command_receipts WHERE root_id=? AND command_sequence=?`, root, source.identity.Sequence).Scan(&originalBytes); err != nil {
			t.Fatal(err)
		}
		turn = wc008NativeTurn(t, w, cleanup.kind, uoRootAllDefinitions())
		lookup, err = lookupUORootCommand(t.Context(), turn, cleanup)
		if err != nil {
			t.Fatal(err)
		}
		result, err := reclaimUORootReceipt(t.Context(), turn, lookup, 300+cycle*100)
		if err != nil || result.removedCount != 1 || result.removedSequence != source.identity.Sequence || result.removedBytes != originalBytes || result.remainingRows != 0 || result.remainingBytes != 0 || !result.noNext {
			t.Fatal(result, err)
		}
		uoFlagsCommit(t, turn)
		turn = wc008NativeTurn(t, w, cleanup.kind, uoRootAllDefinitions())
		lookup, err = lookupUORootCommand(t.Context(), turn, cleanup)
		if err != nil || lookup.decision != receiptReplay {
			t.Fatal(lookup.decision, err)
		}
		var before, after int64
		if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&before); err != nil {
			t.Fatal(err)
		}
		got, err := restoreUORootReclaim(t.Context(), turn, lookup)
		if err != nil || got != result {
			t.Fatal(got, err)
		}
		if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil || before != after {
			t.Fatal("fixed-slot replay changed facts", before, after, err)
		}
		if _, err := acknowledgeUORootReclaim(t.Context(), turn, lookup, 400+cycle*100); err != nil {
			t.Fatal(err)
		}
		uoFlagsCommit(t, turn)
		var children, rows, bytes int64
		var due any
		if err := w.db.QueryRowContext(t.Context(), `SELECT receipt_rows,receipt_variable_bytes,receipt_reclaim_due_ms,(SELECT count(*) FROM user_mutation_command_receipts WHERE root_id=?) FROM user_mutation_command_owners WHERE root_id=?`, root, root).Scan(&rows, &bytes, &due, &children); err != nil || rows != 0 || bytes != 0 || children != 0 || due != nil {
			t.Fatal("idle debt loop or cleanup receipt child", rows, bytes, due, children, err)
		}
	}
	turn := wc008NativeTurn(t, w, RootReceiptReclaim, uoRootAllDefinitions())
	retired, err := lookupUORootCommand(t.Context(), turn, ref(2, RootReceiptReclaim))
	if err != nil || retired.decision != receiptRetired {
		t.Fatal(retired.decision, err)
	}
	if _, err := restoreUORootReclaim(t.Context(), turn, retired); err == nil {
		t.Fatal("newer fixed slot decoded as older command")
	}
}
