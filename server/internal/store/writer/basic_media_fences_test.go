package writer

import (
	"errors"
	"testing"
)

// These six component controls exercise the input fence itself. Capture happens
// before native drift; no stored plan or source hash comparison can supply the
// expected conflict. Removing checkBasicStreamInput from checkBasicMediaFamily
// must let the direct stage/seal call proceed and fail these assertions.
func TestBasicMediaNativeInputDriftFences(t *testing.T) {
	for _, drift := range []struct {
		name string
		sql  string
	}{
		{"original_source_witness", `UPDATE catalog_info_input_witnesses SET object_revision='object:020' WHERE change_id=51 AND ordinal=0`},
		{"asset_info_epoch", `UPDATE catalog_assets SET info_epoch=18 WHERE id=41`},
		{"cjr_input_revision", `UPDATE catalog_info_changes SET input_revision=8 WHERE id=51`},
	} {
		for _, operation := range []string{"stage", "seal"} {
			t.Run(drift.name+"/"+operation, func(t *testing.T) {
				w, source := basicStreamFixture(t)
				if _, err := w.db.ExecContext(t.Context(), `INSERT INTO catalog_info_input_families(change_id,family,witness_ordinal,rows_expected) VALUES(51,2,0,2)`); err != nil {
					t.Fatal(err)
				}
				// Freeze the original bounded family before changing any native
				// source witness, asset epoch or CJR input vector.
				family, err := prepareBasicMediaFamily(source, 2, 0, []basicMediaRow{
					basicStreamRow{index: 0, streamType: "video", codec: "h264", isDefault: true},
					basicStreamRow{index: 3, streamType: "audio", codec: "aac", language: "en", channels: 2, channelLayout: "stereo", isDefault: true},
				})
				if err != nil {
					t.Fatal(err)
				}
				turn := basicComponentTurn(t, w, basicMediaTestDefs())
				// The native stream child FK needs its actual owner first.
				if got, err := stageBasicStreamFact(t.Context(), turn, source); err != nil || got != (basicFamilyProgress{51, 7, 1, 0}) {
					t.Fatal("stage parent fact", got, err)
				}
				// Separate each existing bounded owner turn; the regression
				// must not fail because prerequisites consumed its quantum.
				basicMediaCommit(t, turn)
				turn = basicComponentTurn(t, w, basicMediaTestDefs())
				if operation == "seal" {
					if got, err := stageBasicMediaRows(t.Context(), turn, family, 0, 2); err != nil || got != (basicFamilyProgress{51, 7, 2, 0}) {
						t.Fatal("populate through stage owner", got, err)
					}
					basicMediaCommit(t, turn)
					turn = basicComponentTurn(t, w, basicMediaTestDefs())
				}
				if _, err := turn.transaction.conn.ExecContext(t.Context(), drift.sql); err != nil {
					t.Fatal(err)
				}
				// Observe in the SAME transaction, before rollback could hide a
				// partial write. total_changes also detects equal-value writes.
				snapshot := func() [5]int64 {
					t.Helper()
					var s [5]int64
					if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT
(SELECT count(*) FROM catalog_info_new_streams WHERE change_id=51),
(SELECT rows_staged FROM catalog_info_input_families WHERE change_id=51 AND family=2),
(SELECT sealed FROM catalog_info_input_families WHERE change_id=51 AND family=2),
(SELECT used_bytes FROM catalog_info_input_budget WHERE singleton=1), total_changes()`).Scan(&s[0], &s[1], &s[2], &s[3], &s[4]); err != nil {
						t.Fatal(err)
					}
					return s
				}
				before := snapshot()
				wantRows := int64(0)
				if operation == "seal" {
					wantRows = 2
				}
				if before[0] != wantRows || before[1] != wantRows || before[2] != 0 {
					t.Fatal("invalid pre-refusal population", before)
				}
				var got basicFamilyProgress
				if operation == "stage" {
					got, err = stageBasicMediaRows(t.Context(), turn, family, 0, 2)
				} else {
					got, err = sealBasicMediaFamily(t.Context(), turn, family)
				}
				if !errors.Is(err, ErrConflict) || got != (basicFamilyProgress{}) {
					t.Fatal("native input drift must return ErrConflict and ZERO", got, err)
				}
				if after := snapshot(); after != before {
					t.Fatal("refusal mutated rows/stage/seal/debt or total_changes", before, after)
				}
				if err := turn.closeRows(); err != nil {
					t.Fatal(err)
				}
				_, _ = turn.transaction.rollback(err)
			})
		}
	}
}
