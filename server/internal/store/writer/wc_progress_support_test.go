package writer

import (
	"context"
	"database/sql"
	"math"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/apievents"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// Real domain receipt + debt first, never a synthetic ACK or missing child.
// Only current authority/classification facts remain synthetic component setup.
func wcNativeProgressAckFixture(t *testing.T, w *executor, oldFloor int64, due sql.NullInt64) wcProgressAck {
	t.Helper()
	p := wc008NativeProgress(t, w, 1000)
	p.last, p.acknowledged, p.identity.Sequence = oldFloor, oldFloor, oldFloor+1
	wc008NativeExec(t, w, `UPDATE playback_personal_claims SET command_last_sequence=?,command_acknowledged_sequence=?`, oldFloor, oldFloor)
	turn := wc008NativeTurn(t, w, ProgressAccept, wcProgressDefinitions())
	e, err := applyWC008ProgressMetrics(t.Context(), turn, p)
	if err != nil {
		t.Fatal(err)
	}
	n, err := storeWC008ProgressResult(t.Context(), turn, p, e, RequestDigest{31}, ResultDigest{32})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	if due.Valid {
		wc008NativeExec(t, w, `UPDATE playback_personal_claims SET receipt_reclaim_due_ms=?`, due.Int64)
	}
	return wcProgressAck{playbackID: p.proof.playbackID, reference: ReceiptReference{Identity: p.identity, RequestDigest: RequestDigest{31}}, owner: receiptOwnerFacts{exists: true, incarnation: p.identity.Incarnation, lastSequence: p.identity.Sequence, acknowledgedSequence: oldFloor}, decision: receiptReplay, sourceKind: "drain_piece", receiptRows: 1, receiptVariableBytes: n, due: due, appliedMS: 2000}
}

func TestWCProgressAckFloorDebtAndRetry(t *testing.T) {
	for _, c := range []struct {
		name string
		old  int64
		due  sql.NullInt64
	}{{"new_due", 0, sql.NullInt64{}}, {"existing_due", 0, sql.NullInt64{Int64: 17, Valid: true}}, {"max_floor", math.MaxInt64 - 1, sql.NullInt64{}}, {"retained_cleanup_slot", 1, sql.NullInt64{}}} {
		t.Run(c.name, func(t *testing.T) {
			w := wc008NativeExecutor(t)
			q := wcNativeProgressAckFixture(t, w, c.old, c.due)
			if c.name == "retained_cleanup_slot" {
				wc008NativeExec(t, w, `UPDATE playback_personal_claims SET reclaim_command_sequence=1,reclaim_operation_id=?,reclaim_request_digest=?,reclaim_command_kind='progress_receipt_reclaim',reclaim_result_version=1,reclaim_captured_acknowledged_sequence=0,reclaim_old_after_sequence=0,reclaim_removed_count=0,reclaim_removed_variable_bytes=0,reclaim_result_after_sequence=0,reclaim_result_no_next=1,reclaim_applied_ms=1000,reclaim_result_count=1,reclaim_result_digest=?`, []byte{9, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, make([]byte, 32), []byte{11, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
			}

			turn := wc008NativeTurn(t, w, ProgressAccept, wcProgressAckDefinitions())
			floor, err := acknowledgeWCProgress(t.Context(), turn, q)
			if err != nil || floor != q.owner.lastSequence {
				t.Fatal(floor, err)
			}
			if _, err := turn.transaction.commit(); err != nil {
				t.Fatal(err)
			}
			var last, ack, rows, bytes, cursor, childRows, due int64
			var slot sql.NullInt64
			if err := w.db.QueryRowContext(t.Context(), `SELECT command_last_sequence,command_acknowledged_sequence,receipt_rows,receipt_variable_bytes,receipt_reclaim_after_sequence,receipt_reclaim_due_ms,reclaim_command_sequence,(SELECT count(*) FROM playback_claim_command_receipts) FROM playback_personal_claims`).Scan(&last, &ack, &rows, &bytes, &cursor, &due, &slot, &childRows); err != nil {
				t.Fatal(err)
			}
			if last != floor || ack != floor || rows != 1 || bytes != q.receiptVariableBytes || cursor != 0 || slot.Valid != (c.name == "retained_cleanup_slot") || slot.Valid && slot.Int64 != 1 || childRows != 1 || due < 0 || due > q.appliedMS || c.due.Valid && due > c.due.Int64 {
				t.Fatal("ACK cleared custody or missed debt wake", last, ack, rows, bytes, cursor, slot, childRows, due)
			}

			if c.name == "retained_cleanup_slot" {
				var count int64
				var digest []byte
				if err := w.db.QueryRowContext(t.Context(), `SELECT reclaim_result_count,reclaim_result_digest FROM playback_personal_claims`).Scan(&count, &digest); err != nil || count != 1 || len(digest) != 32 || digest[0] != 11 {
					t.Fatal("ACK lost retained scalar", count, digest, err)
				}
			}
			q.owner.acknowledgedSequence = floor
			q.decision = receiptRetired
			// A retired ACK does not inspect already-reclaimable payload/due facts.
			q.sourceKind = ""
			q.receiptRows, q.receiptVariableBytes, q.appliedMS = -1, -1, -1
			turn = wc008NativeTurn(t, w, ProgressAccept, wcProgressAckDefinitions())
			var before, after int64
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			if got, err := acknowledgeWCProgress(t.Context(), turn, q); err != nil || got != floor {
				t.Fatal(got, err)
			}
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil || before != after {
				t.Fatal("ACK retry mutated native state", before, after, err)
			}
			if _, err := turn.transaction.commit(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWCProgressAckRefusesStaleScopeAndDue(t *testing.T) {
	for _, failure := range []string{"incarnation", "due", "cleanup_kind", "canceled"} {
		t.Run(failure, func(t *testing.T) {
			w := wc008NativeExecutor(t)
			q := wcNativeProgressAckFixture(t, w, 0, sql.NullInt64{})
			if failure == "incarnation" {
				q.reference.Identity.Incarnation[0] = 9
				q.owner.incarnation = q.reference.Identity.Incarnation
			}
			if failure == "due" {
				wc008NativeExec(t, w, `UPDATE playback_personal_claims SET receipt_reclaim_due_ms=777`)
			}
			if failure == "cleanup_kind" {
				q.sourceKind = "progress_receipt_reclaim"
			}
			turn := wc008NativeTurn(t, w, ProgressAccept, wcProgressAckDefinitions())
			ctx := t.Context()
			if failure == "canceled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if floor, err := acknowledgeWCProgress(ctx, turn, q); err == nil || floor != 0 {
				t.Fatal("invalid ACK exposed floor", floor, err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			var floor, rows int64
			if err := w.db.QueryRowContext(t.Context(), `SELECT command_acknowledged_sequence,receipt_rows FROM playback_personal_claims`).Scan(&floor, &rows); err != nil || floor != 0 || rows != 1 {
				t.Fatal(floor, rows, err)
			}
		})
	}
}

func wcNativeAPIEvent() wcAPIEvent {
	return wcAPIEvent{audience: "profile:local:account:音", eventType: "progress.changed", resourceKind: "library", resourceID: "bibliothèque", revision: "3", data: `{"observation":"42"}`, atMS: 2000}
}

// Finite test population, not production census/bootstrap or a runtime writer.
func wcNativeAPIEventCapacity(t *testing.T, w *executor) {
	t.Helper()
	wc008NativeExec(t, w, `WITH RECURSIVE n(id) AS(VALUES(1) UNION ALL SELECT id+1 FROM n WHERE id<20000) INSERT INTO api_events(id,audience,type,resource_kind,resource_id,revision,data,at_ms) SELECT id,'admin','list.changed','devices','old','1','old-音',0 FROM n`)
}

func TestWCAPIEventRingCapacityAndGaps(t *testing.T) {
	w := wc008NativeExecutor(t)
	wcNativeAPIEventCapacity(t, w)
	for step := int64(0); step < 2; step++ {
		if step == 1 {
			wc008NativeExec(t, w, `UPDATE sqlite_sequence SET seq=40000 WHERE name='api_events'`)
		}
		turn := wc008NativeTurn(t, w, ProgressAccept, wcAPIEventDefinitions())
		e, err := appendWCAPIEvent(t.Context(), turn, wcNativeAPIEvent())
		if err != nil || !e.removed || e.removedID != step+1 || e.removedVariableBytes != int64(len("adminlist.changeddevicesold1old-音")) {
			t.Fatal(e, err)
		}
		wantID := int64(20001)
		if step == 1 {
			wantID = 40001
		}
		if e.insertedID != wantID {
			t.Fatal(e)
		}
		if _, err := turn.transaction.commit(); err != nil {
			t.Fatal(err)
		}
		var count, minID, maxID, newBytes int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT count(*),min(id),max(id) FROM api_events`).Scan(&count, &minID, &maxID); err != nil || count != apievents.RingSize || minID != step+2 || maxID != wantID {
			t.Fatal("ring bound/one-row removal failed", count, minID, maxID, err)
		}
		if err := w.db.QueryRowContext(t.Context(), `SELECT length(CAST(audience AS BLOB))+length(CAST(type AS BLOB))+length(CAST(resource_kind AS BLOB))+length(CAST(resource_id AS BLOB))+length(CAST(revision AS BLOB))+length(CAST(data AS BLOB)) FROM api_events WHERE id=?`, wantID).Scan(&newBytes); err != nil || newBytes != e.insertedVariableBytes {
			t.Fatal("native UTF8 byte charge mismatch", newBytes, e, err)
		}
	}
}

func TestWCAPIEventNoVictimAndParentRollback(t *testing.T) {
	for _, capacity := range []bool{false, true} {
		w := wc008NativeExecutor(t)
		if capacity {
			wcNativeAPIEventCapacity(t, w)
		}
		turn := wc008NativeTurn(t, w, ProgressAccept, wcAPIEventDefinitions())
		e, err := appendWCAPIEvent(t.Context(), turn, wcNativeAPIEvent())
		if err != nil || e.insertedID <= 0 || e.removed != capacity {
			t.Fatal(e, err)
		}
		if !capacity && (e.removedID != 0 || e.removedVariableBytes != 0) {
			t.Fatal("fabricated victim", e)
		}
		// Any later parent receipt/event/terminal refusal rolls back BOTH mutations.
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
		var count, minID, maxID int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT count(*),coalesce(min(id),0),coalesce(max(id),0) FROM api_events`).Scan(&count, &minID, &maxID); err != nil {
			t.Fatal(err)
		}
		if !capacity && count != 0 || capacity && (count != 20000 || minID != 1 || maxID != 20000) {
			t.Fatal("parent rollback lost ring state", count, minID, maxID)
		}
	}
}

func TestWCAPIEventNativeIDExhaustion(t *testing.T) {
	w := wc008NativeExecutor(t)
	wc008NativeExec(t, w, `INSERT INTO api_events(audience,type,at_ms) VALUES('admin','list.changed',0)`)
	wc008NativeExec(t, w, `UPDATE sqlite_sequence SET seq=? WHERE name='api_events'`, int64(math.MaxInt64-1))
	turn := wc008NativeTurn(t, w, ProgressAccept, wcAPIEventDefinitions())
	e, err := appendWCAPIEvent(t.Context(), turn, wcNativeAPIEvent())
	if err != nil || e.insertedID != math.MaxInt64 || !e.removed || e.removedID != 1 {
		t.Fatal(e, err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	turn = wc008NativeTurn(t, w, ProgressAccept, wcAPIEventDefinitions())
	if e, err := appendWCAPIEvent(t.Context(), turn, wcNativeAPIEvent()); err == nil || e != (wcAPIEventEffect{}) {
		t.Fatal("native ID exhaustion exposed effects", e, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM api_events`).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}

func TestWCProgressSupportClosedGrammar(t *testing.T) {
	w := wc008NativeExecutor(t)
	defs := append(wcProgressAckDefinitions(), wcAPIEventDefinitions()...)
	for _, d := range defs {
		t.Run(string(d.ID), func(t *testing.T) {
			if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
				t.Fatal(issues)
			}
		})
	}
	wc008NativePrepare(t, w, defs)
	if _, err := acknowledgeWCProgress(t.Context(), nil, wcProgressAck{}); err == nil {
		t.Fatal("nil turn ACK")
	}
	if _, err := appendWCAPIEvent(t.Context(), nil, wcAPIEvent{}); err == nil {
		t.Fatal("nil turn append")
	}
}

func TestWCAPIEventDeleteFailureRollsBackParent(t *testing.T) {
	w := wc008NativeExecutor(t)
	wcNativeAPIEventCapacity(t, w)
	turn := wc008NativeTurn(t, w, ProgressAccept, wcAPIEventDefinitions())
	delete(turn.steps, wcAPIEventDelete) // fault after INSERT and oldest-row read
	if out, err := appendWCAPIEvent(t.Context(), turn, wcNativeAPIEvent()); err == nil || out != (wcAPIEventEffect{}) {
		t.Fatal("failed delete exposed partial effect", out, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	var count, minID, maxID int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*),min(id),max(id) FROM api_events`).Scan(&count, &minID, &maxID); err != nil || count != 20000 || minID != 1 || maxID != 20000 {
		t.Fatal("failed delete left new hint or lost victim", count, minID, maxID, err)
	}
}

func TestWCProgressCaptureAckDoesNotDrainPending(t *testing.T) {
	w := wc008NativeExecutor(t)
	capture := wcCaptureFixture(t, w)
	turn := wc008NativeTurn(t, w, ProgressAccept, wcProgressDefinitions())
	e, err := applyWC008Capture(t.Context(), turn, capture)
	if err != nil {
		t.Fatal(err)
	}
	n, err := storeWC008CaptureResult(t.Context(), turn, capture, e, RequestDigest{31}, ResultDigest{32})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	p := capture.source
	ack := wcProgressAck{playbackID: p.proof.playbackID, reference: ReceiptReference{Identity: p.identity, RequestDigest: RequestDigest{31}}, owner: receiptOwnerFacts{exists: true, incarnation: p.identity.Incarnation, lastSequence: 1}, decision: receiptReplay, sourceKind: "capture_report", receiptRows: 1, receiptVariableBytes: n, appliedMS: 2000}
	turn = wc008NativeTurn(t, w, ProgressAccept, wcProgressAckDefinitions())
	if floor, err := acknowledgeWCProgress(t.Context(), turn, ack); err != nil || floor != 1 {
		t.Fatal(floor, err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	var floor, report, cursor, until, rows, bytes, spanRows, played int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT command_acknowledged_sequence,pending_time_sequence,pending_time_cursor_ms,pending_time_until_ms,receipt_rows,receipt_variable_bytes,(SELECT count(*) FROM listening_time_spans),(SELECT played_ms FROM personal_items) FROM playback_personal_claims`).Scan(&floor, &report, &cursor, &until, &rows, &bytes, &spanRows, &played); err != nil || floor != 1 || report != 13 || cursor != 0 || until != 1000 || rows != 1 || bytes != n || spanRows != 0 || played != 0 {
		t.Fatal("capture ACK consumed pending proof or refunded result custody", floor, report, cursor, until, rows, bytes, spanRows, played, err)
	}
}
