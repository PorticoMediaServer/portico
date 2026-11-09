package writer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

type reclaimCrashSource struct {
	Operation string
	Initial   effectiveRawJournal
	Identity  CommandIdentity
	Request   RequestDigest
	Result    ResultDigest
}

// Independent single-child transition. No prepareWCReclaim, slot decoder,
// classifier or returned production observation supplies expected state.
func expectedReclaimCrash(source reclaimCrashSource) (want effectiveRawJournal, out wc008ReclaimEffect, charge int64, err error) {
	defer func() {
		if v := recover(); v != nil {
			want, out, charge, err = nil, wc008ReclaimEffect{}, 0, fmt.Errorf("reclaim oracle: %v", v)
		}
	}()
	progressRequire(source.Operation == "empty" || source.Operation == "removed" || source.Operation == "zero" || source.Operation == "none", "reclaim operation")
	progressRequire(source.Request == (RequestDigest{11}) && source.Result == (ResultDigest{12}) && source.Identity.Incarnation == ([16]byte{1}), "original reclaim identity/digests")
	data, err := json.Marshal(source.Initial)
	progressRequire(err == nil && json.Unmarshal(data, &want) == nil && len(want) == len(progressCrashTables), "original reclaim checkpoint")
	owner := progressSingle(want, "playback_personal_claims")
	last, ack, cursor := owner["command_last_sequence"].Int, owner["command_acknowledged_sequence"].Int, owner["receipt_reclaim_after_sequence"].Int
	progressRequire(last == ack && source.Identity.Sequence == last+1 && source.Identity.OperationID != ([16]byte{}) && owner["receipt_reclaim_due_ms"].Int == 123, "captured original floor/due")
	table := want["playback_claim_command_receipts"]
	progressRequire(int64(len(table.Rows)) == owner["receipt_rows"].Int, "original exact child count")
	var first map[string]effectiveRawCell
	var total int64
	for _, row := range table.Rows {
		progressRequire(row["playback_id"].Text == "playback-音" && (row["command_kind"].Text == "capture_report" || row["command_kind"].Text == "drain_piece"), "original source child")
		progressRequire(row["variable_bytes"].Int == progressBytes(row, progressReceiptByteColumns), "original child byte charge")
		total += row["variable_bytes"].Int
		seq := row["command_sequence"].Int
		if seq > cursor && seq <= ack && (first == nil || seq < first["command_sequence"].Int) {
			first = row
		}
	}
	progressRequire(total == owner["receipt_variable_bytes"].Int, "original exact debt")
	out.first = wcReclaimProbe{playbackID: "playback-音", incarnation: source.Identity.Incarnation, afterSequence: cursor, throughACK: ack}
	out.following = out.first
	rows, bytes := owner["receipt_rows"].Int, total
	var removed any
	if first != nil {
		seq, debit := first["command_sequence"].Int, first["variable_bytes"].Int
		out.hasChild = true
		out.child = wcReclaimChild{key: wcReclaimKey{playbackID: "playback-音", incarnation: source.Identity.Incarnation, sequence: seq}, variableBytes: debit}
		out.first.nextSequence = seq
		out.following.afterSequence = seq
		for _, row := range table.Rows {
			next := row["command_sequence"].Int
			if next > seq && next <= ack && (out.following.nextSequence == 0 || next < out.following.nextSequence) {
				out.following.nextSequence = next
			}
		}
		out.prepared = wcReclaimPrepared{remove: true, key: out.child.key, removedVariableBytes: debit}
		var kept []map[string]effectiveRawCell
		for _, row := range table.Rows {
			if row["command_sequence"].Int != seq {
				kept = append(kept, row)
			}
		}
		table.Rows = kept
		want["playback_claim_command_receipts"] = table
		rows, bytes, cursor, removed = rows-1, bytes-debit, seq, seq
	}
	out.prepared.afterSequence, out.prepared.remainingRows, out.prepared.remainingVariableBytes = cursor, rows, bytes
	out.prepared.noNext = out.following.nextSequence == 0
	progressRequire(out.prepared.noNext == (rows == 0) && (rows == 0) == (bytes == 0), "original eligible range agrees with debt")
	var due any
	var count, noNext int64
	if out.hasChild {
		count = 1
	}
	if out.prepared.noNext {
		noNext = 1
	} else {
		due = int64(123)
	}
	for key, value := range map[string]any{
		"command_last_sequence": source.Identity.Sequence, "receipt_rows": rows, "receipt_variable_bytes": bytes, "receipt_reclaim_after_sequence": cursor, "receipt_reclaim_due_ms": due,
		"reclaim_command_sequence": source.Identity.Sequence, "reclaim_operation_id": source.Identity.OperationID[:], "reclaim_request_digest": source.Request[:],
		"reclaim_command_kind": "progress_receipt_reclaim", "reclaim_result_version": int64(1), "reclaim_captured_acknowledged_sequence": ack,
		"reclaim_old_after_sequence": owner["receipt_reclaim_after_sequence"].Int, "reclaim_removed_sequence": removed, "reclaim_removed_count": count,
		"reclaim_removed_variable_bytes": out.prepared.removedVariableBytes, "reclaim_result_after_sequence": cursor, "reclaim_result_no_next": noNext,
		"reclaim_applied_ms": int64(124), "reclaim_result_count": int64(1), "reclaim_result_digest": source.Result[:],
	} {
		progressSet(owner, key, value)
	}
	charge = progressBytes(owner, progressClaimByteColumns)
	progressSet(owner, "control_variable_bytes", charge)
	for name, table := range want {
		sortEffectiveRows(&table)
		want[name] = table
	}
	return want, out, charge, nil
}

