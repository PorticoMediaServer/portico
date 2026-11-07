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
