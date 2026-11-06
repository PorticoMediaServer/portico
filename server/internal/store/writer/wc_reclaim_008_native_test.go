package writer

import (
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	"strings"
	"testing"
)

func wc008NativeReclaim(t *testing.T, w *executor) wc008ReclaimCommand {
	t.Helper()
	p := wc008NativeProgress(t, w, 1000)
	// TEST ORACLE setup: capture and drain receipts use different source units.
	wc008NativeExec(t, w, `INSERT INTO playback_claim_command_receipts(playback_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,result_version,applied_ms,source_accepted_ordinal,source_last_accepted_sequence,source_report_sequence,source_occurrence_generation,source_activity_generation,source_history_policy_generation,source_clock_incarnation,result_start_event_id,result_report_sequence,result_pending_sequence,capture_created_start,result_count,result_digest)
 VALUES(?,?,1,?,?,'capture_report',1,1,71,-1,13,5,1,1,?,1,13,13,1,1,?)`, p.proof.playbackID, p.identity.Incarnation[:], []byte(strings.Repeat("x", 16)), make([]byte, 32), p.proof.clockIncarnation[:], make([]byte, 32))
	wc008NativeExec(t, w, `INSERT INTO playback_claim_command_receipts(playback_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,result_version,applied_ms,source_accepted_ordinal,source_last_accepted_sequence,source_report_sequence,source_occurrence_generation,source_activity_generation,source_history_policy_generation,source_clock_incarnation,source_start_event_id,source_pending_from_ms,source_pending_until_ms,source_pending_cursor_ms,result_start_event_id,result_report_sequence,result_pending_sequence,result_viewer_revision,result_observation_revision,result_history_revision,result_personal_revision,consumed_from_ms,consumed_until_ms,result_next_cursor_ms,finished,credited_ms,result_count,result_digest)
 VALUES(?,?,3,?,?,'drain_piece',1,2,71,17,13,5,1,1,?,1,0,1000,0,1,17,13,23,42,32,19,0,1000,1000,1,1000,1,?)`, p.proof.playbackID, p.identity.Incarnation[:], []byte(strings.Repeat("y", 16)), make([]byte, 32), p.proof.clockIncarnation[:], make([]byte, 32))
	wc008NativeExec(t, w, `UPDATE playback_personal_claims SET command_last_sequence=4,command_acknowledged_sequence=4,receipt_rows=2,receipt_variable_bytes=(SELECT sum(variable_bytes) FROM playback_claim_command_receipts),receipt_reclaim_due_ms=123`)
	q := wc008ReclaimCommand{owner: wcReclaimOwner{playbackID: p.proof.playbackID, incarnation: p.identity.Incarnation,
		lastSequence: 4, acknowledgedSequence: 4, rows: 2, due: true, dueMS: 123},
		identity: CommandIdentity{Incarnation: p.identity.Incarnation, Sequence: 5, OperationID: [16]byte{9}}, appliedMS: 124, resultVersion: 1}
	if err := w.db.QueryRowContext(t.Context(), `SELECT receipt_variable_bytes,control_variable_bytes FROM playback_personal_claims`).Scan(&q.owner.variableBytes, &q.oldControlBytes); err != nil {
		t.Fatal(err)
	}
	return q
}

