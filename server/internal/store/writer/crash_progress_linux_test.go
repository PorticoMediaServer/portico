package writer

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const progressCrashSchema019 = "81d6971a2eeb98f07304cf9e889af5f0bf94572c9c8f3fa8539a099d7f98d46b"

func progressCrashDefinitions() []stmt.Definition {
	defs := append(wcProgressDefinitions(), wcProgressAckDefinitions()...)
	return append(defs, wcAPIEventDefinitions()...)
}

func progressCrashInput(t *testing.T, operation string) (*executor, string, wc008CaptureReport, progressCrashSource) {
	t.Helper()
	if operation != "capture" && operation != "drain" && operation != "drain_pending" {
		t.Fatal("unsupported progress component", operation)
	}
	w, path, _ := testExecutor(t)
	until := int64(1000)
	if operation == "drain_pending" {
		until = 900001
	}
	p := wc008NativeProgress(t, w, until) // original WC INPUT only; no turn/registration/permission
	p.appliedMS = progressAppliedMS(operation)
	p.appliedAt = time.UnixMilli(p.appliedMS).UTC().Format(time.RFC3339Nano)
	if operation == "capture" {
		wc008NativeExec(t, w, "UPDATE play_history SET item_kind=1,duration_ms=100000,position=1000")
	}
	start, err := readEffectiveJournal(t.Context(), w.db, []string{"play_history"})
	if err != nil || len(start["play_history"].Rows) != 1 {
		t.Fatal("missing original immutable START input", err)
	}
	snapshot := start["play_history"].Rows[0]
	var input wc008HistorySnapshot
	for i, name := range progressSnapshotColumns {
		cell, exists := snapshot[name]
		if !exists {
			t.Fatal("unmodeled original snapshot field", name)
		}
		switch cell.Kind {
		case "integer":
			input.values[i] = cell.Int
		case "text":
			input.values[i] = cell.Text
		case "blob":
			input.values[i] = append([]byte(nil), cell.Blob...)
		case "null":
		default:
			t.Fatal("unsupported original snapshot type", name, cell.Kind)
		}
	}
	if operation == "capture" {
		// Same canonical capture-start population as WC, without its stmttest
		// read turn. The original snapshot is copied directly from native SQL.
		wc008NativeExec(t, w, `UPDATE playback_personal_claims SET pending_time_from_ms=NULL,pending_time_until_ms=NULL,pending_time_cursor_ms=NULL,pending_time_activity_generation=NULL,pending_time_remember_generation=NULL,pending_time_clock_incarnation=NULL,pending_time_start_event_id=NULL,pending_time_occurrence_generation=NULL,occurrence_start_event_id=NULL,last_accepted_sequence=-1`)
		wc008NativeExec(t, w, "DELETE FROM play_history")
		wc008NativeExec(t, w, "UPDATE personal_items SET favorite=1")
		raw, err := json.Marshal(map[string]any{"revision": int64(19), "watched": false, "favorite": true, "watchlisted": false, "progressSeconds": 0, "lastPlayedAt": "", "status": "current", "conflicts": []any{}, "rating": nil})
		if err != nil {
			t.Fatal(err)
		}
		wc008NativeExec(t, w, "INSERT INTO personal_snapshots(profile_id,item_id,revision,state,created_at) VALUES(?,?,19,?,'1970-01-01T00:00:00Z')", p.profileKey, p.subjectID, string(raw))
		p.startEventID, p.lastAcceptedSequence = 0, -1
		if err := w.db.QueryRowContext(t.Context(), "SELECT control_variable_bytes FROM playback_personal_claims").Scan(&p.oldControlBytes); err != nil {
			t.Fatal(err)
		}
	}
	initial, err := readEffectiveJournal(t.Context(), w.db, progressCrashTables)
	if err != nil {
		t.Fatal(err)
	}
	source := progressCrashSource{Operation: operation, Initial: initial, Snapshot: snapshot, Identity: p.identity, Request: RequestDigest{4}, Result: ResultDigest{5}}
	return w, path, wc008CaptureReport{source: p, original: input, positionSeconds: 1, durationSeconds: 100, startedPercent: 1, state: "playing"}, source
}

