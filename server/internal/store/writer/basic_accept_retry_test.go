package writer

import (
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

func TestBasicAcceptClearsResumeAttemptsBeforeRetirement(t *testing.T) {
	walk := basicResumeWalk(t)
	// Like the other complete-walk fixtures, Begin's header/witness/manifests
	// are seeded. Fund their actual logical charge before testing retirement.
	fixed, err := basicAbandonHeaderCharge(walk.s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = walk.e.db.ExecContext(t.Context(), `UPDATE catalog_info_input_budget SET used_bytes=used_bytes+? WHERE singleton=1`, fixed); err != nil {
		t.Fatal(err)
	}
	got, err := walk.chain.stage(basicControlRequest{operation: basicControlResume, source: walk.s})
	if err != nil {
		t.Fatal(err)
	}
	revision := walk.revision + 1
	for n := 0; got.Continuation.NextFamily != 0; n++ {
		if n > 32 {
			t.Fatal("ordinary copy did not drain")
		}
		c := got.Continuation
		got, err = walk.chain.stage(basicControlRequest{operation: basicControlCopyFamily, source: walk.s, family: c.NextFamily, changeRevision: revision, entityID: 31, shapeID: walk.shape,
			copy: basicCopyCursor{family: c.NextFamily, phase: c.NextCopyPhase, id: c.NextCopyID, aux: c.NextCopyAux, key: c.NextCopyKey, key2: c.NextCopyKey2, key3: c.NextCopyKey3}})
		if err != nil {
			t.Fatal("ordinary copy after resume", err)
		}
		revision++
	}
	var phase, attempts int64
	if err = walk.e.db.QueryRowContext(t.Context(), `SELECT phase,job_attempts FROM catalog_info_changes WHERE id=51`).Scan(&phase, &attempts); err != nil || phase != 3 || attempts != 1 {
		t.Fatal("mixed path did not reproduce retained attempts", phase, attempts, err)
	}
	basicResumeAssertHidden(t, walk)
	// An already completed copy may still carry a due retry. Accept owns the
	// idle handoff even when the producer does not issue another resume first.
	if _, err = walk.e.db.ExecContext(t.Context(), `UPDATE catalog_info_changes SET job_next_attempt_ms=1 WHERE id=51`); err != nil {
		t.Fatal(err)
	}
	accepted, err := basicWalkAccept(t, walk, revision)
	if err != nil || accepted.Outcome.State != Applied {
		t.Fatal("mixed-path Accept", accepted, err)
	}
	var idle bool
	if err = walk.e.db.QueryRowContext(t.Context(), `SELECT phase=5 AND job_attempts=0 AND job_next_attempt_ms IS NULL AND job_revision=? FROM catalog_info_changes WHERE id=51`, revision+2).Scan(&idle); err != nil || !idle {
		t.Fatal("published header stranded", idle, err)
	}
	basicDispatchReturned(t, walk.e, walk.authority)

	// Reuse the existing bounded native media-prefix cleanup bodies, then run
	// the unchanged final retirement on THIS accepted header. This component
	// proof adds no production retirement/abandon dispatch or authorization.
	defs := basicRetireFirstTestDefinitions()
	for _, d := range basicAbandonDefinitions() {
		for n := 0; n < 8; n++ {
			if d.ID == basicAbandonReadID(n) || d.ID == basicAbandonDeleteID(n) {
				defs = append(defs, d)
			}
		}
	}
	walk.e.config.Synchronous = sqliteconn.Normal
	turn := basicRetireFirstComponentTurn(t, walk.e, defs)
	var refund int64
	for n := 0; n < 8; n++ {
		charge, rows, err := reclaimBasicAbandonedTable(t.Context(), turn, walk.s, n, 8)
		if err != nil || rows != 1 {
			t.Fatal("bounded staged-prefix cleanup", n, rows, err)
		}
		refund += charge
	}
	row, err := basicStageRead(t.Context(), turn, basicStageBudget, int64(1))
	if err != nil {
		t.Fatal(err)
	}
	var used, cap int64
	if err = row.Scan(&used, &cap); err != nil {
		t.Fatal(err)
	}
	row, err = basicStageRead(t.Context(), turn, basicStageBudgetCAS, used-refund, int64(1), used, cap)
	if err != nil {
		t.Fatal(err)
	}
	var actual int64
	if err = row.Scan(&actual); err != nil || actual != used-refund {
		t.Fatal("prefix refund", actual, err)
	}
	retired, err := retireBasicFirstInput(t.Context(), turn, basicRetireFirstEffects{source: walk.s, currentAssetEpoch: walk.s.expectedEpoch + 1, changeRevision: revision + 2, shapeID: walk.shape, entityID: 31, counts: [9]int64{1, 1, 1, 1, 1, 1, 1, 1, 1}})
	if err != nil || retired.deleted != 12 {
		t.Fatal("mixed-path final retirement", retired, err)
	}
	basicMediaCommit(t, turn)
	var intact bool
	if err = walk.e.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*)=0 FROM catalog_info_changes WHERE id=51) AND (SELECT used_bytes=? FROM catalog_info_input_budget WHERE singleton=1) AND (SELECT ref_count=1 FROM catalog_info_shapes WHERE id=?) AND (SELECT info_epoch=18 AND available=1 AND info_publication_pending=0 FROM catalog_assets WHERE id=41) AND (SELECT count(*)=1 FROM asset_streams WHERE asset_id='basic-asset') AND (SELECT info_pending_change IS NULL AND info_shape_id=? FROM catalog_asset_links WHERE entity_id=31 AND asset_id=41)`, basicAbandonTestEndDebt, walk.shape, walk.shape).Scan(&intact); err != nil || !intact {
		t.Fatal("retirement touched serving state or stranded debt", intact, err)
	}
}

func TestBasicAcceptRetryIdleFailureRollsBackPublication(t *testing.T) {
	w, command := basicFirstAcceptCommandFixture(t)
	if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_info_changes SET job_attempts=3,job_next_attempt_ms=1 WHERE id=51`); err != nil {
		t.Fatal(err)
	}
	defs := basicFirstAcceptTestDefinitions()
	for n := range defs {
		if defs[n].ID == basicAcceptRetryIdle {
			defs[n].SQL = strings.Replace(defs[n].SQL, "RETURNING id,job_revision,job_attempts,job_next_attempt_ms", "RETURNING job_revision,id,job_attempts,job_next_attempt_ms", 1)
		}
	}
	turn := basicHandlerTurn(t, w, BasicAccept, defs)
	got, err := applyBasicAccept(t.Context(), turn, command)
	if !errors.Is(err, ErrConflict) || got.Outcome.State == Applied {
		t.Fatal("bad cleanup echo accepted", got, err)
	}
	var phase, attempts int64
	if readErr := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT phase,job_attempts FROM catalog_info_changes WHERE id=51`).Scan(&phase, &attempts); readErr != nil || phase != 5 || attempts != 0 {
		t.Fatal("late cleanup cut was not reached", phase, attempts, readErr)
	}
	if closeErr := turn.closeRows(); closeErr != nil {
		t.Fatal(closeErr)
	}
	_, _ = turn.transaction.rollback(err)
	var restored bool
	if readErr := w.db.QueryRowContext(t.Context(), `SELECT (SELECT phase=3 AND job_revision=13 AND job_attempts=3 AND job_next_attempt_ms=1 FROM catalog_info_changes WHERE id=51) AND (SELECT info_epoch=17 AND info_publication_pending=1 FROM catalog_assets WHERE id=41) AND (SELECT info_pending_change=51 FROM catalog_asset_links WHERE entity_id=31 AND asset_id=41) AND (SELECT count(*)=6 FROM catalog_info_scalar_old WHERE change_id=51) AND (SELECT job_revision=41 AND last_command_sequence=18 FROM inventory_runs WHERE job_id='basic-run') AND (SELECT count(*)=0 FROM scan_command_receipts WHERE run_id='basic-run')`).Scan(&restored); readErr != nil || !restored {
		t.Fatal("cleanup failure leaked publication or retry reset", restored, readErr)
	}
}

func TestBasicRetireFirstNonzeroAttemptsRefuseBeforeEffects(t *testing.T) {
	_, turn, in, _ := basicRetireFirstFixture(t, `UPDATE catalog_info_changes SET job_attempts=1 WHERE id=51`, "")
	var before, after int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	got, err := retireBasicFirstInput(t.Context(), turn, in)
	if !errors.Is(err, ErrConflict) || got != (basicRetiredFirstInput{}) {
		t.Fatal("busy retirement header accepted", got, err)
	}
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil || after != before {
		t.Fatal("attempts guard came after retirement effects", before, after, err)
	}
}
