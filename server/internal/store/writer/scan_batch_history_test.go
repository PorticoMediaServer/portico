package writer

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
)

// Saved rows are supplied independently of the fresh writer. Replay must be
// able to recover every supported width even with an N=1 accepting profile.
func batchHistoryFixture(t *testing.T, width int) (*executor, ScanReceiptRequest, inventoryAcceptResult) {
	t.Helper()
	w, requests, _ := inventoryLookupFixture(t)
	q := requests["hit"]
	v := inventoryAcceptResult{RunID: q.RunID, AcceptedRunRevision: 2, AppliedMS: 100,
		SourceID: "scan-source-hit", SourceIncarnation: "source-incarnation", SourceRevision: "opaque:007", NextCursor: "native-next", ResultCount: int64(width)}
	for i := 0; i < width; i++ {
		object := strings.Repeat("o", 10+i)
		v.Members = append(v.Members, inventoryResultMember{int64(i), 0, 0, object, 7, 31, 41, 1})
		if i == 0 {
			if _, err := w.db.ExecContext(t.Context(), `UPDATE scan_command_results SET object_id=? WHERE run_id=?`, object, q.RunID); err != nil {
				t.Fatal(err)
			}
		} else {
			if _, err := w.db.ExecContext(t.Context(), `INSERT INTO scan_command_results SELECT run_id,command_incarnation,command_sequence,?,child_kind,child_ordinal,?,library_id,subject_id,asset_id,accepted_asset_revision FROM scan_command_results WHERE run_id=? AND input_ordinal=0`, i, object, q.RunID); err != nil {
				t.Fatal(err)
			}
		}
	}
	digest, err := inventoryAcceptResultDigest(q.Receipt.Identity, q.Receipt.RequestDigest, v)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE scan_command_receipts SET result_count=?,result_digest=? WHERE run_id=?`, width, digest[:], q.RunID); err != nil {
		t.Fatal(err)
	}
	return w, q, v
}

func TestInventoryBatchHistory(t *testing.T) {
	for _, width := range []int{1, 2, maxInventoryBatchFiles} {
		t.Run(strings.Repeat("N", width), func(t *testing.T) {
			w, q, want := batchHistoryFixture(t, width)
			registry := cachedNativeInventoryFixtureRegistry(t, w, inventoryLookupDefinitions(), "inventory_resolution_test.go", "scan_receipts_test.go")
			turn, tx := inventoryLookupTurn(t, w, registry)
			var before, after int64
			if err := tx.conn.QueryRowContext(tx.ctx, `SELECT total_changes()`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			got, err := lookupInventoryAccept(t.Context(), turn, q)
			if err != nil || got.decision != receiptReplay || !reflect.DeepEqual(got.result, want) {
				t.Fatal("saved cohort", got, want, err)
			}
			if err := tx.conn.QueryRowContext(tx.ctx, `SELECT total_changes()`).Scan(&after); err != nil {
				t.Fatal(err)
			}
			if after != before {
				t.Fatal("replay wrote effects")
			}
			tx.rollback(nil)
		})
	}
}

func TestInventoryBatchHistoryRefusesMalformedCohortsBeforeText(t *testing.T) {
	for _, mutation := range []string{
		`DELETE FROM scan_command_results WHERE run_id='scan-hit' AND input_ordinal=1`,
		`UPDATE scan_command_results SET input_ordinal=8 WHERE run_id='scan-hit' AND input_ordinal=1`,
		`UPDATE scan_command_results SET child_kind=1,child_ordinal=1 WHERE run_id='scan-hit' AND input_ordinal=1`,
		`UPDATE scan_command_results SET object_id=printf('%.*c',2097152,'x') WHERE run_id='scan-hit' AND input_ordinal=1`,
	} {
		t.Run(mutation, func(t *testing.T) {
			w, q, _ := batchHistoryFixture(t, 2)
			if _, err := w.db.ExecContext(t.Context(), mutation); err != nil {
				t.Fatal(err)
			}
			registry := cachedNativeInventoryFixtureRegistry(t, w, inventoryLookupDefinitions(), "inventory_resolution_test.go", "scan_receipts_test.go")
			turn, tx := inventoryLookupTurn(t, w, registry)
			step := turn.steps[scanReceiptResults]
			step.work.Rows = math.MaxUint64
			turn.steps[scanReceiptResults] = step
			got, err := lookupInventoryAccept(t.Context(), turn, q)
			if err == nil || errors.Is(err, ErrQuantum) || !reflect.DeepEqual(got, inventoryReceiptLookup{}) {
				t.Fatal("copied unpreflighted cohort", got, err)
			}
			tx.rollback(err)
		})
	}
}