func reclaimCrashCommand(t *testing.T, source reclaimCrashSource) wc008ReclaimCommand {
	t.Helper()
	r := progressSingle(source.Initial, "playback_personal_claims")
	q := wc008ReclaimCommand{identity: source.Identity, appliedMS: 124, resultVersion: 1, oldControlBytes: r["control_variable_bytes"].Int,
		owner: wcReclaimOwner{playbackID: "playback-音", incarnation: source.Identity.Incarnation, lastSequence: r["command_last_sequence"].Int, acknowledgedSequence: r["command_acknowledged_sequence"].Int,
			afterSequence: r["receipt_reclaim_after_sequence"].Int, rows: r["receipt_rows"].Int, variableBytes: r["receipt_variable_bytes"].Int, due: true, dueMS: 123}}
	if r["reclaim_command_sequence"].Kind != "null" {
		if r["reclaim_command_kind"].Text != "progress_receipt_reclaim" || r["reclaim_result_version"].Int != 1 || r["reclaim_result_count"].Int != 1 {
			t.Fatal("unsupported native old-slot input")
		}
		old := wcReclaimSlot{present: true, reference: ReceiptReference{Identity: CommandIdentity{Incarnation: source.Identity.Incarnation, Sequence: r["reclaim_command_sequence"].Int}},
			floor: r["reclaim_captured_acknowledged_sequence"].Int, oldCursor: r["reclaim_old_after_sequence"].Int, removedSequence: r["reclaim_removed_sequence"].Int,
			removedCount: r["reclaim_removed_count"].Int, removedBytes: r["reclaim_removed_variable_bytes"].Int, nextCursor: r["reclaim_result_after_sequence"].Int, noNext: r["reclaim_result_no_next"].Int == 1, appliedMS: r["reclaim_applied_ms"].Int}
		if copy(old.reference.Identity.OperationID[:], r["reclaim_operation_id"].Blob) != 16 || copy(old.reference.RequestDigest[:], r["reclaim_request_digest"].Blob) != 32 || copy(old.resultDigest[:], r["reclaim_result_digest"].Blob) != 32 {
			t.Fatal("old native slot width")
		}
		q.oldSlot, q.expectedOldSlot, q.oldResultVersion = old, old, 1
	}
	return q
}

