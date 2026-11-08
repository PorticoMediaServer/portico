package writer

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// A staged but uncommitted command: the caller owns the receipt/ACK and commit.
func mgGuideStagePending(t *testing.T, w *executor, before mgGuideNative) (*commandTurn, mgGuideNativeResult, CommandIdentity, mgGuideFrozenStage) {
	t.Helper()
	stages := mgGuideFrozenFixture(t)
	stage := stages[0]
	identity := mgGuideIdentityAfter(before)
	turn := mgGuideTestTurn(t, w, mgGuidePurposeFor(stage.family), mgGuideDefinitions())
	result, err := mgStageGuideNative(t.Context(), turn, before, identity, stage)
	if err != nil {
		t.Fatal(err)
	}
	return turn, result, identity, stage
}

func mgGuideReceiptZero(t *testing.T, r mgGuideReceipt, found bool, err error) {
	t.Helper()
	if err == nil || found || r != (mgGuideReceipt{}) {
		t.Fatal("failure leaked a receipt", r, found, err)
	}
}

func TestMGGuideReceiptSaveReplayAndProjectionFence(t *testing.T) {
	w, b, _ := mgGuideBirthFixture(t, false)
	before := mgGuideBeginCommitted(t, w, b)
	turn, result, identity, _ := mgGuideStagePending(t, w, before)
	original, err := mgSaveGuideReceipt(t.Context(), turn, result, identity, RequestDigest{byte(identity.Sequence)}, mgGuideReceiptChannels, mgGuideProjectionFixture())
	if err != nil {
		t.Fatal(err)
	}
	if original.source != result.head.source || original.sourceRevision != result.head.revision || original.sourceState != result.head.state ||
		original.sourceName != result.operation.name || original.tuner != result.operation.tuner ||
		original.staged != result.operation.progress.staged || original.chain != result.operation.progress.chain ||
		original.revision != result.operation.progress.revision || original.projection != mgGuideProjectionFixture() ||
		original.acknowledged != result.operation.acknowledged || original.generation != "new" {
		t.Fatal("receipt did not bind native facts", original)
	}
	var changes int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&changes); err != nil {
		t.Fatal(err)
	}
	replay, err := mgSaveGuideReceipt(t.Context(), turn, result, identity, RequestDigest{byte(identity.Sequence)}, mgGuideReceiptChannels, mgGuideProjectionFixture())
	if err != nil || replay != original {
		t.Fatal("exact replay", replay, err)
	}
	var after int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil || after != changes {
		t.Fatal("replay made effects", changes, after, err)
	}
	// Original history is owner/generation-owned: current metadata changes and
	// even deletion of today's owner/generation cannot rewrite the saved result.
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE live_sources SET name='Replacement',revision=revision+1 WHERE id='source'`); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE live_operations SET status='superseded',stage_revision=stage_revision+1 WHERE request_id='guide-request'`); err != nil {
		t.Fatal(err)
	}
	moved, err := mgLookupGuideReceipt(t.Context(), turn, original.key)
	if err != nil || !moved.found || moved.receipt != original {
		t.Fatal("replay rebuilt today's result", moved, err)
	}
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `DELETE FROM live_operations WHERE request_id='guide-request'`); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `DELETE FROM live_channel_versions WHERE generation_id='new'`); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `DELETE FROM live_generations WHERE id='new'`); err != nil {
		t.Fatal(err)
	}
	changed := mgGuideProjectionFixture()
	changed.examples[0] = "new diagnostic"
	stale := result
	stale.operation.name = "current metadata is irrelevant"
	replay, err = mgSaveGuideReceipt(t.Context(), turn, stale, identity, RequestDigest{byte(identity.Sequence)}, mgGuideReceiptChannels, changed)
	if err != nil || replay != original {
		t.Fatal("original history was rewritten", replay, err)
	}
}

