package writer

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

func mgGuideReceiptTestTurn(t *testing.T, w *executor, defs ...[]stmt.Definition) *commandTurn {
	t.Helper()
	all := append(mgGuidePublicationDefinitions(), mgGuideReceiptDefinitions()...)
	if len(defs) > 0 {
		all = defs[0]
	}
	// Virtual component clock, not a latency/admission/qualification result.
	clock := time.Now().Add(time.Hour)
	w.now = func() time.Time { return clock }
	registry, err := stmttest.New(t, all)
	if err != nil {
		t.Fatal(err)
	}
	// Several protocol steps are exercised in one synthetic fixture frame. This
	// larger test-only ledger is not an admission profile for a production step.
	recipe := turnRecipe{limit: Work{4096, 1000000, 4096, 32 << 20, 25 * time.Millisecond}, setup: Work{8, 100, 8, 4096, time.Millisecond}, terminal: Work{8, 100, 8, 4096, time.Millisecond}}
	tx, err := w.begin(t.Context(), MGGuidePublish, recipe, &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	var steps []statementStep
	for _, d := range all {
		steps = append(steps, statementStep{d.ID, Work{16, 10000, 32, 65536, time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = turn.closeRows() })
	return turn
}
func mgGuideReceiptProjectionFixture() mgGuideReceiptProjection {
	return mgGuideReceiptProjection{capacityKnown: 1, planningEstimate: 999, tunerCountMode: "discovered", deliveryValidation: "frozen original parser metadata", acquisition: "upload", skippedChannels: 2, skippedProgrammes: 3, adjustedProgrammes: 4, exampleCount: 2, examples: [10]string{"original channel diagnostic", "original programme diagnostic"}}
}
func mgGuideReceiptPublishFixture(t *testing.T, turn *commandTurn, before mgGuideNative) (mgGuideNativeResult, mgGuideReceipt) {
	t.Helper()
	identity := mgGuideIdentityAfter(before)
	result, err := mgPublishGuideNative(t.Context(), turn, before, identity, true)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := mgSaveGuideReceipt(t.Context(), turn, result, identity, RequestDigest{17}, mgGuideReceiptPublish, mgGuideReceiptProjectionFixture())
	if err != nil {
		t.Fatal(err)
	}
	return result, receipt
}
func mgGuideReceiptZero(t *testing.T, r mgGuideReceipt, found bool, err error) {
	t.Helper()
	if err == nil || found || r != (mgGuideReceipt{}) {
		t.Fatal("failure leaked receipt", r, found, err)
	}
}
func TestMGGuideReceiptNativeSaveAndReplay(t *testing.T) {
	w, before := mgGuideReceiptFixture(t, true)
	turn := mgGuideReceiptTestTurn(t, w)
	result, original := mgGuideReceiptPublishFixture(t, turn, before)
	if original.source != result.head.source || original.sourceRevision != result.head.revision || original.sourceState != result.head.state || original.sourceName != result.operation.name || original.tuner != result.operation.tuner || original.staged != result.operation.progress.staged || original.chain != result.operation.progress.chain || original.revision != result.operation.progress.revision || original.projection != mgGuideReceiptProjectionFixture() {
		t.Fatal("saved echoed projection instead of native facts", original)
	}
	var changes int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&changes); err != nil {
		t.Fatal(err)
	}
	replay, err := mgSaveGuideReceipt(t.Context(), turn, result, original.key.identity, original.key.requestDigest, original.key.kind, mgGuideReceiptProjectionFixture())
	if err != nil || replay != original {
		t.Fatal(replay, err)
	}
	var after int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil || after != changes {
		t.Fatal("replay made effects", changes, after, err)
	}
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE live_sources SET name='Replacement',active_generation='old',revision=revision+1 WHERE id='source'`); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE live_operations SET status='superseded',stage_revision=stage_revision+1 WHERE request_id='guide-request'`); err != nil {
		t.Fatal(err)
	}
	moved, found, err := mgLookupGuideReceipt(t.Context(), turn, original.key)
	if err != nil || !found || moved != original {
		t.Fatal("replay rebuilt today's result", moved, found, err)
	}
	// Raw fixture deletion only simulates the parent's debt/floor-gated removal.
	// These helpers do not authorize or implement owner/generation deletion.
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `DELETE FROM live_operations WHERE request_id='guide-request'`); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `DELETE FROM live_generations WHERE id='new'`); err != nil {
		t.Fatal(err)
	}
	stale := result
	stale.operation.name = "Current metadata is irrelevant"
	changedProjection := mgGuideReceiptProjectionFixture()
	changedProjection.examples[0] = "new diagnostic"
	replay, err = mgSaveGuideReceipt(t.Context(), turn, stale, original.key.identity, original.key.requestDigest, original.key.kind, changedProjection)
	if err != nil || replay != original {
		t.Fatal("original history was owner/generation-owned", replay, err)
	}
}
func TestMGGuideReceiptIdentityConflicts(t *testing.T) {
	w, before := mgGuideReceiptFixture(t, true)
	turn := mgGuideReceiptTestTurn(t, w)
	_, r := mgGuideReceiptPublishFixture(t, turn, before)
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
		got, found, err := mgLookupGuideReceipt(t.Context(), turn, key)
		if !errors.Is(err, ErrConflict) {
			t.Fatal(name, err)
		}
		mgGuideReceiptZero(t, got, found, err)
	}
	unknown := r.key
	unknown.identity.Sequence++
	unknown.identity.OperationID[0]++
	got, found, err := mgLookupGuideReceipt(t.Context(), turn, unknown)
	if err != nil || found || got != (mgGuideReceipt{}) {
		t.Fatal("unknown operation invented success", got, found, err)
	}
	// An unknown operation cannot ACK an existing sequence, including with a
	// disclosed correct digest copied from a different original operation.
	altered := r.key
	altered.identity.OperationID[0]++
	out, err := mgACKGuideReceipt(t.Context(), turn, mgGuideNativeResultOperation(t, turn), altered, r.resultDigest)
	if err == nil || out != (mgGuideNative{}) {
		t.Fatal("unknown operation ACK succeeded", out, err)
	}
}
func mgGuideNativeResultOperation(t *testing.T, turn *commandTurn) mgGuideNative {
	t.Helper()
	out, err := mgReadGuideNative(t.Context(), turn, "guide-request")
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func TestMGGuideReceiptDiagnosticCompleteness(t *testing.T) {
	w, before := mgGuideReceiptFixture(t, true)
	for _, name := range []string{"missing child", "changed child", "extra child", "header count", "result digest"} {
		t.Run(name, func(t *testing.T) {
			turn := mgGuideReceiptTestTurn(t, w)
			_, r := mgGuideReceiptPublishFixture(t, turn, before)
			var q string
			switch name {
			case "missing child":
				q = `DELETE FROM live_operation_receipt_diagnostics WHERE ordinal=0`
			case "changed child":
				q = `UPDATE live_operation_receipt_diagnostics SET message='tampered' WHERE ordinal=1`
			case "extra child":
				q = `INSERT INTO live_operation_receipt_diagnostics SELECT request_id,command_incarnation,command_sequence,2,'extra' FROM live_operation_command_receipts`
			case "header count":
				q = `UPDATE live_operation_command_receipts SET result_diagnostic_example_count=1`
			case "result digest":
				q = `UPDATE live_operation_command_receipts SET result_digest=zeroblob(32)`
			}
			if _, err := turn.transaction.conn.ExecContext(t.Context(), q); err != nil {
				t.Fatal(err)
			}
			got, found, err := mgLookupGuideReceipt(t.Context(), turn, r.key)
			mgGuideReceiptZero(t, got, found, err)
		})
	}
	t.Run("all ten bounded diagnostics", func(t *testing.T) {
		turn := mgGuideReceiptTestTurn(t, w)
		identity := mgGuideIdentityAfter(before)
		result, err := mgPublishGuideNative(t.Context(), turn, before, identity, true)
		if err != nil {
			t.Fatal(err)
		}
		p := mgGuideReceiptProjectionFixture()
		p.exampleCount = 10
		for i := range p.examples {
			p.examples[i] = strings.Repeat("x", i+1)
		}
		got, err := mgSaveGuideReceipt(t.Context(), turn, result, identity, RequestDigest{17}, mgGuideReceiptPublish, p)
		if err != nil || got.projection != p {
			t.Fatal(got, err)
		}
	})
}
func TestMGGuideReceiptACKAndRetirementFloor(t *testing.T) {
	w, before := mgGuideReceiptFixture(t, true)
	turn := mgGuideReceiptTestTurn(t, w)
	result, r := mgGuideReceiptPublishFixture(t, turn, before)
	wrong := r.resultDigest
	wrong[0]++
	ack, err := mgACKGuideReceipt(t.Context(), turn, result.operation, r.key, wrong)
	if err == nil || ack != (mgGuideNative{}) {
		t.Fatal("wrong digest ACK", ack, err)
	}
	floor, err := mgSaveGuideRetirementFloor(t.Context(), turn, result.operation, r.key, r.resultDigest)
	if err == nil || floor != (mgGuideRetirementFloor{}) {
		t.Fatal("unACKed floor", floor, err)
	}
	ack, err = mgACKGuideReceipt(t.Context(), turn, result.operation, r.key, r.resultDigest)
	if err != nil || ack.acknowledged != r.key.identity.Sequence {
		t.Fatal(ack, err)
	}
	var changes int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&changes); err != nil {
		t.Fatal(err)
	}
	repeated, err := mgACKGuideReceipt(t.Context(), turn, ack, r.key, r.resultDigest)
	if err != nil || repeated != ack {
		t.Fatal("exact duplicate ACK", repeated, err)
	}
	var after int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil || after != changes {
		t.Fatal("duplicate ACK mutated", changes, after, err)
	}
	floor, err = mgSaveGuideRetirementFloor(t.Context(), turn, ack, r.key, r.resultDigest)
	if err != nil || floor.finalSequence != r.key.identity.Sequence || floor.resultDigest != r.resultDigest || floor.birthDigest != before.birthDigest || floor.createOperation != before.createOperation {
		t.Fatal(floor, err)
	}
	// Floor creation leaves actual owner/generation/physical debt untouched.
	actual := mgGuideNativeResultOperation(t, turn)
	if actual != ack {
		t.Fatal("floor changed live owner", actual)
	}
	duplicate, err := mgSaveGuideRetirementFloor(t.Context(), turn, ack, r.key, r.resultDigest)
	if err != nil || duplicate != floor {
		t.Fatal("idempotent floor drift", duplicate, err)
	}
	unknown := r.key
	unknown.identity.OperationID[0]++
	duplicate, err = mgSaveGuideRetirementFloor(t.Context(), turn, ack, unknown, r.resultDigest)
	if err == nil || duplicate != (mgGuideRetirementFloor{}) {
		t.Fatal("unknown floor operation succeeded", duplicate, err)
	}
	if err := mgCheckGuideReceiptBirth(t.Context(), turn, before.request); !errors.Is(err, ErrConflict) {
		t.Fatal("live/floored request reborn", err)
	}
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `DELETE FROM live_operations WHERE request_id='guide-request'`); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `DELETE FROM live_operation_command_receipts WHERE request_id='guide-request'`); err != nil {
		t.Fatal(err)
	}
	stored, found, err := mgReadGuideRetirementFloor(t.Context(), turn, before.request)
	if err != nil || !found || stored != floor {
		t.Fatal("floor lost with owner/history", stored, found, err)
	}
	if err := mgCheckGuideReceiptBirth(t.Context(), turn, before.request); !errors.Is(err, ErrConflict) {
		t.Fatal("GC allowed request rebirth", err)
	}
	if err := mgCheckGuideReceiptBirth(t.Context(), turn, "new-request"); err != nil {
		t.Fatal(err)
	}
	var children int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM live_operation_receipt_diagnostics`).Scan(&children); err != nil || children != 0 {
		t.Fatal("bounded diagnostic cascade", children, err)
	}
}

func TestMGGuideReceiptNativeFactFences(t *testing.T) {
	w, before := mgGuideReceiptFixture(t, true)
	for _, name := range []string{"native progress", "native head", "namespace", "ACK sequence", "future time", "invalid metadata"} {
		t.Run(name, func(t *testing.T) {
			turn := mgGuideReceiptTestTurn(t, w)
			identity := mgGuideIdentityAfter(before)
			result, err := mgPublishGuideNative(t.Context(), turn, before, identity, true)
			if err != nil {
				t.Fatal(err)
			}
			p := mgGuideReceiptProjectionFixture()
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
			got, err := mgSaveGuideReceipt(t.Context(), turn, result, identity, RequestDigest{17}, mgGuideReceiptPublish, p)
			if err == nil || got != (mgGuideReceipt{}) {
				t.Fatal("stale/fabricated native facts accepted", name, got, err)
			}
			var n int64
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM live_operation_command_receipts`).Scan(&n); err != nil || n != 0 {
				t.Fatal("failed save left history", n, err)
			}
		})
	}
}
func TestMGGuideReceiptFailureWholeFrameRollback(t *testing.T) {
	w, before := mgGuideReceiptFixture(t, true)
	for _, name := range []string{"receipt insert", "diagnostic insert"} {
		t.Run(name, func(t *testing.T) {
			defs := append(mgGuidePublicationDefinitions(), mgGuideReceiptDefinitions()...)
			for i := range defs {
				if name == "receipt insert" && defs[i].ID == mgGuideReceiptInsert {
					// Native positive-revision CHECK fails while retaining all original binds.
					defs[i].SQL = mgGuideReceiptZeroRevision(defs[i].SQL)
				}
				if name == "diagnostic insert" && defs[i].ID == mgGuideReceiptDiagnosticInsert {
					defs[i].SQL = `INSERT INTO live_operation_receipt_diagnostics(request_id,command_incarnation,command_sequence,ordinal,message) VALUES(?,?,?,?+10,?)`
				}
			}
			turn := mgGuideReceiptTestTurn(t, w, defs)
			identity := mgGuideIdentityAfter(before)
			result, err := mgPublishGuideNative(t.Context(), turn, before, identity, true)
			if err != nil {
				t.Fatal(err)
			}
			out, err := mgSaveGuideReceipt(t.Context(), turn, result, identity, RequestDigest{17}, mgGuideReceiptPublish, mgGuideReceiptProjectionFixture())
			if err == nil || out != (mgGuideReceipt{}) {
				t.Fatal("failed statement released result", out, err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			var generation, status string
			var sequence, revision, receipts, children int64
			if err := w.db.QueryRowContext(t.Context(), `SELECT s.active_generation,o.status,o.last_command_sequence,o.stage_revision,(SELECT count(*) FROM live_operation_command_receipts),(SELECT count(*) FROM live_operation_receipt_diagnostics) FROM live_sources s JOIN live_operations o ON o.source_id=s.id WHERE o.request_id='guide-request'`).Scan(&generation, &status, &sequence, &revision, &receipts, &children); err != nil || generation != "old" || status != "staging" || sequence != before.progress.sequence || revision != before.progress.revision || receipts != 0 || children != 0 {
				t.Fatal("whole-frame rollback failed", generation, status, sequence, revision, receipts, children, err)
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
	w, before := mgGuideReceiptFixture(t, true)
	turn := mgGuideReceiptTestTurn(t, w)
	result, r := mgGuideReceiptPublishFixture(t, turn, before)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	out, found, err := mgLookupGuideReceipt(ctx, turn, r.key)
	mgGuideReceiptZero(t, out, found, err)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	saved, err := mgSaveGuideReceipt(ctx, turn, result, r.key.identity, r.key.requestDigest, r.key.kind, r.projection)
	if !errors.Is(err, context.Canceled) || saved != (mgGuideReceipt{}) {
		t.Fatal(saved, err)
	}
	ack, err := mgACKGuideReceipt(ctx, turn, result.operation, r.key, r.resultDigest)
	if !errors.Is(err, context.Canceled) || ack != (mgGuideNative{}) {
		t.Fatal(ack, err)
	}
}
func TestMGGuideReceiptDefinitions(t *testing.T) {
	defs := mgGuideReceiptDefinitions()
	if len(defs) != 11 {
		t.Fatal("finite inventory", len(defs))
	}
	if _, err := stmt.Describe(defs); err != nil {
		t.Fatal(err)
	}
	w, _ := mgGuideReceiptFixture(t, true)
	for _, d := range defs {
		if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
			t.Fatal(d.ID, issues)
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
}
