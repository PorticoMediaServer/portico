package writer

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Actual Capture, exact-purpose grant and SAME frame, with a deliberately
// synthetic component recipe. No dispatcher/current-authority/preparation
// issuer or production/physical qualification is supplied by this fixture.
func basicHandlerTurn(t *testing.T, w *executor, kind CommandID, defs []stmt.Definition, registries ...*stmt.Registry) *commandTurn {
	t.Helper()
	var registry *stmt.Registry
	if len(registries) == 1 {
		registry = registries[0]
	} else if len(registries) == 0 {
		registry = nativeInventoryFixtureRegistry(t, w, defs, "basic_handlers_test.go", "basic_control_test.go", "basic_stage_test.go")
	} else {
		t.Fatal("one captured startup registry only")
	}
	recipe := turnRecipe{limit: Work{65536, 1000000, 4096, 32 << 20, 25 * time.Millisecond}, setup: Work{16, 500, 16, 65536, time.Millisecond}, terminal: Work{16, 500, 16, 65536, 8 * time.Millisecond}}
	g, authority, wal, request := governedFixture(t, w.now, governedFixtureProfile{recipe, 8 << 20})
	request.Key.Domain, request.Key.ID = uint16(kind), "basic-run"
	request.Footprint = []governor.Scope{{Library: 7, Domain: uint16(kind)}}
	if err := wal.Publish(wal.Observation(), governor.WALState{PageSize: 4096}, w.now()); err != nil {
		t.Fatal(err)
	}
	decision := g.Try(request)
	if decision.Grant == nil {
		t.Fatal(decision.Refusal)
	}
	custody, err := bindGovernedTurn(kind, recipe.limit, g, authority, wal, decision.Grant, request)
	if err != nil {
		t.Fatal(errors.Join(err, authority.Retired(decision.Grant)))
	}
	custody.basicMemory = commandMemoryBudget{4 << 20, 4 << 20}
	tx, err := w.begin(t.Context(), kind, recipe, custody)
	if err != nil {
		if errors.Is(err, ErrBusy) {
			err = errors.Join(err, custody.retired())
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { tx.rollback(nil) })
	steps := make([]statementStep, 0, len(defs))
	for _, def := range defs {
		rows := uint64(16)
		for family := int64(2); family <= 9; family++ {
			if def.ID == basicMediaRowsID(family) {
				rows = uint64(basicMediaFamilyLimit(family) + 1)
			}
		}
		steps = append(steps, statementStep{def.ID, Work{rows, 10000, 16, 65536, 25 * time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	return turn
}

func basicHandlerCommit(t *testing.T, turn *commandTurn, purpose BasicPurpose, lease int64) {
	t.Helper()
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if err := checkBasicTerminal(t.Context(), turn, purpose, lease); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
}

// Fixture-only prerequisite between originals: the real common ACK adapter is
// still separately owned. Read the exact successful result/run values, not a
// fabricated replacement receipt or fresh operation with another identity.
func basicHandlerFloor(t *testing.T, w *executor, original *basicOriginalScope, identity *CommandIdentity, result BasicResult) {
	t.Helper()
	changed, err := w.db.ExecContext(t.Context(), `UPDATE inventory_runs SET acknowledged_command_sequence=? WHERE job_id=? AND job_revision=? AND last_command_sequence=? AND acknowledged_command_sequence=?`, identity.Sequence, original.scope.runID, result.AcceptedRunRevision, identity.Sequence, original.run.acknowledgedSequence)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := changed.RowsAffected(); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	original.run.revision, original.run.lastSequence, original.run.acknowledgedSequence = result.AcceptedRunRevision, identity.Sequence, identity.Sequence
	identity.Sequence++
	identity.OperationID[0]++
}

func TestBasicHandlerBeginSavesActualAllocatedNativeResult(t *testing.T) {
	w, source, original, identity := basicControlFixture(t)
	seedBasicFirstOrdinaryOwner(t, w)
	for _, q := range []string{`DELETE FROM catalog_info_input_families`, `DELETE FROM catalog_info_input_witnesses`, `DELETE FROM catalog_info_changes`} {
		if _, err := w.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	source.changeID, source.inputRevision = 0, 0
	in := basicStageRequest{original: original, purpose: BasicPurposeStage, operation: basicStageBeginInput,
		begin: basicBeginEffects{source: source, shape: ScanShape{LibraryID: 7, OrdinaryOwners: 1}, counts: [9]int64{1, 0, 0, 1, 0, 1, 0, 0, 1}}}
	command, err := newBasicStageCommand(identity, in)
	if err != nil {
		t.Fatal(err)
	}
	turn := basicHandlerTurn(t, w, BasicStage, basicStageCommandDefinitions(command.request))
	result, err := applyBasicStage(t.Context(), turn, command)
	if err != nil || result.Outcome != (ReceiptOutcome{}) || result.AcceptedRunRevision != 42 || result.ChangeID <= 0 || result.ExpectedEpoch != 17 || result.SourceRevision != "object:019" || result.Continuation != (BasicContinuation{}) || result.ResultCount != 0 || result.Members == nil || len(result.Members) != 0 || result.Digest == (ResultDigest{}) {
		t.Fatal(result, err)
	}
	var exact bool
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT
 (SELECT id=? AND job_revision=1 AND input_revision=1 AND expected_epoch=17 AND created_ms=? FROM catalog_info_changes WHERE asset_id=41)
 AND (SELECT count(*)=9 FROM catalog_info_input_families WHERE change_id=?)
 AND (SELECT change_id=? AND accepted_revision=42 AND expected_epoch=17 AND source_revision='object:019' AND command_kind='basic_stage' AND result_digest=? FROM scan_command_receipts WHERE run_id='basic-run' AND command_sequence=19)
 AND (SELECT info_epoch=17 AND info_publication_pending=0 FROM catalog_assets WHERE id=41)`, result.ChangeID, result.AppliedMS, result.ChangeID, result.ChangeID, result.Digest[:]).Scan(&exact); err != nil || !exact {
		t.Fatal(exact, err)
	}
	basicHandlerCommit(t, turn, BasicPurposeStage, original.run.leaseExpiresMS)
}

func TestBasicHandlerMediaStageAndSealKeepActualReceipts(t *testing.T) {
	w, source, original, identity := basicControlFixture(t)
	for _, q := range []string{`UPDATE catalog_info_changes SET family_mask=511 WHERE id=51`, `INSERT INTO catalog_info_input_families(change_id,family,witness_ordinal,rows_expected) VALUES(51,4,0,1),(51,5,0,2)`} {
		if _, err := w.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	// Capture the full finite recipe once on FULL startup. Later NORMAL turns
	// reuse its actual receipt; they cannot pretend to be a second startup.
	all := append(basicControlCommandDefinitions(), basicMediaDefinitions()...)
	registry := nativeInventoryFixtureRegistry(t, w, all, "basic_handlers_test.go", "basic_control_test.go", "basic_stage_test.go")
	// The native chapter child FK requires its actual staged family4 parent.
	// Publish that parent through the SAME Stage body before any chapter row;
	// the failed first fixture omitted it and correctly hit native FK787.
	parent, err := newBasicStageCommand(identity, basicStageRequest{original: original, purpose: BasicPurposeStage, operation: basicStageMediaInput, fact: source, changeRevision: 1,
		rows: basicRowsRequest{family: 4, rawChapterCount: 2, chapterFacts: []basicChapterFact{{37, "known", "chapters:37"}}}, start: 0, end: 1})
	if err != nil {
		t.Fatal(err)
	}
	turn := basicHandlerTurn(t, w, BasicStage, basicStageCommandDefinitions(parent.request), registry)
	parentResult, err := applyBasicStage(t.Context(), turn, parent)
	if err != nil || parentResult.AcceptedRunRevision != 42 || parentResult.Outcome != (ReceiptOutcome{}) {
		t.Fatal(parentResult, err)
	}
	basicHandlerCommit(t, turn, BasicPurposeStage, original.run.leaseExpiresMS)
	basicHandlerFloor(t, w, &original, &identity, parentResult)
	rows := basicRowsRequest{family: 5, rawChapterCount: 2, chapters: []basicChapter{{0, "Opening", 0, 3}, {1, "Ending", 3, 7}}}
	stage, err := newBasicStageCommand(identity, basicStageRequest{original: original, purpose: BasicPurposeStage, operation: basicStageMediaInput, fact: source, changeRevision: 1, rows: rows, start: 0, end: 2})
	if err != nil {
		t.Fatal(err)
	}
	turn = basicHandlerTurn(t, w, BasicStage, basicStageCommandDefinitions(stage.request), registry)
	staged, err := applyBasicStage(t.Context(), turn, stage)
	if err != nil || staged.Outcome != (ReceiptOutcome{}) || staged.AcceptedRunRevision != 43 || staged.Continuation != (BasicContinuation{}) || staged.ExpectedEpoch != 17 {
		t.Fatal(staged, err)
	}
	basicHandlerCommit(t, turn, BasicPurposeStage, original.run.leaseExpiresMS)
	basicHandlerFloor(t, w, &original, &identity, staged)
	seal, err := newBasicSealCommand(identity, basicSealRequest{original: original, purpose: BasicPurposeSeal, operation: basicSealMediaInput, source: source, changeRevision: 1, rows: rows})
	if err != nil {
		t.Fatal(err)
	}
	turn = basicHandlerTurn(t, w, BasicSeal, basicSealCommandDefinitions(seal.request), registry)
	sealed, err := applyBasicSeal(t.Context(), turn, seal)
	if err != nil || sealed.Outcome != (ReceiptOutcome{}) || sealed.AcceptedRunRevision != 44 || sealed.ChangeID != 51 || sealed.ExpectedEpoch != 17 || sealed.SourceRevision != "object:019" || sealed.Continuation != (BasicContinuation{}) || sealed.ResultCount != 0 || sealed.Members == nil || len(sealed.Members) != 0 || sealed.Digest == staged.Digest {
		t.Fatal(sealed, err)
	}
	var exact bool
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT
 (SELECT rows_expected=2 AND rows_staged=2 AND sealed=1 FROM catalog_info_input_families WHERE change_id=51 AND family=5)
 AND (SELECT count(*)=2 FROM catalog_info_new_chapters WHERE change_id=51)
 AND (SELECT count(*)=3 FROM scan_command_receipts WHERE run_id='basic-run')
 AND (SELECT accepted_revision=44 AND source_revision='object:019' AND expected_epoch=17 AND result_digest=? AND command_kind='basic_seal' FROM scan_command_receipts WHERE run_id='basic-run' AND command_sequence=21)
 AND (SELECT info_epoch=17 AND info_publication_pending=0 FROM catalog_assets WHERE id=41)
 AND (SELECT discovered=59 AND analyzed=43 AND warnings=47 FROM inventory_runs WHERE job_id='basic-run')
 AND (SELECT processed=67 FROM jobs WHERE id='basic-run')
 AND (SELECT count(*)=0 FROM scan_command_results)`, sealed.Digest[:]).Scan(&exact); err != nil || !exact {
		t.Fatal(exact, err)
	}
	basicHandlerCommit(t, turn, BasicPurposeSeal, original.run.leaseExpiresMS)
}

func TestBasicHandlerLateReceiptRefusalClearsAndRollsBack(t *testing.T) {
	w, source, original, identity := basicControlFixture(t)
	if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_info_changes SET family_mask=511 WHERE id=51`); err != nil {
		t.Fatal(err)
	}
	stage, err := newBasicStageCommand(identity, basicStageRequest{original: original, purpose: BasicPurposeStage, operation: basicStageFactInput, fact: source, changeRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	turn := basicHandlerTurn(t, w, BasicStage, basicStageCommandDefinitions(stage.request))
	delete(turn.steps, basicControlReceipt) // precise late refusal after native stage+run CAS
	result, err := applyBasicStage(t.Context(), turn, stage)
	if err == nil || !reflect.DeepEqual(result, BasicResult{}) {
		t.Fatal(result, err)
	}
	var revision int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT job_revision FROM inventory_runs WHERE job_id='basic-run'`).Scan(&revision); err != nil || revision != 42 {
		t.Fatal("fault was not after native run CAS", revision, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	var exact bool
	if err := w.db.QueryRowContext(t.Context(), `SELECT
 (SELECT job_revision=41 AND last_command_sequence=18 AND acknowledged_command_sequence=18 FROM inventory_runs WHERE job_id='basic-run')
 AND (SELECT count(*)=0 FROM catalog_info_new_stream_facts)
 AND (SELECT rows_staged=0 AND sealed=0 FROM catalog_info_input_families WHERE change_id=51 AND family=1)
 AND (SELECT count(*)=0 FROM scan_command_receipts)
 AND (SELECT used_bytes=1000 FROM catalog_info_input_budget)`).Scan(&exact); err != nil || !exact {
		t.Fatal(exact, err)
	}
}