func TestMGGuideReceiptIdentityConflicts(t *testing.T) {
	w, b, _ := mgGuideBirthFixture(t, false)
	before := mgGuideBeginCommitted(t, w, b)
	turn, result, identity, _ := mgGuideStagePending(t, w, before)
	r, err := mgSaveGuideReceipt(t.Context(), turn, result, identity, RequestDigest{byte(identity.Sequence)}, mgGuideReceiptChannels, mgGuideProjectionFixture())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"request digest", "operation", "kind", "sequence reuses operation"} {
		key := r.key
		switch name {
		case "request digest":
			key.requestDigest[0]++
		case "operation":
			key.identity.OperationID[0]++
		case "kind":
			key.kind = mgGuideReceiptSeal
		case "sequence reuses operation":
			key.identity.Sequence++
		}
		got, err := mgLookupGuideReceipt(t.Context(), turn, key)
		if !errors.Is(err, ErrConflict) {
			t.Fatal(name, err)
		}
		if got.found || got.receipt != (mgGuideReceipt{}) {
			t.Fatal(name, "lookup leaked a receipt")
		}
	}
	unknown := r.key
	unknown.identity.Sequence++
	unknown.identity.OperationID[0]++
	got, err := mgLookupGuideReceipt(t.Context(), turn, unknown)
	if err != nil || got.found || got.receipt != (mgGuideReceipt{}) {
		t.Fatal("unknown operation invented success", got, err)
	}
	altered := r.key
	altered.identity.OperationID[0]++
	if out, err := mgACKGuideReceipt(t.Context(), turn, result.operation, altered, r.resultDigest); err == nil || out != (mgGuideNative{}) {
		t.Fatal("unknown operation ACK succeeded", out, err)
	}
}

func TestMGGuideReceiptDiagnosticCompleteness(t *testing.T) {
	w, b, _ := mgGuideBirthFixture(t, false)
	before := mgGuideBeginCommitted(t, w, b)
	for _, name := range []string{"missing child", "changed child", "extra child", "wrong ordinal", "header count", "result digest"} {
		t.Run(name, func(t *testing.T) {
			turn, result, identity, _ := mgGuideStagePending(t, w, before)
			projection := mgGuideProjectionFixture()
			if name == "wrong ordinal" {
				projection.exampleCount = 1
				projection.examples[1] = ""
			}
			r, err := mgSaveGuideReceipt(t.Context(), turn, result, identity, RequestDigest{byte(identity.Sequence)}, mgGuideReceiptChannels, projection)
			if err != nil {
				t.Fatal(err)
			}
			var q string
			switch name {
			case "missing child":
				q = `DELETE FROM live_operation_receipt_diagnostics WHERE ordinal=0`
			case "changed child":
				q = `UPDATE live_operation_receipt_diagnostics SET message='tampered' WHERE ordinal=1`
			case "extra child":
				q = `INSERT INTO live_operation_receipt_diagnostics SELECT request_id,command_incarnation,command_sequence,2,'extra' FROM live_operation_command_receipts`
			case "wrong ordinal":
				q = `UPDATE live_operation_receipt_diagnostics SET ordinal=3 WHERE ordinal=0`
			case "header count":
				q = `UPDATE live_operation_command_receipts SET result_diagnostic_example_count=1`
			case "result digest":
				q = `UPDATE live_operation_command_receipts SET result_digest=zeroblob(32)`
			}
			if _, err := turn.transaction.conn.ExecContext(t.Context(), q); err != nil {
				t.Fatal(err)
			}
			got, err := mgLookupGuideReceipt(t.Context(), turn, r.key)
			mgGuideReceiptZero(t, got.receipt, got.found, err)
		})
	}
	t.Run("all ten bounded diagnostics", func(t *testing.T) {
		turn, result, identity, _ := mgGuideStagePending(t, w, before)
		p := mgGuideProjectionFixture()
		p.exampleCount = 10
		for i := range p.examples {
			p.examples[i] = strings.Repeat("x", i+1)
		}
		got, err := mgSaveGuideReceipt(t.Context(), turn, result, identity, RequestDigest{byte(identity.Sequence)}, mgGuideReceiptChannels, p)
		if err != nil || got.projection != p {
			t.Fatal(got, err)
		}
	})
}

