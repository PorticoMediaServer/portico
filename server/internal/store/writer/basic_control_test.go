package writer

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func basicControlFixture(t *testing.T) (*executor, basicStreamFactStage, basicOriginalScope, CommandIdentity) {
	t.Helper()
	w, source := basicStreamFixture(t)
	identity := CommandIdentity{Incarnation: [16]byte(bytes.Repeat([]byte{1}, 16)), Sequence: 19, OperationID: [16]byte(bytes.Repeat([]byte{2}, 16))}
	original := basicOriginalScope{scope: inventoryScope{source.runID, "basic-library", source.libraryID},
		source: inventorySource{id: source.sourceID, incarnation: source.incarnation, rootIdentity: "root:17", resolvedRoot: []byte("/media"), generation: source.sourceGeneration},
		run:    inventoryRun{revision: 41, policyRevision: source.policyRevision, leaseOwner: 13, leaseGeneration: 17, leaseExpiresMS: 4000000000000, lastSequence: 18, acknowledgedSequence: 18, phase: "inventory", scanMode: "first-import"}}
	for _, sql := range []string{
		`INSERT INTO library_sources(id,library_id,configured_root,root,incarnation,root_identity,generation,identity_confirmed) VALUES('basic-source','basic-library','/media','/media','basic-incarnation','root:17',23,1)`,
		`INSERT INTO library_scan_policies(library_id,revision) VALUES('basic-library',29)`,
		`INSERT INTO jobs(id,library_id,status,created_at,processed) VALUES('basic-run','basic-library','running','2026-11-07T00:00:00Z',67)`,
		`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,seen_job) VALUES('basic-object','basic-source','basic-asset','basic-incarnation','Arrival.mkv','object:019','{}',42,-11,'basic-run')`,
	} {
		if _, err := w.db.ExecContext(t.Context(), sql); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO inventory_runs(job_id,original_library_id,original_library_key,original_resolved_root,original_follow_symlinks,create_operation_id,create_request_digest,source_id,source_generation,root_incarnation,root_identity,policy_revision,scan_mode,job_lease_owner,job_lease_generation,job_lease_expires_ms,job_revision,command_incarnation,last_command_sequence,acknowledged_command_sequence,discovered,analyzed,warnings)
VALUES('basic-run',7,'basic-library','/media',0,?,?,'basic-source',23,'basic-incarnation','root:17',29,'first-import',13,17,4000000000000,41,?,18,18,59,43,47)`, bytes.Repeat([]byte{5}, 16), bytes.Repeat([]byte{6}, 32), identity.Incarnation[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO inventory_source_active VALUES('basic-source','basic-run')`); err != nil {
		t.Fatal(err)
	}
	return w, source, original, identity
}

func basicControlTurn(t *testing.T, w *executor) *commandTurn {
	t.Helper()
	// Component SQL facts only, not a BasicStage/Seal runtime or funded profile.
	tx, err := w.begin(t.Context(), BasicAccept, testRecipe(), &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	config := w.config
	config.Synchronous = sqliteconn.Normal
	defs := append(basicStreamStageDefinitions(), basicControlDefinitions()...)
	golden := captureEffectiveFixture(t, tx.conn, config, defs, "basic_control_test.go")
	registry, err := stmt.New(defs, golden.Receipt())
	if err != nil {
		t.Fatal(err)
	}
	vm := make(map[stmt.ID]uint64)
	for _, p := range golden.Plans {
		vm[p.ID] = max(vm[p.ID], 2*uint64(len(p.Explain.Values)))
	}
	var steps []statementStep
	for _, d := range defs {
		// Synthetic component ledger only: real compiled VM envelope, no
		// read WAL, and a virtual Hold charge. Not a production profile.
		work := Work{Rows: 4, VMOps: vm[d.ID], Pages: 1, Hold: 100 * time.Microsecond}
		if d.Kind == stmt.Write {
			work.Pages, work.WALBytes = 4, 4096
		}
		steps = append(steps, statementStep{d.ID, work})
	}
	turn, err := newCommandTurn(tx, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = turn.closeRows() })
	return turn
}

