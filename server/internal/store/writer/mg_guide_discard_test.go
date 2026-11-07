package writer

import "testing"

func TestMGGuideNativeDiscardRetainsGenerationAndReceipt(t *testing.T) {
	for _, removed := range []bool{false, true} {
		t.Run(map[bool]string{false: "current", true: "removed"}[removed], func(t *testing.T) {
			w, b := mgGuideBirthFixture(t, false)
			turn := mgGuideBirthTurn(t, w)
			result, err := mgBeginGuideNative(t.Context(), turn, b)
			if err != nil {
				t.Fatal(err)
			}
			saved, err := mgSaveGuideReceipt(t.Context(), turn, result, b.identity, b.requestDigest, mgGuideReceiptBegin, mgGuideReceiptProjectionFixture())
			if err != nil {
				t.Fatal(err)
			}
			op, err := mgACKGuideReceipt(t.Context(), turn, result.operation, saved.key, saved.resultDigest)
			if err != nil {
				t.Fatal(err)
			}
			if err := turn.closeRows(); err != nil {
				t.Fatal(err)
			}
			if _, err := turn.transaction.commit(); err != nil {
				t.Fatal(err)
			}
			if removed {
				if _, err := w.db.ExecContext(t.Context(), `UPDATE live_source_identities SET has_live_source_removals=1,removal_request_id='removed',removal_revision=8,removal_removed_at='now' WHERE id='source'`); err != nil {
					t.Fatal(err)
				}
			}
			turn = mgGuideBirthTurn(t, w)
			identity := mgGuideIdentityAfter(op)
			result, err = mgDiscardGuideNative(t.Context(), turn, op, identity)
			if err != nil || result.operation.status != "superseded" {
				t.Fatal(result, err)
			}
			if _, err := mgSaveGuideReceipt(t.Context(), turn, result, identity, RequestDigest{15}, mgGuideReceiptDiscard, mgGuideReceiptProjectionFixture()); err != nil {
				t.Fatal(err)
			}
			if out, err := mgStageGuideNative(t.Context(), turn, result.operation, mgGuideIdentityAfter(result.operation), mgGuideFrozenStage{}); err == nil || out != (mgGuideNativeResult{}) {
				t.Fatal("discard appended", out, err)
			}
			if err := turn.closeRows(); err != nil {
				t.Fatal(err)
			}
			if _, err := turn.transaction.commit(); err != nil {
				t.Fatal(err)
			}
			var generation, head string
			if err := w.db.QueryRowContext(t.Context(), `SELECT id FROM live_generations WHERE id=?`, b.manifest.generation).Scan(&generation); err != nil {
				t.Fatal("logical discard deleted generation", err)
			}
			if err := w.db.QueryRowContext(t.Context(), `SELECT active_generation FROM live_sources WHERE id='source'`).Scan(&head); err != nil || head != "old" {
				t.Fatal("discard changed serving head", head, err)
			}
		})
	}
}