func TestMGGuideReceiptACKAndRetirementFloor(t *testing.T) {
	w, b, _ := mgGuideBirthFixture(t, false)
	before := mgGuideBeginCommitted(t, w, b)
	turn, result, identity, _ := mgGuideStagePending(t, w, before)
	r, err := mgSaveGuideReceipt(t.Context(), turn, result, identity, RequestDigest{byte(identity.Sequence)}, mgGuideReceiptChannels, mgGuideProjectionFixture())
	if err != nil {
		t.Fatal(err)
	}
	wrong := r.resultDigest
	wrong[0]++
	if ack, err := mgACKGuideReceipt(t.Context(), turn, result.operation, r.key, wrong); err == nil || ack != (mgGuideNative{}) {
		t.Fatal("wrong digest ACK", ack, err)
	}
	ack, err := mgACKGuideReceipt(t.Context(), turn, result.operation, r.key, r.resultDigest)
	if err != nil || ack.acknowledged != r.key.identity.Sequence {
		t.Fatal(ack, err)
	}
	var changes int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&changes); err != nil {
		t.Fatal(err)
	}
	repeated, err := mgACKGuideReceipt(t.Context(), turn, ack, r.key, r.resultDigest)
	if err != nil || repeated != ack {
		t.Fatal("duplicate ACK", repeated, err)
	}
	var after int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil || after != changes {
		t.Fatal("duplicate ACK mutated", changes, after, err)
	}
	// A retirement floor is non-resurrection evidence; installing one is owned by
	// the retirement task, so this fixture inserts only its exact scalar row.
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `INSERT INTO live_operation_retirements(request_id,command_incarnation,create_operation_id,birth_request_digest,source_id,final_sequence,final_result_digest,retired_ms,last_command_sequence,acknowledged_command_sequence) VALUES('floor-request',?,?,?,'source',1,?,123,1,1)`, bytes.Repeat([]byte{1}, 16), bytes.Repeat([]byte{2}, 16), bytes.Repeat([]byte{3}, 32), bytes.Repeat([]byte{4}, 32)); err != nil {
		t.Fatal(err)
	}
	floor, err := mgReadGuideRetirementFloor(t.Context(), turn, "floor-request")
	if err != nil || !floor.found || floor.floor.request != "floor-request" || floor.floor.finalSequence != 1 {
		t.Fatal(floor, err)
	}
	if err := mgCheckGuideReceiptBirth(t.Context(), turn, "floor-request"); !errors.Is(err, ErrConflict) {
		t.Fatal("floored request reborn", err)
	}
	if err := mgCheckGuideReceiptBirth(t.Context(), turn, "unused-request"); err != nil {
		t.Fatal(err)
	}
	// Original receipt replay still wins over floor lookup; new lineage does not.
	if replay, err := mgSaveGuideReceipt(t.Context(), turn, result, identity, RequestDigest{byte(identity.Sequence)}, mgGuideReceiptChannels, mgGuideProjectionFixture()); err != nil || replay != r {
		t.Fatal("original replay after floor", replay, err)
	}
	if floor, err := mgReadGuideRetirementFloor(t.Context(), turn, before.request); err != nil || floor.found {
		t.Fatal("unexpected floor for live request", floor, err)
	}
}

