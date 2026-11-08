package writer

import (
	"errors"
	"testing"
)

// Measure the ledger written by the existing BEGIN and staging bodies. Never
// use the retirement charge helper to seed or predict its own refund oracle.
func basicChargedRetirementFixture(t *testing.T) (*executor, basicRetireFirstEffects, int64, int64) {
	t.Helper()
	w, input := basicBeginFixture(t)
	var baseline, charged int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT used_bytes FROM catalog_info_input_budget`).Scan(&baseline); err != nil {
		t.Fatal(err)
	}
	turn := basicProbeTestTurn(t, w, basicBeginTestDefinitions())
	begun, err := beginBasicInput(t.Context(), turn, input, 2000)
	if err != nil {
		t.Fatal(err)
	}
	basicMediaCommit(t, turn)
	turn = basicStreamComponentTurn(t, w)
	if _, err = stageBasicStreamFact(t.Context(), turn, begun.source); err != nil {
		t.Fatal(err)
	}
	if _, err = sealBasicStreamFact(t.Context(), turn, begun.source); err != nil {
		t.Fatal(err)
	}
	basicMediaCommit(t, turn)
	if err = w.db.QueryRowContext(t.Context(), `SELECT used_bytes FROM catalog_info_input_budget`).Scan(&charged); err != nil || charged <= baseline {
		t.Fatal("producer bodies did not charge their retained rows", baseline, charged, err)
	}
	// Minimal settled, drained native state for this component. Other raw
	// families are already drained; their immutable manifests remain retained.
	for _, statement := range []string{
		`INSERT INTO catalog_info_shapes(id,container,video_codec,height,ref_count) VALUES(61,'mkv','h264',1080,2)`,
		`UPDATE catalog_assets SET info_epoch=18,info_publication_pending=0 WHERE id=41`,
		`UPDATE catalog_asset_links SET info_shape_id=61,info_duration=100 WHERE entity_id=31 AND asset_id=41`,
	} {
		if _, err = w.db.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = w.db.ExecContext(t.Context(), `UPDATE catalog_info_changes SET phase=5,job_revision=15,target_shape_id=61,target_available=1,has_after=1,after_entity=31,upper_entity=31 WHERE id=?`, begun.source.changeID); err != nil {
		t.Fatal(err)
	}
	if _, err = w.db.ExecContext(t.Context(), `UPDATE catalog_info_input_families SET rows_staged=rows_expected,sealed=1 WHERE change_id=?`, begun.source.changeID); err != nil {
		t.Fatal(err)
	}
	return w, basicRetireFirstEffects{source: begun.source, currentAssetEpoch: 18, changeRevision: 15, shapeID: 61, entityID: 31, counts: input.counts}, baseline, charged
}

func TestBasicRetireRefundEqualsActualProducerDebits(t *testing.T) {
	w, in, baseline, charged := basicChargedRetirementFixture(t)
	turn := basicRetireFirstComponentTurn(t, w, basicRetireFirstTestDefinitions())
	got, err := retireBasicFirstInput(t.Context(), turn, in)
	if err != nil || got.refunded != charged-baseline || got.expectedEpoch != 17 || got.currentAssetEpoch != 18 {
		t.Fatal("refund differs from independently observed producer debits", got, charged-baseline, err)
	}
	basicMediaCommit(t, turn)
	var remaining int64
	if err = w.db.QueryRowContext(t.Context(), `SELECT used_bytes FROM catalog_info_input_budget`).Scan(&remaining); err != nil || remaining != baseline {
		t.Fatal(remaining, baseline, err)
	}
}

func TestBasicRetireUnsettledAndWrongLibraryRefuseWithoutEffects(t *testing.T) {
	for _, mode := range []string{"unsettled", "wrong_library"} {
		t.Run(mode, func(t *testing.T) {
			w, in, _, charged := basicChargedRetirementFixture(t)
			if mode == "unsettled" {
				if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_info_changes SET phase=4 WHERE id=?`, in.source.changeID); err != nil {
					t.Fatal(err)
				}
			} else {
				in.source.libraryID = 8
			}
			turn := basicRetireFirstComponentTurn(t, w, basicRetireFirstTestDefinitions())
			var before, after int64
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			got, err := retireBasicFirstInput(t.Context(), turn, in)
			if !errors.Is(err, ErrConflict) || got != (basicRetiredFirstInput{}) {
				t.Fatal("unsettled or unrelated-library input was retired", got, err)
			}
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil || after != before {
				t.Fatal("refusal changed native input before rollback", before, after, err)
			}
			_ = turn.closeRows()
			_, _ = turn.transaction.rollback(err)
			var intact bool
			if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*)=1 FROM catalog_info_changes WHERE id=?) AND (SELECT count(*)=9 FROM catalog_info_input_families WHERE change_id=?) AND (SELECT count(*)=1 FROM catalog_info_input_witnesses WHERE change_id=?) AND (SELECT count(*)=1 FROM catalog_info_new_stream_facts WHERE change_id=?) AND (SELECT ref_count=2 FROM catalog_info_shapes WHERE id=61) AND (SELECT used_bytes=? FROM catalog_info_input_budget)`, in.source.changeID, in.source.changeID, in.source.changeID, in.source.changeID, charged).Scan(&intact); err != nil || !intact {
				t.Fatal("refusal/rollback lost owned rows or debt", intact, err)
			}
		})
	}
}
