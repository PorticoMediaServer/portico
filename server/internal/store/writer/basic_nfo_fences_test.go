package writer

import (
	"context"
	"errors"
	"testing"
)

// basicNFOFenceSnapshot is the complete observable NFO effect state plus the
// connection's cumulative write counter. It is read from the SAME native
// transaction that will execute the body, so a refusal must leave every value
// byte-identical and must not add even one native change.
type basicNFOFenceSnapshot struct {
	changes int64
	rows    int64
	row     string
	status  string
}

func basicNFOFenceSnapshotOf(t *testing.T, turn *commandTurn) basicNFOFenceSnapshot {
	t.Helper()
	var s basicNFOFenceSnapshot
	// char(31) keeps the exact row/status fields unambiguous without inventing
	// a second owner helper or trusting a golden/source hash.
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes(),
 (SELECT count(*) FROM video_nfo_evidence WHERE library_id='basic-library' AND asset_id='basic-asset'),
 (SELECT locator||char(31)||kind||char(31)||coordinate||char(31)||digest||char(31)||payload||char(31)||source_size||char(31)||source_modified||char(31)||observed_at FROM video_nfo_evidence WHERE library_id='basic-library' AND asset_id='basic-asset'),
 (SELECT status||char(31)||issue FROM video_nfo_status WHERE library_id='basic-library' AND asset_id='basic-asset')`).Scan(&s.changes, &s.rows, &s.row, &s.status); err != nil {
		t.Fatal(err)
	}
	return s
}

// TestBasicNFOFenceRefusalsHaveNoNativeEffect proves that the shared native
// fence, not a source struct/golden/pin check, is what rejects stale evidence.
// The source is prepared from the exact valid full before/after cohort first.
// The normal helper turn then captures the initial state, the native source
// rows are mutated inside that SAME actual transaction, and the body is called.
// Each case must refuse with ErrConflict and ZERO progress while total_changes
// and the actual NFO row/status remain exactly as the mutator left them.
func TestBasicNFOFenceRefusalsHaveNoNativeEffect(t *testing.T) {
	apply := func(ctx context.Context, turn *commandTurn, in *basicNFOSet) (basicNFOProgress, error) {
		return applyBasicNFO(ctx, turn, in, 2000)
	}
	applyPhase3 := func(ctx context.Context, turn *commandTurn, in *basicNFOSet) (basicNFOProgress, error) {
		return replaceBasicNFOEvidenceForAccept(ctx, turn, in, 2000)
	}
	cases := []struct {
		name       string
		statements []string
		call       func(context.Context, *commandTurn, *basicNFOSet) (basicNFOProgress, error)
		native     string
		wantNative int64
	}{
		{"stale_asset_info_epoch", []string{`UPDATE catalog_assets SET info_epoch=18 WHERE id=41`}, apply, `SELECT info_epoch FROM catalog_assets WHERE id=41`, 18},
		{"stale_change_input_revision", []string{`UPDATE catalog_info_changes SET input_revision=8 WHERE id=51`}, apply, `SELECT input_revision FROM catalog_info_changes WHERE id=51`, 8},
		{"wrong_native_phase", []string{`UPDATE catalog_info_changes SET phase=1 WHERE id=51`}, apply, `SELECT phase FROM catalog_info_changes WHERE id=51`, 1},
		{"phase3_stale_asset_epoch", []string{`UPDATE catalog_info_changes SET phase=3 WHERE id=51`, `UPDATE catalog_assets SET info_epoch=18 WHERE id=41`}, applyPhase3, `SELECT info_epoch FROM catalog_assets WHERE id=41`, 18},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, source := basicStreamFixture(t)
			old := seedBasicNFO(t, w)
			after := basicNFORow{"locator-2", "movie", "[null,null,null]", "digest-2", `{"title":"Arrival"}`, 42, -11, ""}
			prepared, err := prepareBasicNFOSet(source, "basic-library", []basicNFORow{old}, []basicNFORow{after}, &basicNFOStatus{"observed", ""}, basicNFOStatus{"observed", ""}, true)
			if err != nil {
				t.Fatal(err)
			}
			turn := basicProbeTestTurn(t, w, basicNFOTestDefs())
			// Capture FIRST, on the exact normal helper turn, before touching the
			// native source state.
			initial := basicNFOFenceSnapshotOf(t, turn)
			for _, statement := range tc.statements {
				if _, err := turn.transaction.conn.ExecContext(t.Context(), statement); err != nil {
					t.Fatal(err)
				}
			}
			var native int64
			if err := turn.transaction.conn.QueryRowContext(t.Context(), tc.native).Scan(&native); err != nil {
				t.Fatal(err)
			}
			if native != tc.wantNative {
				t.Fatalf("source mutation is not visible in the same transaction: got %d want %d", native, tc.wantNative)
			}
			// Snapshot AFTER the mutator and BEFORE the body. The mutator owns
			// exactly its own row writes; no NFO evidence/status effect is allowed.
			beforeCall := basicNFOFenceSnapshotOf(t, turn)
			if beforeCall.changes != initial.changes+int64(len(tc.statements)) || beforeCall.rows != initial.rows || beforeCall.row != initial.row || beforeCall.status != initial.status {
				t.Fatalf("mutator changed NFO effects: initial=%+v beforeCall=%+v", initial, beforeCall)
			}
			got, err := tc.call(t.Context(), turn, prepared)
			if !errors.Is(err, ErrConflict) || got != (basicNFOProgress{}) {
				t.Fatalf("stale native source was not refused with zero progress: %+v %v", got, err)
			}
			afterCall := basicNFOFenceSnapshotOf(t, turn)
			if afterCall != beforeCall {
				t.Fatalf("refusal wrote native NFO effects before rollback: before=%+v after=%+v", beforeCall, afterCall)
			}
			_ = turn.closeRows()
			if _, rollbackErr := turn.transaction.rollback(err); !errors.Is(rollbackErr, ErrConflict) {
				t.Fatalf("rollback lost the native conflict: %v", rollbackErr)
			}
			// The mutator was in the same transaction, so rollback restores the
			// fixture's original native values with the seeded NFO cohort intact.
			var restored int64
			if tc.name == "stale_change_input_revision" {
				if err := w.db.QueryRowContext(t.Context(), `SELECT input_revision FROM catalog_info_changes WHERE id=51`).Scan(&restored); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := w.db.QueryRowContext(t.Context(), `SELECT info_epoch FROM catalog_assets WHERE id=41`).Scan(&restored); err != nil {
					t.Fatal(err)
				}
			}
			wantRestored := int64(17)
			if tc.name == "stale_change_input_revision" {
				wantRestored = 7
			}
			if restored != wantRestored {
				t.Fatalf("rollback did not restore the native source: %d", restored)
			}
			var rows int64
			if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM video_nfo_evidence`).Scan(&rows); err != nil || rows != 1 {
				t.Fatalf("rollback changed the seeded NFO cohort: %d %v", rows, err)
			}
		})
	}
}