func TestMGGuideReceiptNativeFactFences(t *testing.T) {
	w, b, _ := mgGuideBirthFixture(t, false)
	before := mgGuideBeginCommitted(t, w, b)
	for _, name := range []string{"native progress", "native head", "namespace", "ACK sequence", "future time", "invalid metadata"} {
		t.Run(name, func(t *testing.T) {
			turn, result, identity, _ := mgGuideStagePending(t, w, before)
			p := mgGuideProjectionFixture()
			digest := RequestDigest{byte(identity.Sequence)}
			switch name {
			case "native progress":
				result.operation.progress.bytes++
			case "native head":
				result.head.revision++
			case "namespace":
				identity.Incarnation[0]++
			case "ACK sequence":
				result.operation.acknowledged--
			case "future time":
				result.appliedMS++
			case "invalid metadata":
				p.exampleCount = 11
			}
			got, err := mgSaveGuideReceipt(t.Context(), turn, result, identity, digest, mgGuideReceiptChannels, p)
			if err == nil || got != (mgGuideReceipt{}) {
				t.Fatal("fabricated native facts accepted", name, got, err)
			}
			var n int64
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM live_operation_command_receipts WHERE command_sequence>=2`).Scan(&n); err != nil || n != 0 {
				t.Fatal("failed save left history", n, err)
			}
		})
	}
}

func TestMGGuideReceiptFailureWholeFrameRollback(t *testing.T) {
	w, b, _ := mgGuideBirthFixture(t, false)
	before := mgGuideBeginCommitted(t, w, b)
	for _, name := range []string{"receipt insert", "diagnostic insert"} {
		t.Run(name, func(t *testing.T) {
			defs := mgGuideDefinitions()
			for i := range defs {
				if name == "receipt insert" && defs[i].ID == mgGuideReceiptInsert {
					// Native positive-revision CHECK fails while retaining all binds.
					defs[i].SQL = mgGuideReceiptZeroRevision(defs[i].SQL)
				}
				if name == "diagnostic insert" && defs[i].ID == mgGuideReceiptDiagnosticInsert {
					defs[i].SQL = `INSERT INTO live_operation_receipt_diagnostics(request_id,command_incarnation,command_sequence,ordinal,message) VALUES(?,?,?,?+10,?)`
				}
			}
			stage := mgGuideFrozenFixture(t)[0]
			identity := mgGuideIdentityAfter(before)
			turn := mgGuideTestTurn(t, w, MGGuideStageChannels, defs)
			result, err := mgStageGuideNative(t.Context(), turn, before, identity, stage)
			if err != nil {
				t.Fatal(err)
			}
			out, err := mgSaveGuideReceipt(t.Context(), turn, result, identity, RequestDigest{byte(identity.Sequence)}, mgGuideReceiptChannels, mgGuideProjectionFixture())
			if err == nil || out != (mgGuideReceipt{}) {
				t.Fatal("failed statement released a receipt", out, err)
			}
			mgGuideRollback(t, turn)
			var sequence, revision, rows, receipts, children int64
			if err := w.db.QueryRowContext(t.Context(), `SELECT last_command_sequence,stage_revision,(SELECT count(*) FROM live_channel_versions WHERE generation_id='new'),(SELECT count(*) FROM live_operation_command_receipts WHERE command_sequence>=2),(SELECT count(*) FROM live_operation_receipt_diagnostics WHERE command_sequence>=2) FROM live_operations WHERE request_id='guide-request'`).Scan(&sequence, &revision, &rows, &receipts, &children); err != nil {
				t.Fatal(err)
			}
			if sequence != before.progress.sequence || revision != before.progress.revision || rows != 0 || receipts != 0 || children != 0 {
				t.Fatal("whole-frame rollback failed", sequence, revision, rows, receipts, children)
			}
		})
	}
}

func mgGuideReceiptZeroRevision(q string) string {
	columns, values, _ := strings.Cut(q, " VALUES(")
	names := strings.Split(strings.TrimSuffix(strings.TrimPrefix(columns, "INSERT INTO live_operation_command_receipts("), ")"), ",")
	parts := strings.Split(strings.TrimSuffix(values, ")"), ",")
	for i, name := range names {
		if name == "accepted_revision" {
			parts[i] = "(?*0)"
		}
	}
	return columns + " VALUES(" + strings.Join(parts, ",") + ")"
}

func TestMGGuideReceiptCancelledLifetime(t *testing.T) {
	w, b, _ := mgGuideBirthFixture(t, false)
	before := mgGuideBeginCommitted(t, w, b)
	turn, result, identity, _ := mgGuideStagePending(t, w, before)
	r, err := mgSaveGuideReceipt(t.Context(), turn, result, identity, RequestDigest{byte(identity.Sequence)}, mgGuideReceiptChannels, mgGuideProjectionFixture())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	lookup, err := mgLookupGuideReceipt(ctx, turn, r.key)
	if !errors.Is(err, context.Canceled) || lookup.found || lookup.receipt != (mgGuideReceipt{}) {
		t.Fatal(lookup, err)
	}
	saved, err := mgSaveGuideReceipt(ctx, turn, result, identity, r.key.requestDigest, mgGuideReceiptChannels, mgGuideProjectionFixture())
	if !errors.Is(err, context.Canceled) || saved != (mgGuideReceipt{}) {
		t.Fatal(saved, err)
	}
	ack, err := mgACKGuideReceipt(ctx, turn, result.operation, r.key, r.resultDigest)
	if !errors.Is(err, context.Canceled) || ack != (mgGuideNative{}) {
		t.Fatal(ack, err)
	}
}

func TestMGGuideStatementDefinitions(t *testing.T) {
	defs := mgGuideDefinitions()
	if len(defs) != 21 {
		t.Fatal("finite inventory", len(defs))
	}
	if _, err := stmt.Describe(defs); err != nil {
		t.Fatal(err)
	}
	w, _ := testCanonicalExecutor(t)
	for _, d := range defs {
		if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
			t.Fatal(d.ID, issues)
		}
		if len(d.Cohorts) == 0 {
			t.Fatal("missing cohort", d.ID)
		}
		rows, err := w.db.QueryContext(t.Context(), "EXPLAIN "+d.SQL, d.Cohorts[0].Bindings...)
		if err != nil {
			t.Fatal(d.ID, err)
		}
		n := 0
		for rows.Next() {
			n++
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil || n == 0 {
			t.Fatal("native compile", d.ID, n, err)
		}
	}
	ds := mgGuideDescriptors()
	if len(ds) != 4 || ds[0].ID != MGGuideBegin || ds[1].ID != MGGuideStageChannels || ds[2].ID != MGGuideStageProgrammes || ds[3].ID != MGGuideStageChoices {
		t.Fatal(ds)
	}
	for _, d := range ds {
		if d.Durability != Normal {
			t.Fatal("guide staging is not fully NORMAL", d)
		}
	}
	if _, err := buildCommandDescriptors(commandRegistration{owner: "MG", descriptors: ds}); err != nil {
		t.Fatal(err)
	}
}