func TestWC008NativeReclaimCaptureThenDrainAndNoNext(t *testing.T) {
	w := wc008NativeExecutor(t)
	q := wc008NativeReclaim(t, w)
	for step := 0; step < 2; step++ {
		if step == 1 {
			// The supplied old proof can agree with itself yet disagree with
			// the native fixed slot. A post-delete CAS miss rolls it ALL back.
			stale := q
			stale.oldSlot.appliedMS++
			stale.expectedOldSlot = stale.oldSlot
			turn := wc008NativeTurn(t, w, ProgressAccept, wcReclaimDefinitions())
			e, err := applyWC008ReclaimChild(t.Context(), turn, stale)
			if err != nil {
				t.Fatal(err)
			}
			if n, err := storeWC008ReclaimResult(t.Context(), turn, stale, e, RequestDigest{11}, ResultDigest{12}); err == nil || n != 0 {
				t.Fatal("native OLD slot mismatch accepted", n, err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			var remaining int64
			if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM playback_claim_command_receipts WHERE command_sequence=3`).Scan(&remaining); err != nil || remaining != 1 {
				t.Fatal("source delete survived failed slot CAS", remaining, err)
			}
		}
		turn := wc008NativeTurn(t, w, ProgressAccept, wcReclaimDefinitions()) // synthetic frame only; no new purpose ID minted
		e, err := applyWC008ReclaimChild(t.Context(), turn, q)
		if err != nil || !e.prepared.remove || e.prepared.key.sequence != int64(1+2*step) || e.prepared.noNext != (step == 1) {
			t.Fatal("actual first native source child", e, err)
		}
		charge, err := storeWC008ReclaimResult(t.Context(), turn, q, e, RequestDigest{11}, ResultDigest{12})
		if err != nil || charge <= 0 {
			t.Fatal("native fixed cleanup scalar", charge, err)
		}
		if _, err := turn.transaction.commit(); err != nil {
			t.Fatal(err)
		}
		wc008NativeExec(t, w, `UPDATE playback_personal_claims SET command_acknowledged_sequence=command_last_sequence`)
		q.oldSlot = wcReclaimSlot{present: true, reference: ReceiptReference{Identity: q.identity, RequestDigest: RequestDigest{11}},
			resultDigest: [32]byte{12}, floor: q.owner.acknowledgedSequence, oldCursor: q.owner.afterSequence,
			removedSequence: e.prepared.key.sequence, removedCount: 1, removedBytes: e.prepared.removedVariableBytes,
			nextCursor: e.prepared.afterSequence, noNext: e.prepared.noNext, appliedMS: q.appliedMS}
		q.expectedOldSlot, q.oldResultVersion = q.oldSlot, q.resultVersion
		q.owner.afterSequence, q.owner.rows, q.owner.variableBytes = e.prepared.afterSequence, e.prepared.remainingRows, e.prepared.remainingVariableBytes
		q.owner.lastSequence, q.owner.acknowledgedSequence = q.identity.Sequence, q.identity.Sequence
		q.oldControlBytes = charge
		q.identity.Sequence++
		q.identity.OperationID[0]++
	}
	var due sql.NullInt64
	var rows, bytes, cursor, ack, count int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT receipt_rows,receipt_variable_bytes,receipt_reclaim_after_sequence,command_acknowledged_sequence,receipt_reclaim_due_ms,reclaim_result_count FROM playback_personal_claims`).Scan(&rows, &bytes, &cursor, &ack, &due, &count); err != nil || rows != 0 || bytes != 0 || cursor != 3 || ack != 6 || due.Valid || count != 1 {
		t.Fatal("final native debt/floor/slot or idle-loop state", rows, bytes, cursor, ack, due, count, err)
	}
}

func TestWC008NativeReclaimScheduledZeroRemoval(t *testing.T) {
	w := wc008NativeExecutor(t)
	p := wc008NativeProgress(t, w, 1000)
	wc008NativeExec(t, w, `UPDATE playback_personal_claims SET receipt_reclaim_due_ms=123`)
	q := wc008ReclaimCommand{owner: wcReclaimOwner{playbackID: p.proof.playbackID, incarnation: p.identity.Incarnation, due: true, dueMS: 123},
		identity: CommandIdentity{Incarnation: p.identity.Incarnation, Sequence: 1, OperationID: [16]byte{9}}, appliedMS: 124, resultVersion: 1}
	if err := w.db.QueryRowContext(t.Context(), `SELECT control_variable_bytes FROM playback_personal_claims`).Scan(&q.oldControlBytes); err != nil {
		t.Fatal(err)
	}
	turn := wc008NativeTurn(t, w, ProgressAccept, wcReclaimDefinitions())
	e, err := applyWC008ReclaimChild(t.Context(), turn, q)
	if err != nil || e.prepared.remove || !e.prepared.noNext {
		t.Fatal("native scheduled empty boundary", e, err)
	}
	if _, err := storeWC008ReclaimResult(t.Context(), turn, q, e, RequestDigest{11}, ResultDigest{12}); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	wc008NativeExec(t, w, `UPDATE playback_personal_claims SET command_acknowledged_sequence=command_last_sequence`)
	var removed, due sql.NullInt64
	var count, bytes, cursor, resultCount int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT reclaim_removed_sequence,reclaim_removed_count,reclaim_removed_variable_bytes,receipt_reclaim_after_sequence,receipt_reclaim_due_ms,reclaim_result_count FROM playback_personal_claims`).Scan(&removed, &count, &bytes, &cursor, &due, &resultCount); err != nil || removed.Valid || count != 0 || bytes != 0 || cursor != 0 || due.Valid || resultCount != 1 {
		t.Fatal("zero-removal NULL/count1/debit/idle shape", removed, count, bytes, cursor, due, resultCount, err)
	}
	// A later source ACK can redue real debt after a zero-removal scalar.
	// Native fixture setup only: ACK/scheduling is composed by W in production.
	old := wcReclaimSlot{present: true, reference: ReceiptReference{Identity: q.identity, RequestDigest: RequestDigest{11}}, resultDigest: [32]byte{12}, noNext: true, appliedMS: q.appliedMS}
	wc008NativeExec(t, w, `INSERT INTO playback_claim_command_receipts(playback_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,result_version,applied_ms,source_accepted_ordinal,source_last_accepted_sequence,source_report_sequence,source_occurrence_generation,source_activity_generation,source_history_policy_generation,source_clock_incarnation,result_start_event_id,result_report_sequence,result_pending_sequence,capture_created_start,result_count,result_digest) VALUES(?,?,2,?,?,'capture_report',1,125,71,-1,13,5,1,1,?,1,13,13,1,1,?)`, p.proof.playbackID, p.identity.Incarnation[:], []byte(strings.Repeat("z", 16)), make([]byte, 32), p.proof.clockIncarnation[:], make([]byte, 32))
	wc008NativeExec(t, w, `UPDATE playback_personal_claims SET command_last_sequence=2,command_acknowledged_sequence=2,receipt_rows=1,receipt_variable_bytes=(SELECT variable_bytes FROM playback_claim_command_receipts),receipt_reclaim_due_ms=200`)
	q.oldSlot, q.expectedOldSlot, q.oldResultVersion = old, old, 1
	q.owner.lastSequence, q.owner.acknowledgedSequence, q.owner.rows, q.owner.dueMS = 2, 2, 1, 200
	q.identity.Sequence, q.appliedMS = 3, 201
	q.identity.OperationID[0]++
	if err := w.db.QueryRowContext(t.Context(), `SELECT receipt_variable_bytes,control_variable_bytes FROM playback_personal_claims`).Scan(&q.owner.variableBytes, &q.oldControlBytes); err != nil {
		t.Fatal(err)
	}
	turn = wc008NativeTurn(t, w, ProgressAccept, wcReclaimDefinitions())
	e, err = applyWC008ReclaimChild(t.Context(), turn, q)
	if err != nil || !e.prepared.remove || e.prepared.key.sequence != 2 || !e.prepared.noNext {
		t.Fatal(e, err)
	}
	if _, err := storeWC008ReclaimResult(t.Context(), turn, q, e, RequestDigest{13}, ResultDigest{14}); err != nil {
		t.Fatal("old zero-removal slot did not compare NULL exactly", err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}

}

func TestWCReclaimClosedGrammarAndPrepare(t *testing.T) {
	w := wc008NativeExecutor(t)
	for _, d := range wcReclaimDefinitions() {
		t.Run(string(d.ID), func(t *testing.T) {
			if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
				t.Fatal(issues)
			}
		})
	}
	wc008NativePrepare(t, w, wcReclaimDefinitions())
}