func reclaimCrashInput(t *testing.T, operation string) (*executor, string, reclaimCrashSource) {
	t.Helper()
	w, path, _ := testExecutor(t)
	q := wc008NativeReclaim(t, w) // canonical finite INPUT, never its stmttest turn
	// Make both original saved digests nonzero; retained corruption must be
	// detectable even though the canonical WC input used zero opaque octets.
	wc008NativeExec(t, w, `UPDATE playback_claim_command_receipts SET request_digest=?,result_digest=?`, append([]byte{31}, make([]byte, 31)...), append([]byte{32}, make([]byte, 31)...))
	var oldSequence, oldFloor, removedBytes, cursor, last int64
	var removed any
	var removedCount, noNext int64
	switch operation {
	case "empty":
		wc008NativeExec(t, w, `UPDATE playback_claim_command_receipts SET command_sequence=2 WHERE command_sequence=3`)
		last = 2
	case "removed":
		if err := w.db.QueryRowContext(t.Context(), `SELECT variable_bytes FROM playback_claim_command_receipts WHERE command_sequence=1`).Scan(&removedBytes); err != nil {
			t.Fatal(err)
		}
		wc008NativeExec(t, w, `DELETE FROM playback_claim_command_receipts WHERE command_sequence=1`)
		oldSequence, oldFloor, removed, removedCount, cursor, last = 2, 1, int64(1), 1, 1, 3
	case "zero":
		wc008NativeExec(t, w, `DELETE FROM playback_claim_command_receipts WHERE command_sequence=3`)
		wc008NativeExec(t, w, `UPDATE playback_claim_command_receipts SET command_sequence=2 WHERE command_sequence=1`)
		oldSequence, last, noNext = 1, 2, 1
	case "none":
		wc008NativeExec(t, w, `DELETE FROM playback_claim_command_receipts`)
	default:
		t.Fatal("unsupported reclaim fixture", operation)
	}
	// Historical owner/slot populations are INITIAL fixture input. This is not
	// an executed cleanup ACK, a source issuer, or production history proof.
	wc008NativeExec(t, w, `UPDATE playback_personal_claims SET command_last_sequence=?,command_acknowledged_sequence=?,receipt_rows=(SELECT count(*) FROM playback_claim_command_receipts),receipt_variable_bytes=coalesce((SELECT sum(variable_bytes) FROM playback_claim_command_receipts),0)`, last, last)
	if oldSequence != 0 {
		wc008NativeExec(t, w, `UPDATE playback_personal_claims SET receipt_reclaim_after_sequence=?,reclaim_command_sequence=?,reclaim_operation_id=?,reclaim_request_digest=?,reclaim_command_kind='progress_receipt_reclaim',reclaim_result_version=1,reclaim_captured_acknowledged_sequence=?,reclaim_old_after_sequence=0,reclaim_removed_sequence=?,reclaim_removed_count=?,reclaim_removed_variable_bytes=?,reclaim_result_after_sequence=?,reclaim_result_no_next=?,reclaim_applied_ms=120,reclaim_result_count=1,reclaim_result_digest=?`, cursor, oldSequence, append([]byte{8}, make([]byte, 15)...), append([]byte{13}, make([]byte, 31)...), oldFloor, removed, removedCount, removedBytes, cursor, noNext, append([]byte{14}, make([]byte, 31)...))
	}
	initial, err := readEffectiveJournal(t.Context(), w.db, progressCrashTables)
	if err != nil {
		t.Fatal(err)
	}
	id := CommandIdentity{Incarnation: q.identity.Incarnation, Sequence: last + 1, OperationID: [16]byte{9}}
	return w, path, reclaimCrashSource{Operation: operation, Initial: initial, Identity: id, Request: RequestDigest{11}, Result: ResultDigest{12}}
}

func reclaimCrashRegistry(t *testing.T, w *executor) *stmt.Registry {
	t.Helper()
	if schema.SHA256 != progressCrashSchema017 {
		t.Fatal("reclaim captures require schema017", schema.SHA256)
	}
	defs := wcReclaimDefinitions()
	if len(defs) != 5 {
		t.Fatal("reclaim definition inventory changed", len(defs))
	}
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := sqliteconn.Assert(t.Context(), conn, w.config); err != nil {
		t.Fatal(err)
	}
	return nativeFixtureRegistry(t, conn, w.config, defs, "wc_reclaim_008_native_test.go", "wc_008_native_test.go", "crash_reclaim_linux_test.go")
}

func reclaimCrashPhases(operation string) []string {
	ids := []stmt.ID{wc008ReclaimFirst}
	if operation != "none" {
		ids = append(ids, wc008ReclaimFirst, wc008ReclaimDelete)
	}
	last := wc008ReclaimStoreEmpty
	if operation == "removed" {
		last = wc008ReclaimStoreRemoved
	}
	if operation == "zero" {
		last = wc008ReclaimStoreZero
	}
	ids = append(ids, last)
	var phases []string
	for i, id := range ids {
		phases = append(phases, fmt.Sprintf("native_%02d_%s", i, id))
	}
	return phases
}

