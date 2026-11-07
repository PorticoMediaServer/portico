package writer

import (
	"fmt"
	"testing"
)

func mgGuideBirthFixture(t *testing.T, fresh bool) (*executor, mgGuideBirth) {
	t.Helper()
	w, old := mgGuideReceiptFixture(t, false)
	m := old.progress.manifest
	m.generation = "birth-generation"
	m.expected = [3]int64{1, 0, 0}
	if fresh {
		m.source = "fresh-source"
		m.expectedRevision = 0
		m.previousGeneration = ""
	}
	return w, mgGuideBirth{request: "birth-request", input: "birth-input", name: "New Guide", tuners: 1, manifest: m, identity: CommandIdentity{Incarnation: [16]byte{11}, Sequence: 1, OperationID: [16]byte{12}}, requestDigest: RequestDigest{13}}
}
func mgGuideBirthTurn(t *testing.T, w *executor) *commandTurn {
	t.Helper()
	defs := append(mgGuidePublicationDefinitions(), mgGuideReceiptDefinitions()...)
	defs = append(defs, mgGuideStageDefinitions()...)
	defs = append(defs, mgGuideBeginDefinitions()...)
	defs = append(defs, mgGuideDiscardDefinitions()...)
	return mgGuideReceiptTestTurn(t, w, defs)
}
func TestMGGuideNativeBirthReceiptAndNoResurrection(t *testing.T) {
	for _, fresh := range []bool{false, true} {
		t.Run(fmt.Sprint(fresh), func(t *testing.T) {
			w, b := mgGuideBirthFixture(t, fresh)
			turn := mgGuideBirthTurn(t, w)
			result, err := mgBeginGuideNative(t.Context(), turn, b)
			if err != nil {
				t.Fatal(err)
			}
			if result.operation.progress.staged != ([3]int64{}) || result.operation.acknowledged != 0 || result.operation.progress.sealed || result.operation.progress.sequence != 1 {
				t.Fatal(result)
			}
			saved, err := mgSaveGuideReceipt(t.Context(), turn, result, b.identity, b.requestDigest, mgGuideReceiptBegin, mgGuideReceiptProjectionFixture())
			if err != nil {
				t.Fatal(err)
			}
			acked, err := mgACKGuideReceipt(t.Context(), turn, result.operation, saved.key, saved.resultDigest)
			if err != nil || acked.acknowledged != 1 {
				t.Fatal(acked, err)
			}
			if err := turn.closeRows(); err != nil {
				t.Fatal(err)
			}
			if _, err := turn.transaction.commit(); err != nil {
				t.Fatal(err)
			}
			// Neither a new generation nor a new namespace may reuse the request ID.
			b.manifest.generation = "another-generation"
			b.identity.Incarnation[0]++
			turn = mgGuideBirthTurn(t, w)
			if out, err := mgBeginGuideNative(t.Context(), turn, b); err == nil || out != (mgGuideNativeResult{}) {
				t.Fatal("birth recycled", out, err)
			}
		})
	}
}
func TestMGGuideNativeBirthRefusesEmptyAndCapacity(t *testing.T) {
	for _, test := range []string{"zero-channels", "stale-head", "capacity"} {
		t.Run(test, func(t *testing.T) {
			w, b := mgGuideBirthFixture(t, true)
			switch test {
			case "zero-channels":
				b.manifest.expected[0] = 0
			case "stale-head":
				b.manifest.expectedRevision = 7
				b.manifest.previousGeneration = "old"
			case "capacity":
				for j := 0; j < 63; j++ {
					source, generation := fmt.Sprintf("capacity-source-%d", j), fmt.Sprintf("capacity-generation-%d", j)
					for _, q := range []struct {
						sql  string
						args []any
					}{
						{`INSERT INTO live_source_identities(id) VALUES(?)`, []any{source}},
						{`INSERT INTO live_generations(id,source_id,name,tuner_count,channel_count,programme_count,created_at) VALUES(?,?,'Capacity',1,1,0,'now')`, []any{generation, source}},
						{`INSERT INTO live_sources VALUES(?,'Capacity',1,'active',1,?,'now')`, []any{source, generation}},
					} {
						if _, err := w.db.ExecContext(t.Context(), q.sql, q.args...); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			turn := mgGuideBirthTurn(t, w)
			if out, err := mgBeginGuideNative(t.Context(), turn, b); err == nil || out != (mgGuideNativeResult{}) {
				t.Fatal("invalid birth", out, err)
			}
			if err := turn.closeRows(); err != nil {
				t.Fatal(err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			var n int64
			if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM live_source_identities WHERE id='fresh-source'`).Scan(&n); err != nil || n != 0 {
				t.Fatal("identity prefix escaped", n, err)
			}
		})
	}
}

func TestMGGuideNativeBirthStageSealPublishReceipts(t *testing.T) {
	w, b := mgGuideBirthFixture(t, false)
	stage, err := freezeMGGuideChannels([]mgGuideChannel{{id: "born-channel", name: "Born Channel", locator: []byte("sealed locator")}})
	if err != nil {
		t.Fatal(err)
	}
	// This immutable whole-input commitment is frozen before the writer turn.
	m := b.manifest
	initial := mgGuideInitialChain(m)
	b.manifest.expectedChain = mgGuideNextChain(initial, mgGuideStageDelta{mgGuideChannels, m.source, m.generation, 2, 0, 1, stage.bytes + 9 + int64(len(m.generation)), stage.payload})
	turn := mgGuideBirthTurn(t, w)
	result, err := mgBeginGuideNative(t.Context(), turn, b)
	if err != nil {
		t.Fatal(err)
	}
	accept := func(result mgGuideNativeResult, identity CommandIdentity, kind mgGuideReceiptKind) mgGuideNative {
		t.Helper()
		digest := RequestDigest{byte(identity.Sequence)}
		if identity.Sequence == 1 {
			digest = b.requestDigest
		}
		saved, err := mgSaveGuideReceipt(t.Context(), turn, result, identity, digest, kind, mgGuideReceiptProjectionFixture())
		if err != nil {
			t.Fatal(kind, err)
		}
		op, err := mgACKGuideReceipt(t.Context(), turn, result.operation, saved.key, saved.resultDigest)
		if err != nil {
			t.Fatal(kind, err)
		}
		return op
	}
	op := accept(result, b.identity, mgGuideReceiptBegin)
	identity := mgGuideIdentityAfter(op)
	result, err = mgStageGuideNative(t.Context(), turn, op, identity, stage)
	if err != nil {
		t.Fatal(err)
	}
	op = accept(result, identity, mgGuideReceiptChannels)
	identity = mgGuideIdentityAfter(op)
	result, err = mgSealGuideNative(t.Context(), turn, op, identity)
	if err != nil {
		t.Fatal(err)
	}
	op = accept(result, identity, mgGuideReceiptSeal)
	identity = mgGuideIdentityAfter(op)
	result, err = mgPublishGuideNative(t.Context(), turn, op, identity, true)
	if err != nil {
		t.Fatal(err)
	}
	op = accept(result, identity, mgGuideReceiptPublish)
	if op.status != "published" || op.progress.sequence != 4 || op.acknowledged != 4 || result.head.generation != b.manifest.generation {
		t.Fatal("final native protocol", op, result.head)
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	var receipts int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM live_operation_command_receipts WHERE request_id=?`, b.request).Scan(&receipts); err != nil || receipts != 4 {
		t.Fatal(receipts, err)
	}
}

func TestMGGuideOrphanReceiptBlocksNewBirth(t *testing.T) {
	w, b := mgGuideBirthFixture(t, true)
	turn := mgGuideBirthTurn(t, w)
	result, err := mgBeginGuideNative(t.Context(), turn, b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgSaveGuideReceipt(t.Context(), turn, result, b.identity, b.requestDigest, mgGuideReceiptBegin, mgGuideReceiptProjectionFixture()); err != nil {
		t.Fatal(err)
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	// Simulated damaged cleanup: history survives but both owner and floor are
	// absent. Absence is not permission to classify a new namespace as fresh.
	if _, err := w.db.ExecContext(t.Context(), `DELETE FROM live_operations WHERE request_id=?`, b.request); err != nil {
		t.Fatal(err)
	}
	b.identity.Incarnation[0]++
	b.manifest.generation = "orphan-rebirth"
	turn = mgGuideBirthTurn(t, w)
	if out, err := mgBeginGuideNative(t.Context(), turn, b); err == nil || out != (mgGuideNativeResult{}) {
		t.Fatal("history ignored by birth", out, err)
	}
}