func TestBasicControlNativeRunAndReceiptAreOneUnit(t *testing.T) {
	w, source, original, identity := basicControlFixture(t)
	turn := basicControlTurn(t, w)
	facts, err := readBasicFreshFacts(t.Context(), turn, identity, original, source)
	if err != nil || facts != (basicFreshFacts{41, 59, 43, 47, 67}) {
		t.Fatal(facts, err)
	}
	if _, err = stageBasicStreamFact(t.Context(), turn, source); err != nil {
		t.Fatal(err)
	}
	continuation, err := readBasicControlPostState(t.Context(), turn, source)
	if err != nil || continuation != (BasicContinuation{}) {
		t.Fatal(continuation, err)
	}
	runRevision, err := advanceBasicControlRun(t.Context(), turn, identity, original, facts)
	if err != nil || runRevision != 42 {
		t.Fatal(runRevision, err)
	}
	// Synthetic SQL fixture digest ONLY; W's closed Basic codec/dispatcher is
	// absent. This test is not an Applied or historical-resolution proof.
	result := BasicResult{Purpose: BasicPurposeStage, RunID: source.runID, AcceptedRunRevision: runRevision, AppliedMS: 2000,
		SourceID: source.sourceID, SourceIncarnation: source.incarnation, SourceRevision: source.objectRevision,
		ChangeID: source.changeID, ExpectedEpoch: source.expectedEpoch, Continuation: continuation,
		Members: []InventoryMember{}, Digest: ResultDigest{4}}
	if err = writeBasicControlReceipt(t.Context(), turn, identity, RequestDigest{3}, result); err != nil {
		t.Fatal(err)
	}
	if err = turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	var exact bool
	if err = w.db.QueryRowContext(t.Context(), `SELECT
 (SELECT job_revision=42 AND last_command_sequence=19 AND acknowledged_command_sequence=18 AND discovered=59 AND analyzed=43 AND warnings=47 FROM inventory_runs WHERE job_id='basic-run')
 AND (SELECT processed=67 FROM jobs WHERE id='basic-run')
 AND (SELECT accepted_revision=42 AND source_revision='object:019' AND change_id=51 AND expected_epoch=17 AND result_count=0 AND next_family=0 FROM scan_command_receipts WHERE run_id='basic-run')
 AND (SELECT count(*)=0 FROM scan_command_results)
 AND (SELECT info_epoch=17 AND info_publication_pending=0 FROM catalog_assets WHERE id=41)`).Scan(&exact); err != nil || !exact {
		t.Fatal("control changed availability/progress or aliased revisions", exact, err)
	}
}

func TestBasicControlLateReceiptFailureRollsBackRunAndRows(t *testing.T) {
	w, source, original, identity := basicControlFixture(t)
	turn := basicControlTurn(t, w)
	facts, err := readBasicFreshFacts(t.Context(), turn, identity, original, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = stageBasicStreamFact(t.Context(), turn, source); err != nil {
		t.Fatal(err)
	}
	if _, err = advanceBasicControlRun(t.Context(), turn, identity, original, facts); err != nil {
		t.Fatal(err)
	}
	delete(turn.steps, basicControlReceipt)
	err = writeBasicControlReceipt(t.Context(), turn, identity, RequestDigest{3}, BasicResult{Purpose: BasicPurposeSeal, RunID: source.runID,
		AcceptedRunRevision: 42, AppliedMS: 2000, SourceID: source.sourceID, SourceIncarnation: source.incarnation, SourceRevision: source.objectRevision,
		ChangeID: 51, ExpectedEpoch: 17, Members: []InventoryMember{}, Digest: ResultDigest{4}})
	if err == nil {
		t.Fatal("missing receipt step accepted")
	}
	_ = turn.closeRows()
	if _, rollbackErr := turn.transaction.rollback(err); !errors.Is(rollbackErr, err) {
		t.Fatal(rollbackErr)
	}
	var exact bool
	if err = w.db.QueryRowContext(t.Context(), `SELECT
 (SELECT job_revision=41 AND last_command_sequence=18 FROM inventory_runs WHERE job_id='basic-run')
 AND (SELECT used_bytes=1000 FROM catalog_info_input_budget WHERE singleton=1)
 AND (SELECT rows_staged=0 AND sealed=0 FROM catalog_info_input_families WHERE change_id=51 AND family=1)
 AND (SELECT count(*)=0 FROM catalog_info_new_stream_facts)
 AND (SELECT count(*)=0 FROM scan_command_receipts)`).Scan(&exact); err != nil || !exact {
		t.Fatal(exact, err)
	}
}