func reclaimCrashCase(c crashCase) bool {
	if c.Domain != "wc_reclaim" {
		return false
	}
	switch c.Operation {
	case "empty", "removed", "zero", "none":
	default:
		return false
	}
	if c.CommitFault != "" {
		return c.Operation == "empty" && c.Boundary == "uncertain_return" && (c.CommitFault == "before" || c.CommitFault == "after")
	}
	if c.Boundary == "api_return" {
		return true
	}
	if c.Operation == "empty" {
		for _, phase := range append(reclaimCrashPhases(c.Operation), "before_begin", "after_begin", "before_commit", "after_commit", "physical_return") {
			if phase == c.Boundary {
				return true
			}
		}
	}
	if c.Operation == "removed" || c.Operation == "zero" {
		phases := reclaimCrashPhases(c.Operation)
		return c.Boundary == phases[len(phases)-1]
	}
	return false
}

func crashReclaimFixture(t *testing.T, witness *crashWitness) (*executor, reclaimCrashSource) {
	t.Helper()
	if witness.Case.Boundary != "trace" && !reclaimCrashCase(witness.Case) {
		t.Fatal("unsupported reclaim case", witness.Case)
	}
	w, path, source := reclaimCrashInput(t, witness.Case.Operation)
	witness.Path, witness.Reclaim = path, &source
	registry := reclaimCrashRegistry(t, w)
	ids := map[string]stmt.ID{}
	for _, d := range wcReclaimDefinitions() {
		ids[strings.TrimSpace(d.SQL)] = d.ID
	}
	var sequence []string
	armed := true
	installCrashDriverAt(t, w, path, witness, crashClock, func(query string) {
		if id, ok := ids[strings.TrimSpace(query)]; armed && ok {
			phase := fmt.Sprintf("native_%02d_%s", len(sequence), id)
			sequence = append(sequence, phase)
			crashPoint(t, witness, phase)
		}
	})
	// WC's landed cleanup component uses this actual governed ProgressAccept
	// frame. No new cleanup purpose, profile, codec or authority is invented.
	turn, authority := progressCrashTurn(t, w, path, registry)
	q := reclaimCrashCommand(t, source)
	out, err := applyWC008ReclaimChild(t.Context(), turn, q)
	if err != nil {
		t.Fatal(err)
	}
	charge, err := storeWC008ReclaimResult(t.Context(), turn, q, out, source.Request, source.Result)
	if err != nil {
		t.Fatal(err)
	}
	armed = false
	if !reflect.DeepEqual(sequence, reclaimCrashPhases(source.Operation)) {
		t.Fatal("reclaim native sequence changed", sequence)
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	want, wantOut, wantCharge, err := expectedReclaimCrash(source)
	if err != nil {
		t.Fatal(err)
	}
	if out != wantOut || charge != wantCharge {
		t.Fatal("complete private reclaim return changed", out, wantOut, charge, wantCharge)
	}
	actual, err := readEffectiveJournal(t.Context(), turn.transaction.conn, progressCrashTables)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareEffectiveJournal(want, actual); err != nil {
		t.Fatal("reclaim raw journal differs", err)
	}
	physical, err := turn.transaction.commit()
	if witness.Case.CommitFault != "" {
		if !errors.Is(err, ErrResolveRequired) || !errors.Is(err, errCrashCommitReply) || physical.state != commitUnknown || !w.ambiguous || !w.fenced {
			t.Fatal("reclaim ambiguous commit escaped", physical, err)
		}
		witness.Unknown = true
		crashPoint(t, witness, "uncertain_return")
	} else if err != nil || physical.state != committed || turn.transaction.descriptor.ID != ProgressAccept || turn.transaction.descriptor.Durability != Normal || !turn.transaction.policyVerified || !turn.transaction.commitAttempted || turn.transaction.commitReturnedAt.IsZero() || turn.transaction.physicalReturnedAt.IsZero() || !turn.transaction.retirementComplete {
		t.Fatal("reclaim physical completion missing", physical, err)
	}
	if authority.Usage() != ([4]governor.Amounts{}) || w.db.Stats().InUse != 0 {
		t.Fatal("reclaim custody did not retire")
	}
	crashPoint(t, witness, "api_return")
	if witness.Case.Boundary != "trace" {
		t.Fatal("reclaim boundary not reached")
	}
	return w, source
}

func verifyReclaimCrash(t *testing.T, witness crashWitness) {
	t.Helper()
	if !reclaimCrashCase(witness.Case) || witness.Reclaim == nil || witness.Reclaim.Operation != witness.Case.Operation || witness.Unknown != (witness.Case.CommitFault != "") {
		t.Fatal("unsupported reclaim witness")
	}
	committed := witness.Case.Boundary == "after_commit" || witness.Case.Boundary == "physical_return" || witness.Case.Boundary == "api_return" || witness.Case.CommitFault == "after"
	want := witness.Reclaim.Initial
	if committed {
		var err error
		want, _, _, err = expectedReclaimCrash(*witness.Reclaim)
		if err != nil {
			t.Fatal(err)
		}
	}
	e := reopenCrashExecutor(t, witness.Path)
	actual, err := readEffectiveJournal(t.Context(), e.db, progressCrashTables)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareEffectiveJournal(want, actual); err != nil {
		t.Fatal("reclaim restart lost receipt/custody", err)
	}
	if committed {
		registry := reclaimCrashRegistry(t, e)
		e.now = func() time.Time { return crashClock }
		turn, _ := progressCrashTurn(t, e, witness.Path, registry)
		q := reclaimCrashCommand(t, *witness.Reclaim)
		out, err := applyWC008ReclaimChild(t.Context(), turn, q)
		if err != nil && out != (wc008ReclaimEffect{}) {
			t.Fatal("failed reclaim leaked effect", out, err)
		}
		if err == nil {
			var charge int64
			charge, err = storeWC008ReclaimResult(t.Context(), turn, q, out, witness.Reclaim.Request, witness.Reclaim.Result)
			if charge != 0 {
				t.Fatal("original cleanup replay reported saved new charge", charge, err)
			}
		}
		if err == nil {
			t.Fatal("original cleanup re-executed")
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
	}
	actual, err = readEffectiveJournal(t.Context(), e.db, progressCrashTables)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareEffectiveJournal(want, actual); err != nil {
		t.Fatal("reclaim replay changed retained receipts", err)
	}
}

func TestCrashReplayReclaimComponents(t *testing.T) {
	for _, operation := range []string{"empty", "removed", "zero", "none"} {
		t.Run(operation, func(t *testing.T) {
			witness := crashWitness{Case: crashCase{"wc_reclaim", operation, "trace", ""}}
			w, _ := crashReclaimFixture(t, &witness)
			if err := w.close(); err != nil {
				t.Fatal(err)
			}
			phases := reclaimCrashPhases(operation)
			if operation == "removed" || operation == "zero" {
				phases = phases[len(phases)-1:]
			}
			if operation == "none" {
				phases = nil
			}
			if operation == "empty" {
				phases = append(phases, "before_begin", "after_begin", "before_commit", "after_commit", "physical_return")
			}
			for _, phase := range append(phases, "api_return") {
				t.Run(phase, func(t *testing.T) {
					verifyReclaimCrash(t, runCrashChild(t, crashCase{"wc_reclaim", operation, phase, ""}))
				})
			}
			if operation == "empty" {
				for _, fault := range []string{"before", "after"} {
					t.Run("uncertain_return/"+fault, func(t *testing.T) {
						verifyReclaimCrash(t, runCrashChild(t, crashCase{"wc_reclaim", operation, "uncertain_return", fault}))
					})
				}
			}
		})
	}
}

func TestReclaimCrashRejectsRawCorruption(t *testing.T) {
	witness := crashWitness{Case: crashCase{"wc_reclaim", "empty", "trace", ""}}
	w, source := crashReclaimFixture(t, &witness)
	want, _, _, err := expectedReclaimCrash(source)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cfg := w.config
	cfg.Synchronous = sqliteconn.Normal
	if _, err := sqliteconn.Assert(t.Context(), conn, cfg); err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []struct{ name, sql string }{
		{"retained_child", `UPDATE playback_claim_command_receipts SET result_digest=zeroblob(32)`},
		{"saved_scalar", `UPDATE playback_personal_claims SET reclaim_result_digest=zeroblob(32)`},
		{"exact_debt", `UPDATE playback_personal_claims SET receipt_rows=receipt_rows+1`},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			if _, err := conn.ExecContext(t.Context(), "SAVEPOINT corrupt_reclaim"); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := conn.ExecContext(t.Context(), "ROLLBACK TO corrupt_reclaim"); err != nil {
					t.Error(err)
				}
				if _, err := conn.ExecContext(t.Context(), "RELEASE corrupt_reclaim"); err != nil {
					t.Error(err)
				}
			}()
			result, err := conn.ExecContext(t.Context(), mutation.sql)
			if err != nil {
				t.Fatal(err)
			}
			if n, err := result.RowsAffected(); err != nil || n != 1 {
				t.Fatal("corruption did not reach one row", n, err)
			}
			actual, err := readEffectiveJournal(t.Context(), conn, progressCrashTables)
			if err != nil {
				t.Fatal(err)
			}
			if compareEffectiveJournal(want, actual) == nil {
				t.Fatal("reclaim oracle accepted corruption")
			}
		})
	}
}