func progressCrashRegistry(t *testing.T, w *executor) *stmt.Registry {
	t.Helper()
	if schema.SHA256 != progressCrashSchema019 {
		t.Fatal("PROGRESS native captures require schema018", schema.SHA256)
	}
	defs := progressCrashDefinitions()
	if len(defs) != 42 {
		t.Fatal("closed progress definition set changed", len(defs))
	}
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := sqliteconn.Assert(t.Context(), conn, w.config); err != nil {
		t.Fatal(err)
	}
	return nativeFixtureRegistry(t, conn, w.config, defs,
		"wc_008_native_test.go", "wc_008_native_fixture_test.go", "wc_008_draft_test.go", "wc_progress_support_test.go",
		"crash_progress_linux_test.go", "crash_progress_oracle_linux_test.go")
}

func progressCrashTurn(t *testing.T, w *executor, path string, registry *stmt.Registry) (*commandTurn, *governor.Authority) {
	t.Helper()
	// Same W test profile/common governed binder and actual PROGRESS class.
	// Numerical figures and virtual time are synthetic, never qualification.
	recipe := turnRecipe{limit: Work{4096, 1000000, 4096, 32 << 20, 25 * time.Millisecond}, setup: Work{16, 500, 16, 65536, time.Millisecond}, terminal: Work{16, 500, 16, 65536, 8 * time.Millisecond}}
	g, authority, wal, request := governedFixture(t, w.now, governedFixtureProfile{recipe, 8 << 20})
	request.Key.Domain, request.Key.ID, request.Class = uint16(ProgressAccept), "wc-progress-component", governor.Foreground
	request.Footprint = []governor.Scope{{Library: 7, Domain: uint16(ProgressAccept)}}
	if err := refreshWriterWAL(path, wal, w.now); err != nil {
		t.Fatal(err)
	}
	decision := g.Try(request)
	if decision.Grant == nil {
		t.Fatal("actual PROGRESS admission refused", decision.Refusal)
	}
	custody, err := bindGovernedTurn(ProgressAccept, recipe.limit, g, authority, wal, decision.Grant, request)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := w.begin(t.Context(), ProgressAccept, recipe, custody)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	var steps []statementStep
	for _, entry := range registry.Manifest() {
		steps = append(steps, statementStep{entry.ID, Work{16, 2000, 16, 65536, 100 * time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	return turn, authority
}

func progressNativeSequence(operation string) []stmt.ID {
	switch operation {
	case "ack_empty", "ack_capture":
		return []stmt.ID{wcProgressAckEmptyDue}
	case "ack_due":
		return []stmt.ID{wcProgressAckDue}
	case "ack_retry":
		return nil
	case "ring_gap", "ring_capacity":
		return []stmt.ID{wcAPIEventInsert, wcAPIEventOldest, wcAPIEventDelete}
	case "ring_empty":
		return []stmt.ID{wcAPIEventInsert, wcAPIEventOldest}
	}
	var ids []stmt.ID
	if operation == "capture" {
		ids = []stmt.ID{wc008CaptureFence, wc008CaptureProfileFence, wc008CaptureGateEmpty, wc008HistoryAppend,
			wc008CaptureLeaf, wc008CaptureSnapshot, wc008CaptureLeafUpdate, wc008CaptureSnapshotInsert, wc008CaptureInstantRead, wc008CaptureInstantInsert}
	} else {
		ids = []stmt.ID{wc008ProgressPending, wc008ProgressAdjacent, wc008ProgressSpanFind, wc008ProgressSpan}
	}
	for range 3 {
		ids = append(ids, wc008ProgressItemBucketRead, wc008ProgressItemBucket, wc008ProgressProfileBucketRead, wc008ProgressProfileBucket)
	}
	if operation == "capture" {
		return append(ids, wc008CaptureViewer, wc008ProgressHistoryRevision, wc008CaptureClaimEmpty, wc008CaptureReceipt, wc008ProgressOwnerAdvance)
	}
	last := wc008ProgressClear
	if operation == "drain_pending" {
		last = wc008ProgressAdvance
	}
	return append(ids, wc008ProgressLeaf, wc008ProgressViewerRevision, wc008ProgressHistoryRevision, wc008ProgressStartSnapshot, wc008HistoryAppend, last, wc008ProgressReceipt, wc008ProgressOwnerAdvance)
}

func progressCrashPhases(operation string) []string {
	var phases []string
	for index, id := range progressNativeSequence(operation) {
		phases = append(phases, fmt.Sprintf("native_%02d_%s", index, id))
	}
	return phases
}

func progressCaseSupported(c crashCase) bool {
	if c.Domain != "wc_progress" {
		return false
	}
	switch c.Operation {
	case "capture", "drain", "drain_pending", "ack_empty", "ack_due", "ack_retry", "ack_capture", "ring_gap", "ring_empty", "ring_capacity":
	default:
		return false
	}
	if c.CommitFault != "" {
		return c.Operation == "capture" && c.Boundary == "uncertain_return" && (c.CommitFault == "before" || c.CommitFault == "after")
	}
	if c.Boundary == "api_return" {
		return true
	}
	if c.Operation == "ack_retry" || c.Operation == "ack_capture" || c.Operation == "ring_empty" || c.Operation == "ring_capacity" {
		return false
	}
	if c.Operation == "capture" {
		for _, phase := range []string{"before_begin", "after_begin", "before_commit", "after_commit", "physical_return"} {
			if c.Boundary == phase {
				return true
			}
		}
	}
	for index, phase := range progressCrashPhases(c.Operation) {
		if phase == c.Boundary {
			return c.Operation != "drain_pending" || progressNativeSequence(c.Operation)[index] == wc008ProgressAdvance
		}
	}
	return false
}

func progressCrashAckInput(source progressCrashSource) wcProgressAck {
	claim := progressSingle(source.Initial, "playback_personal_claims")
	decision := receiptReplay
	kind := "drain_piece"
	if source.Operation == "ack_capture" {
		kind = "capture_report"
	}
	if source.Operation == "ack_retry" {
		decision = receiptRetired
	}
	due := claim["receipt_reclaim_due_ms"]
	return wcProgressAck{playbackID: "playback-音", reference: ReceiptReference{Identity: source.Identity, RequestDigest: source.Request},
		owner:    receiptOwnerFacts{exists: true, incarnation: source.Identity.Incarnation, lastSequence: 1, acknowledgedSequence: claim["command_acknowledged_sequence"].Int},
		decision: decision, sourceKind: kind, receiptRows: 1, receiptVariableBytes: claim["receipt_variable_bytes"].Int,
		afterSequence: 0, due: sql.NullInt64{Int64: due.Int, Valid: due.Kind == "integer"}, appliedMS: 2000}
}

// All population is test INPUT. The real prior drain is checked against its
// pre-body checkpoint before becoming the ACK baseline; a poisoned candidate
// receipt can never silently supply the expected saved payload.
func progressCrashSupportInput(t *testing.T, w *executor, path string, registry *stmt.Registry, operation string, source *progressCrashSource, input wc008CaptureReport) {
	t.Helper()
	w.now = func() time.Time { return crashClock }
	if strings.HasPrefix(operation, "ack_") {
		want, wantCapture, wantDrain, err := expectedProgress(*source)
		if err != nil {
			t.Fatal(err)
		}
		turn, _ := progressCrashTurn(t, w, path, registry)
		var capture wc008CaptureEffect
		var drain wc008ProgressEffect
		var stored int64
		if operation == "ack_capture" {
			capture, err = applyWC008Capture(t.Context(), turn, input)
			if err == nil {
				stored, err = storeWC008CaptureResult(t.Context(), turn, input, capture, source.Request, source.Result)
			}
		} else {
			drain, err = applyWC008ProgressMetrics(t.Context(), turn, input.source)
			if err == nil {
				stored, err = storeWC008ProgressResult(t.Context(), turn, input.source, drain, source.Request, source.Result)
			}
		}
		if err != nil || capture != wantCapture || drain != wantDrain || stored != want["playback_claim_command_receipts"].Rows[0]["variable_bytes"].Int {
			t.Fatal("original progress return", capture, wantCapture, drain, wantDrain, stored, err)
		}
		if err := turn.closeRows(); err != nil {
			t.Fatal(err)
		}
		actual, err := readEffectiveJournal(t.Context(), turn.transaction.conn, progressCrashTables)
		if err != nil {
			t.Fatal(err)
		}
		if err := compareEffectiveJournal(want, actual); err != nil {
			t.Fatal("original drain checkpoint", err)
		}
		if _, err := turn.transaction.commit(); err != nil {
			t.Fatal(err)
		}
		// Retained cleanup slot input, identical to WC's native retention
		// fixture shape. This is not an ACK or a cleanup implementation.
		wc008NativeExec(t, w, `UPDATE playback_personal_claims SET reclaim_command_sequence=1,reclaim_operation_id=?,reclaim_request_digest=?,reclaim_command_kind='progress_receipt_reclaim',reclaim_result_version=1,reclaim_captured_acknowledged_sequence=0,reclaim_old_after_sequence=0,reclaim_removed_count=0,reclaim_removed_variable_bytes=0,reclaim_result_after_sequence=0,reclaim_result_no_next=1,reclaim_applied_ms=1000,reclaim_result_count=1,reclaim_result_digest=?`, []byte{9, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, make([]byte, 32), append([]byte{11}, make([]byte, 31)...))
		if operation == "ack_due" {
			wc008NativeExec(t, w, "UPDATE playback_personal_claims SET receipt_reclaim_due_ms=17")
		}
	} else {
		source.Event = &progressCrashEvent{Audience: "profile:local:account:音", Type: "progress.changed", ResourceKind: "library", ResourceID: "bibliothèque", Revision: "3", Data: `{"observation":"42"}`, AtMS: 2000}
		switch operation {
		case "ring_gap":
			wc008NativeExec(t, w, `INSERT INTO api_events(id,audience,type,resource_kind,resource_id,revision,data,at_ms) VALUES(1,'admin','list.changed','devices','first','1','old-音',0),(2,'admin','list.changed','devices','second','2','retain-two',1),(100,'admin','list.changed','devices','third','3','retain-three',2)`)
			wc008NativeExec(t, w, "UPDATE sqlite_sequence SET seq=40000 WHERE name='api_events'")
		case "ring_capacity":
			wcNativeAPIEventCapacity(t, w) // finite canonical INPUT only
		case "ring_empty":
		default:
			t.Fatal("unsupported ring population", operation)
		}
	}
	source.Operation = operation
	var err error
	source.Initial, err = readEffectiveJournal(t.Context(), w.db, progressCrashTables)
	if err != nil {
		t.Fatal(err)
	}
	if operation == "ack_retry" {
		original := *source
		original.Operation = "ack_empty"
		want, floor, _, err := expectedProgressAux(original)
		if err != nil {
			t.Fatal(err)
		}
		turn, _ := progressCrashTurn(t, w, path, registry)
		got, err := acknowledgeWCProgress(t.Context(), turn, progressCrashAckInput(original))
		if err != nil || got != floor {
			t.Fatal("original real ACK return", got, floor, err)
		}
		if err := turn.closeRows(); err != nil {
			t.Fatal(err)
		}
		actual, err := readEffectiveJournal(t.Context(), turn.transaction.conn, progressCrashTables)
		if err != nil {
			t.Fatal(err)
		}
		if err := compareEffectiveJournal(want, actual); err != nil {
			t.Fatal("original real ACK journal", err)
		}
		if _, err := turn.transaction.commit(); err != nil {
			t.Fatal(err)
		}
		source.Initial, err = readEffectiveJournal(t.Context(), w.db, progressCrashTables)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func crashProgressFixture(t *testing.T, witness *crashWitness) (*executor, progressCrashSource, []string) {
	t.Helper()
	if witness.Case.Boundary != "trace" && !progressCaseSupported(witness.Case) {
		t.Fatal("unsupported progress cell", witness.Case)
	}
	operation := witness.Case.Operation
	fixtureOperation := operation
	if strings.HasPrefix(operation, "ack_") || strings.HasPrefix(operation, "ring_") {
		fixtureOperation = "drain"
	}
	if operation == "ack_capture" {
		fixtureOperation = "capture"
	}
	w, path, input, source := progressCrashInput(t, fixtureOperation)
	registry := progressCrashRegistry(t, w) // COMPLETE actual Capture, outside command custody
	if operation != fixtureOperation {
		progressCrashSupportInput(t, w, path, registry, operation, &source, input)
	}
	witness.Path, witness.Progress = path, &source
	ids := map[string]stmt.ID{}
	for _, d := range progressCrashDefinitions() {
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
	turn, authority := progressCrashTurn(t, w, path, registry)
	var capture wc008CaptureEffect
	var drain wc008ProgressEffect
	var stored int64
	var floor int64
	var event wcAPIEventEffect
	var err error
	if strings.HasPrefix(operation, "ack_") {
		floor, err = acknowledgeWCProgress(t.Context(), turn, progressCrashAckInput(source))
	} else if strings.HasPrefix(operation, "ring_") {
		q := source.Event
		event, err = appendWCAPIEvent(t.Context(), turn, wcAPIEvent{audience: q.Audience, eventType: q.Type, resourceKind: q.ResourceKind, resourceID: q.ResourceID, revision: q.Revision, data: q.Data, atMS: q.AtMS})
	} else if source.Operation == "capture" {
		capture, err = applyWC008Capture(t.Context(), turn, input)
		if err == nil {
			stored, err = storeWC008CaptureResult(t.Context(), turn, input, capture, source.Request, source.Result)
		}
	} else {
		drain, err = applyWC008ProgressMetrics(t.Context(), turn, input.source)
		if err == nil {
			stored, err = storeWC008ProgressResult(t.Context(), turn, input.source, drain, source.Request, source.Result)
		}
	}
	if err != nil {
		t.Fatal("actual progress component refused", err)
	}
	armed = false
	if !reflect.DeepEqual(sequence, progressCrashPhases(source.Operation)) {
		t.Fatal("unreviewed native progress sequence", sequence, progressCrashPhases(source.Operation))
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	expected, wantCapture, wantDrain, err := expectedProgress(source)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(operation, "ack_") || strings.HasPrefix(operation, "ring_") {
		_, wantFloor, wantEvent, err := expectedProgressAux(source)
		if err != nil || floor != wantFloor || event != wantEvent || stored != 0 || capture != (wc008CaptureEffect{}) || drain != (wc008ProgressEffect{}) {
			t.Fatal("complete private auxiliary return changed", floor, wantFloor, event, wantEvent, err)
		}
	} else if capture != wantCapture || drain != wantDrain || stored != expected["playback_claim_command_receipts"].Rows[0]["variable_bytes"].Int {
		t.Fatal("complete private progress return changed", capture, wantCapture, drain, wantDrain, stored)
	}
	actual, err := readEffectiveJournal(t.Context(), turn.transaction.conn, progressCrashTables)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareEffectiveJournal(expected, actual); err != nil {
		t.Fatal("progress raw state/debt differs from original input", err)
	}
	physical, err := turn.transaction.commit()
	if witness.Case.CommitFault != "" {
		if !errors.Is(err, ErrResolveRequired) || !errors.Is(err, errCrashCommitReply) || physical.state != commitUnknown || !w.ambiguous || !w.fenced {
			t.Fatal("ambiguous progress COMMIT escaped quarantine", physical, err)
		}
		witness.Unknown = true
		crashPoint(t, witness, "uncertain_return")
	} else if err != nil || physical.state != committed || turn.transaction.descriptor.ID != ProgressAccept || turn.transaction.descriptor.Durability != Normal ||
		!turn.transaction.policyVerified || !turn.transaction.commitAttempted || turn.transaction.commitReturnedAt.IsZero() || turn.transaction.physicalReturnedAt.IsZero() || !turn.transaction.retirementComplete {
		t.Fatal("progress frame completion missing", physical, err)
	}
	if authority.Usage() != ([4]governor.Amounts{}) || w.db.Stats().InUse != 0 {
		t.Fatal("progress native/governor custody did not retire")
	}
	crashPoint(t, witness, "api_return") // private component/frame return, never public Applied
	if witness.Case.Boundary != "trace" {
		t.Fatal("progress boundary not reached", witness.Case)
	}
	return w, source, sequence
}

func verifyProgressCrash(t *testing.T, witness crashWitness) {
	t.Helper()
	if !progressCaseSupported(witness.Case) || witness.Progress == nil || witness.Progress.Operation != witness.Case.Operation || witness.Unknown != (witness.Case.CommitFault != "") {
		t.Fatal("unsupported/incomplete original progress witness", witness.Case)
	}
	want := witness.Progress.Initial
	committed := witness.Case.Boundary == "after_commit" || witness.Case.Boundary == "physical_return" || witness.Case.Boundary == "api_return" || witness.Case.CommitFault == "after"
	if committed {
		var err error
		want, _, _, err = expectedProgress(*witness.Progress)
		if err != nil {
			t.Fatal(err)
		}
	}
	e := reopenCrashExecutor(t, witness.Path) // shared policy + canonical existing schema; no initialization/reseed
	for range 2 {
		actual, err := readEffectiveJournal(t.Context(), e.db, progressCrashTables)
		if err != nil {
			t.Fatal(err)
		}
		if err := compareEffectiveJournal(want, actual); err != nil {
			t.Fatal("progress restart violated atomic original journal", err)
		}
	}
	if e.db.Stats().InUse != 0 {
		t.Fatal("progress oracle stranded native custody")
	}
}

func TestCrashReplayProgressComponents(t *testing.T) {
	t.Helper()
	if raceDetector {
		// This sequential child matrix exceeds one shard's race budget. The
		// same cells run without -race; a skipped opt-in target is not evidence.
		t.Skip("crash replay is covered without -race; race child matrix exceeds the shard budget")
	}

	for _, operation := range []string{"capture", "drain", "drain_pending", "ack_empty", "ack_due", "ack_retry", "ack_capture", "ring_gap", "ring_empty", "ring_capacity"} {
		t.Run(operation, func(t *testing.T) {
			witness := crashWitness{Case: crashCase{"wc_progress", operation, "trace", ""}}
			w, _, _ := crashProgressFixture(t, &witness)
			if err := w.close(); err != nil {
				t.Fatal(err)
			}
			phases := progressCrashPhases(operation)
			if operation == "ring_empty" || operation == "ring_capacity" || operation == "ack_capture" {
				phases = nil // shared native statements were already killed in their primary case
			}
			if operation == "capture" {
				phases = append(phases, "before_begin", "after_begin", "before_commit", "after_commit", "physical_return")
			}
			if operation == "drain_pending" {
				var distinct []string
				for i, id := range progressNativeSequence(operation) {
					if id == wc008ProgressAdvance {
						distinct = append(distinct, phases[i])
					}
				}
				phases = distinct // common mechanics/metric uses are covered once by full drain
			}
			for _, phase := range append(phases, "api_return") {
				t.Run(phase, func(t *testing.T) {
					verifyProgressCrash(t, runCrashChild(t, crashCase{"wc_progress", operation, phase, ""}))
				})
			}
			if operation == "capture" {
				for _, fault := range []string{"before", "after"} {
					t.Run("uncertain_return/"+fault, func(t *testing.T) {
						verifyProgressCrash(t, runCrashChild(t, crashCase{"wc_progress", operation, "uncertain_return", fault}))
					})
				}
			}
		})
	}
}

func TestProgressCrashRejectsRawCorruption(t *testing.T) {
	for _, operation := range []string{"capture", "drain", "ack_retry", "ack_capture", "ring_gap"} {
		t.Run(operation, func(t *testing.T) {
			witness := crashWitness{Case: crashCase{"wc_progress", operation, "trace", ""}}
			w, source, _ := crashProgressFixture(t, &witness)
			want, _, _, err := expectedProgress(source)
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
			mutations := []struct{ name, sql string }{
				{"saved_digest", "UPDATE playback_claim_command_receipts SET result_digest=zeroblob(32)"},
				{"exact_debt", "UPDATE playback_personal_claims SET receipt_variable_bytes=receipt_variable_bytes+1"},
				{"manual_state", "UPDATE personal_items SET favorite=1-favorite"},
			}
			if operation == "ring_gap" {
				mutations = []struct{ name, sql string }{
					{"retained_ring_payload", "UPDATE api_events SET data='WRONG-RETAINED' WHERE id=2"},
					{"new_ring_envelope", "UPDATE api_events SET resource_id='WRONG-ENVELOPE' WHERE id=40001"},
				}
			}
			if operation == "ack_capture" {
				mutations = []struct{ name, sql string }{{"retained_pending_range", "UPDATE playback_personal_claims SET pending_time_cursor_ms=1"}}
			}
			for _, mutation := range mutations {
				t.Run(mutation.name, func(t *testing.T) {
					if _, err := conn.ExecContext(t.Context(), "SAVEPOINT corrupt_progress"); err != nil {
						t.Fatal(err)
					}
					defer func() {
						if _, err := conn.ExecContext(t.Context(), "ROLLBACK TO corrupt_progress"); err != nil {
							t.Error(err)
						}
						if _, err := conn.ExecContext(t.Context(), "RELEASE corrupt_progress"); err != nil {
							t.Error(err)
						}
					}()
					result, err := conn.ExecContext(t.Context(), mutation.sql)
					if err != nil {
						t.Fatal("native corruption failed before oracle", err)
					}
					if n, err := result.RowsAffected(); err != nil || n != 1 {
						t.Fatal("native corruption did not alter exactly one original row", n, err)
					}
					actual, err := readEffectiveJournal(t.Context(), conn, progressCrashTables)
					if err != nil {
						t.Fatal(err)
					}
					if err := compareEffectiveJournal(want, actual); err == nil {
						t.Fatal("progress oracle accepted native corruption")
					}
				})
			}
		})
	}
}