func TestReclaimCrashMutants(t *testing.T) {
	evidence := os.Getenv("PORTICO_LANE_EVIDENCE_DIR")
	if evidence == "" {
		t.Skip("run with lane-focused.yml for production mutant evidence")
	}
	var proofs []map[string]string
	for _, mutation := range []struct{ name, old, new, reason string }{
		{"return_only", "return slotBytes, turn.checkLifetimes(ctx)", "if os.Getenv(\"PORTICO_CRASH_CASE\") != \"\" {slotBytes++}; return slotBytes, turn.checkLifetimes(ctx)", "complete private reclaim return changed"},
		{"owner_debt", "args := []any{q.identity.Sequence, p.remainingRows, p.remainingVariableBytes", "rows := p.remainingRows; if os.Getenv(\"PORTICO_CRASH_CASE\") != \"\" {rows++}; args := []any{q.identity.Sequence, rows, p.remainingVariableBytes", "reclaim raw journal differs"},
		{"saved_digest", "p := e.prepared", "if os.Getenv(\"PORTICO_CRASH_CASE\") != \"\" {result[0]++}; p := e.prepared", "reclaim raw journal differs"},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			const file = "wc_reclaim_008_draft.go"
			original, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(string(original), mutation.old) != 1 {
				t.Fatal("mutant source locator changed")
			}
			modified := strings.Replace(string(original), mutation.old, mutation.new, 1)
			modified = strings.Replace(modified, "import (", "import (\n\"os\"", 1)
			scratch := t.TempDir()
			out := filepath.Join(scratch, "mutant.go")
			if err := os.WriteFile(out, []byte(modified), 0600); err != nil {
				t.Fatal(err)
			}
			absolute, err := filepath.Abs(file)
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(map[string]any{"Replace": map[string]string{absolute: out}})
			if err != nil {
				t.Fatal(err)
			}
			overlay := filepath.Join(scratch, "overlay.json")
			if err := os.WriteFile(overlay, data, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, "go", "test", "-overlay", overlay, "-p", "2", "-parallel", "2", "-count=1", "-timeout=2m", "-run", "^TestCrashReplayReclaimComponents$/^empty$/^api_return$", ".")
			cmd.Env = append(os.Environ(), "TMPDIR="+scratch, "GOTMPDIR="+scratch)
			output, err := cmd.CombinedOutput()
			if err == nil || ctx.Err() != nil || !strings.Contains(string(output), mutation.reason) || !strings.Contains(string(output), "TestCrashReplayChild") || strings.Contains(string(output), "[build failed]") {
				t.Fatalf("mutant failed outside intended child check: %v\n%s", err, output)
			}
			a, b := sha256.Sum256(original), sha256.Sum256([]byte(modified))
			proofs = append(proofs, map[string]string{"name": mutation.name, "source_sha256": hex.EncodeToString(a[:]), "mutant_sha256": hex.EncodeToString(b[:]), "reason": mutation.reason})
			t.Logf("rejected at child semantic check: %s", mutation.reason)
		})
	}
	if t.Failed() {
		return
	}
	data, err := json.MarshalIndent(proofs, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(evidence, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(evidence, "reclaim-mutants.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}
