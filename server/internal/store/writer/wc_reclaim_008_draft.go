package writer

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Private CHANGE-008 components. Adopted
// CHANGE-008 debd5c77 supplies exact fields, NULL groups and kinds. W must supply current
// retained-owner maintenance authority, original classification and the SAME
// admitted turn; this file cannot mint authority, register/open a writer,
// encode a common receipt, acknowledge a result or complete a transaction.
const (
	wc008ReclaimFirst        stmt.ID = "wc.reclaim.first_child"
	wc008ReclaimDelete       stmt.ID = "wc.reclaim.delete_child"
	wc008ReclaimStoreEmpty   stmt.ID = "wc.reclaim.store_empty"
	wc008ReclaimStoreZero    stmt.ID = "wc.reclaim.store_zero"
	wc008ReclaimStoreRemoved stmt.ID = "wc.reclaim.store_removed"
)

func wcReclaimDefinitions() []stmt.Definition {
	return []stmt.Definition{
		{ID: wc008ReclaimFirst, Owner: "store/writer WC reclaim", Kind: stmt.Read,
			CallSites: []string{"store/writer/wc_reclaim_008_draft.go"},
			SQL: `SELECT command_sequence,command_kind,variable_bytes
 FROM playback_claim_command_receipts
 WHERE playback_id=? AND command_incarnation=? AND command_sequence>? AND command_sequence<=?
 ORDER BY command_sequence LIMIT 1`,
			Params: []stmt.Param{{Name: "playback", Type: stmt.Text}, {Name: "incarnation", Type: stmt.Blob},
				{Name: "after", Type: stmt.Integer}, {Name: "ack", Type: stmt.Integer}},
			Cohorts: []stmt.Cohort{{Name: "first", Bindings: []any{"playback-音", []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, int64(0), int64(4)}}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"playback_claim_command_receipts"}}},
		{ID: wc008ReclaimDelete, Owner: "store/writer WC reclaim", Kind: stmt.Write,
			CallSites: []string{"store/writer/wc_reclaim_008_draft.go"},
			SQL: `DELETE FROM playback_claim_command_receipts
 WHERE playback_id=? AND command_incarnation=? AND command_sequence=? AND variable_bytes=?
 RETURNING variable_bytes`,
			Params: []stmt.Param{{Name: "playback", Type: stmt.Text}, {Name: "incarnation", Type: stmt.Blob},
				{Name: "sequence", Type: stmt.Integer}, {Name: "native_bytes", Type: stmt.Integer}},
			Cohorts: []stmt.Cohort{{Name: "delete", Bindings: []any{"playback-音", []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, int64(1), int64(137)}}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"playback_claim_command_receipts"}}},
		{ID: wc008ReclaimStoreEmpty, Owner: "store/writer WC reclaim", Kind: stmt.Write,
			CallSites: []string{"store/writer/wc_reclaim_008_draft.go"},
			SQL: `UPDATE playback_personal_claims SET command_last_sequence=?,receipt_rows=?,receipt_variable_bytes=?,
 receipt_reclaim_after_sequence=?,receipt_reclaim_due_ms=?,reclaim_command_sequence=?,reclaim_operation_id=?,reclaim_request_digest=?,
 reclaim_captured_acknowledged_sequence=?,reclaim_old_after_sequence=?,reclaim_removed_sequence=?,reclaim_removed_count=?,
 reclaim_removed_variable_bytes=?,reclaim_result_after_sequence=?,reclaim_result_no_next=?,reclaim_applied_ms=?,
 reclaim_command_kind='progress_receipt_reclaim',reclaim_result_version=?,reclaim_result_count=1,reclaim_result_digest=?
 WHERE playback_id=? AND command_incarnation=? AND command_last_sequence=? AND command_acknowledged_sequence=?
 AND receipt_rows=? AND receipt_variable_bytes=? AND receipt_reclaim_after_sequence=? AND receipt_reclaim_due_ms=?
 AND reclaim_command_sequence IS NULL AND reclaim_operation_id IS NULL AND reclaim_request_digest IS NULL
 AND reclaim_captured_acknowledged_sequence IS NULL AND reclaim_old_after_sequence IS NULL AND reclaim_removed_sequence IS NULL
 AND reclaim_removed_count IS NULL AND reclaim_removed_variable_bytes IS NULL AND reclaim_result_after_sequence IS NULL
 AND reclaim_result_no_next IS NULL AND reclaim_applied_ms IS NULL AND reclaim_result_count IS NULL AND reclaim_result_digest IS NULL
 AND reclaim_command_kind IS NULL AND reclaim_result_version IS NULL AND control_variable_bytes=?
 RETURNING control_variable_bytes`,
			Params: []stmt.Param{
				{Name: "last_next", Type: stmt.Integer}, {Name: "rows_next", Type: stmt.Integer}, {Name: "bytes_next", Type: stmt.Integer},
				{Name: "cursor_next", Type: stmt.Integer}, {Name: "due_next", Type: stmt.Integer, Nullable: true},
				{Name: "result_sequence", Type: stmt.Integer}, {Name: "operation", Type: stmt.Blob}, {Name: "request", Type: stmt.Blob},
				{Name: "source_ack", Type: stmt.Integer}, {Name: "old_cursor", Type: stmt.Integer},
				{Name: "removed_sequence", Type: stmt.Integer, Nullable: true}, {Name: "removed_count", Type: stmt.Integer},
				{Name: "removed_bytes", Type: stmt.Integer}, {Name: "result_cursor", Type: stmt.Integer},
				{Name: "no_next", Type: stmt.Integer}, {Name: "applied", Type: stmt.Integer}, {Name: "result_version", Type: stmt.Integer}, {Name: "result", Type: stmt.Blob},
				{Name: "playback", Type: stmt.Text}, {Name: "incarnation", Type: stmt.Blob},
				{Name: "last", Type: stmt.Integer}, {Name: "ack", Type: stmt.Integer}, {Name: "old_rows", Type: stmt.Integer},
				{Name: "old_bytes", Type: stmt.Integer}, {Name: "old_owner_cursor", Type: stmt.Integer}, {Name: "old_due", Type: stmt.Integer},

				{Name: "old_control_bytes", Type: stmt.Integer}},
			Cohorts: []stmt.Cohort{{Name: "storeempty", Bindings: []any{int64(5), int64(1), int64(137), int64(1), int64(123), int64(5), []byte{9, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, []byte{11, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, int64(4), int64(0), int64(1), int64(1), int64(137), int64(1), int64(0), int64(124), int64(1), []byte{12, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, "playback-音", []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, int64(4), int64(4), int64(2), int64(274), int64(0), int64(123), int64(153)}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_playback_personal_claims_2"}}},
		{ID: wc008ReclaimStoreZero, Owner: "store/writer WC reclaim", Kind: stmt.Write,
			CallSites: []string{"store/writer/wc_reclaim_008_draft.go"},
			SQL: `UPDATE playback_personal_claims INDEXED BY playback_reclaim_operation SET command_last_sequence=?,receipt_rows=?,receipt_variable_bytes=?,
 receipt_reclaim_after_sequence=?,receipt_reclaim_due_ms=?,reclaim_command_sequence=?,reclaim_operation_id=?,reclaim_request_digest=?,
 reclaim_captured_acknowledged_sequence=?,reclaim_old_after_sequence=?,reclaim_removed_sequence=?,reclaim_removed_count=?,
 reclaim_removed_variable_bytes=?,reclaim_result_after_sequence=?,reclaim_result_no_next=?,reclaim_applied_ms=?,
 reclaim_command_kind='progress_receipt_reclaim',reclaim_result_version=?,reclaim_result_count=1,reclaim_result_digest=?
 WHERE playback_id=? AND command_incarnation=? AND command_last_sequence=? AND command_acknowledged_sequence=?
 AND receipt_rows=? AND receipt_variable_bytes=? AND receipt_reclaim_after_sequence=? AND receipt_reclaim_due_ms=?
 AND reclaim_command_sequence=? AND reclaim_operation_id=? AND reclaim_request_digest=?
 AND reclaim_captured_acknowledged_sequence=? AND reclaim_old_after_sequence=? AND reclaim_removed_sequence IS NULL
 AND reclaim_removed_count=? AND reclaim_removed_variable_bytes=? AND reclaim_result_after_sequence=?
 AND reclaim_result_no_next=? AND reclaim_applied_ms=? AND reclaim_result_count=? AND reclaim_result_digest=?
 AND reclaim_command_kind=? AND reclaim_result_version=? AND control_variable_bytes=?
 RETURNING control_variable_bytes`,
			Params: []stmt.Param{
				{Name: "last_next", Type: stmt.Integer}, {Name: "rows_next", Type: stmt.Integer}, {Name: "bytes_next", Type: stmt.Integer},
				{Name: "cursor_next", Type: stmt.Integer}, {Name: "due_next", Type: stmt.Integer, Nullable: true},
				{Name: "result_sequence", Type: stmt.Integer}, {Name: "operation", Type: stmt.Blob}, {Name: "request", Type: stmt.Blob},
				{Name: "source_ack", Type: stmt.Integer}, {Name: "old_cursor", Type: stmt.Integer},
				{Name: "removed_sequence", Type: stmt.Integer, Nullable: true}, {Name: "removed_count", Type: stmt.Integer},
				{Name: "removed_bytes", Type: stmt.Integer}, {Name: "result_cursor", Type: stmt.Integer},
				{Name: "no_next", Type: stmt.Integer}, {Name: "applied", Type: stmt.Integer}, {Name: "result_version", Type: stmt.Integer}, {Name: "result", Type: stmt.Blob},
				{Name: "playback", Type: stmt.Text}, {Name: "incarnation", Type: stmt.Blob},
				{Name: "last", Type: stmt.Integer}, {Name: "ack", Type: stmt.Integer}, {Name: "old_rows", Type: stmt.Integer},
				{Name: "old_bytes", Type: stmt.Integer}, {Name: "old_owner_cursor", Type: stmt.Integer}, {Name: "old_due", Type: stmt.Integer},
				{Name: "old_result_sequence", Type: stmt.Integer}, {Name: "old_operation", Type: stmt.Blob},
				{Name: "old_request", Type: stmt.Blob}, {Name: "old_source_ack", Type: stmt.Integer},
				{Name: "old_slot_cursor", Type: stmt.Integer},
				{Name: "old_removed_count", Type: stmt.Integer}, {Name: "old_removed_bytes", Type: stmt.Integer},
				{Name: "old_result_cursor", Type: stmt.Integer}, {Name: "old_no_next", Type: stmt.Integer},
				{Name: "old_applied", Type: stmt.Integer}, {Name: "old_result_count", Type: stmt.Integer},
				{Name: "old_result", Type: stmt.Blob},
				{Name: "old_kind", Type: stmt.Text}, {Name: "old_version", Type: stmt.Integer},
				{Name: "old_control_bytes", Type: stmt.Integer}},
			Cohorts: []stmt.Cohort{{Name: "storezero", Bindings: []any{int64(5), int64(1), int64(137), int64(1), int64(123), int64(5), []byte{9, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, []byte{11, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, int64(4), int64(0), int64(1), int64(1), int64(137), int64(1), int64(0), int64(124), int64(1), []byte{12, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, "playback-音", []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, int64(4), int64(4), int64(2), int64(274), int64(0), int64(123), int64(2), []byte{9, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, []byte{11, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, int64(1), int64(0), int64(1), int64(137), int64(1), int64(1), int64(120), int64(1), []byte{12, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, "progress_receipt_reclaim", int64(1), int64(153)}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"playback_reclaim_operation"}}},
		{ID: wc008ReclaimStoreRemoved, Owner: "store/writer WC reclaim", Kind: stmt.Write,
			CallSites: []string{"store/writer/wc_reclaim_008_draft.go"},
			SQL: `UPDATE playback_personal_claims INDEXED BY playback_reclaim_operation SET command_last_sequence=?,receipt_rows=?,receipt_variable_bytes=?,
 receipt_reclaim_after_sequence=?,receipt_reclaim_due_ms=?,reclaim_command_sequence=?,reclaim_operation_id=?,reclaim_request_digest=?,
 reclaim_captured_acknowledged_sequence=?,reclaim_old_after_sequence=?,reclaim_removed_sequence=?,reclaim_removed_count=?,
 reclaim_removed_variable_bytes=?,reclaim_result_after_sequence=?,reclaim_result_no_next=?,reclaim_applied_ms=?,
 reclaim_command_kind='progress_receipt_reclaim',reclaim_result_version=?,reclaim_result_count=1,reclaim_result_digest=?
 WHERE playback_id=? AND command_incarnation=? AND command_last_sequence=? AND command_acknowledged_sequence=?
 AND receipt_rows=? AND receipt_variable_bytes=? AND receipt_reclaim_after_sequence=? AND receipt_reclaim_due_ms=?
 AND reclaim_command_sequence=? AND reclaim_operation_id=? AND reclaim_request_digest=?
 AND reclaim_captured_acknowledged_sequence=? AND reclaim_old_after_sequence=? AND reclaim_removed_sequence=?
 AND reclaim_removed_count=? AND reclaim_removed_variable_bytes=? AND reclaim_result_after_sequence=?
 AND reclaim_result_no_next=? AND reclaim_applied_ms=? AND reclaim_result_count=? AND reclaim_result_digest=?
 AND reclaim_command_kind=? AND reclaim_result_version=? AND control_variable_bytes=?
 RETURNING control_variable_bytes`,
			Params: []stmt.Param{
				{Name: "last_next", Type: stmt.Integer}, {Name: "rows_next", Type: stmt.Integer}, {Name: "bytes_next", Type: stmt.Integer},
				{Name: "cursor_next", Type: stmt.Integer}, {Name: "due_next", Type: stmt.Integer, Nullable: true},
				{Name: "result_sequence", Type: stmt.Integer}, {Name: "operation", Type: stmt.Blob}, {Name: "request", Type: stmt.Blob},
				{Name: "source_ack", Type: stmt.Integer}, {Name: "old_cursor", Type: stmt.Integer},
				{Name: "removed_sequence", Type: stmt.Integer, Nullable: true}, {Name: "removed_count", Type: stmt.Integer},
				{Name: "removed_bytes", Type: stmt.Integer}, {Name: "result_cursor", Type: stmt.Integer},
				{Name: "no_next", Type: stmt.Integer}, {Name: "applied", Type: stmt.Integer}, {Name: "result_version", Type: stmt.Integer}, {Name: "result", Type: stmt.Blob},
				{Name: "playback", Type: stmt.Text}, {Name: "incarnation", Type: stmt.Blob},
				{Name: "last", Type: stmt.Integer}, {Name: "ack", Type: stmt.Integer}, {Name: "old_rows", Type: stmt.Integer},
				{Name: "old_bytes", Type: stmt.Integer}, {Name: "old_owner_cursor", Type: stmt.Integer}, {Name: "old_due", Type: stmt.Integer},
				{Name: "old_result_sequence", Type: stmt.Integer}, {Name: "old_operation", Type: stmt.Blob},
				{Name: "old_request", Type: stmt.Blob}, {Name: "old_source_ack", Type: stmt.Integer},
				{Name: "old_slot_cursor", Type: stmt.Integer}, {Name: "old_removed_sequence", Type: stmt.Integer},
				{Name: "old_removed_count", Type: stmt.Integer}, {Name: "old_removed_bytes", Type: stmt.Integer},
				{Name: "old_result_cursor", Type: stmt.Integer}, {Name: "old_no_next", Type: stmt.Integer},
				{Name: "old_applied", Type: stmt.Integer}, {Name: "old_result_count", Type: stmt.Integer},
				{Name: "old_result", Type: stmt.Blob},
				{Name: "old_kind", Type: stmt.Text}, {Name: "old_version", Type: stmt.Integer},
				{Name: "old_control_bytes", Type: stmt.Integer}},
			Cohorts: []stmt.Cohort{{Name: "storeremoved", Bindings: []any{int64(5), int64(1), int64(137), int64(1), int64(123), int64(5), []byte{9, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, []byte{11, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, int64(4), int64(0), int64(1), int64(1), int64(137), int64(1), int64(0), int64(124), int64(1), []byte{12, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, "playback-音", []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, int64(4), int64(4), int64(2), int64(274), int64(0), int64(123), int64(2), []byte{9, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, []byte{11, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, int64(1), int64(0), int64(1), int64(1), int64(137), int64(1), int64(1), int64(120), int64(1), []byte{12, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, "progress_receipt_reclaim", int64(1), int64(153)}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"playback_reclaim_operation"}}},
	}
}

type wc008ReclaimCommand struct {
	owner                                            wcReclaimOwner // W's fresh native owner snapshot
	identity                                         CommandIdentity
	expectedOldSlot, oldSlot                         wcReclaimSlot // W validates kind/NULL/count/codec
	appliedMS                                        int64
	resultVersion, oldResultVersion, oldControlBytes int64 // W codec/native snapshots, never defaults
}

// Detached native observations, not Applied. A slot result is not durable
// until storage, complete OLD-to-NEW accounting and W's terminal all succeed.
type wc008ReclaimEffect struct {
	first, following wcReclaimProbe
	child            wcReclaimChild
	hasChild         bool
	prepared         wcReclaimPrepared
}

func wc008ReclaimInput(q wc008ReclaimCommand, e wc008ReclaimEffect) wcReclaimInput {
	in := wcReclaimInput{owner: q.owner, command: q.identity, expectedOldSlot: q.expectedOldSlot,
		oldSlot: q.oldSlot, firstProbe: e.first, probe: e.following}
	if e.hasChild {
		child := e.child
		in.child = &child
	}
	return in
}

// The same finite statement observes the first native child from the OLD
// cursor and then the first child after the candidate. It does not filter out
// unknown kinds: encountering one must refuse, not skip it behind the cursor.
func readWC008ReclaimFirst(ctx context.Context, turn *commandTurn, o wcReclaimOwner, after int64) (child wcReclaimChild, found bool, err error) {
	row, err := wc008DraftQuery(ctx, turn, wc008ReclaimFirst, o.playbackID, o.incarnation[:], after, o.acknowledgedSequence)
	if err != nil {
		return child, false, err
	}
	child.key.playbackID, child.key.incarnation = o.playbackID, o.incarnation
	var kind string
	if err = row.Scan(&child.key.sequence, &kind, &child.variableBytes); errors.Is(err, sql.ErrNoRows) {
		return wcReclaimChild{}, false, turn.checkLifetimes(ctx)
	} else if err != nil {
		return wcReclaimChild{}, false, err
	}
	if kind != "capture_report" && kind != "drain_piece" || child.variableBytes <= 0 ||
		child.key.sequence <= after || child.key.sequence > o.acknowledgedSequence {
		return wcReclaimChild{}, false, ErrResolveRequired
	}
	return child, true, turn.checkLifetimes(ctx)
}

func applyWC008ReclaimChild(ctx context.Context, turn *commandTurn, q wc008ReclaimCommand) (out wc008ReclaimEffect, err error) {
	if turn == nil {
		return out, ErrInvalid
	}
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if q.appliedMS < 0 || q.resultVersion <= 0 || q.oldControlBytes <= 0 {
		return out, ErrConflict
	}
	out.child, out.hasChild, err = readWC008ReclaimFirst(ctx, turn, q.owner, q.owner.afterSequence)
	if err != nil {
		return wc008ReclaimEffect{}, err
	}
	out.first = wcReclaimProbe{q.owner.playbackID, q.owner.incarnation, q.owner.afterSequence,
		q.owner.acknowledgedSequence, 0}
	out.following = out.first
	if out.hasChild {
		out.first.nextSequence = out.child.key.sequence
		out.following.afterSequence = out.child.key.sequence
		next, found, e := readWC008ReclaimFirst(ctx, turn, q.owner, out.child.key.sequence)
		if e != nil {
			return wc008ReclaimEffect{}, e
		}
		if found {
			out.following.nextSequence = next.key.sequence
		}
	}
	out.prepared, err = prepareWCReclaim(wc008ReclaimInput(q, out))
	if err != nil {
		return wc008ReclaimEffect{}, err
	}
	if out.prepared.remove {
		debit, e := wc008DraftInteger(ctx, turn, wc008ReclaimDelete, out.prepared.key.playbackID,
			out.prepared.key.incarnation[:], out.prepared.key.sequence, out.prepared.removedVariableBytes)
		if e != nil {
			return wc008ReclaimEffect{}, e
		}
		if debit != out.prepared.removedVariableBytes {
			return wc008ReclaimEffect{}, ErrResolveRequired
		}
	}
	return out, turn.checkLifetimes(ctx)
}

func validateWC008ReclaimEffect(q wc008ReclaimCommand, e wc008ReclaimEffect) error {
	if q.appliedMS < 0 || q.resultVersion <= 0 || q.oldControlBytes <= 0 || q.oldSlot.present != (q.oldResultVersion > 0) || !e.hasChild && e.child != (wcReclaimChild{}) {
		return ErrResolveRequired
	}
	expected, err := prepareWCReclaim(wc008ReclaimInput(q, e))
	if err != nil {
		return err
	}
	if e.prepared != expected {
		return ErrResolveRequired
	}
	return nil
}

// Save the one cleanup scalar on its ACTUAL claim, with full OLD slot equality
// and captured ACK/cursor/debt/due. No child result, owner deletion or ACK refund
// is introduced. This returns the actual NEW header control bytes; W separately
// charges OLD-to-NEW control and every physical effect before its SAME commit.
func storeWC008ReclaimResult(ctx context.Context, turn *commandTurn, q wc008ReclaimCommand, e wc008ReclaimEffect,
	request RequestDigest, result ResultDigest) (slotBytes int64, err error) {
	if turn == nil {
		return 0, ErrInvalid
	}
	defer func() { slotBytes, err = finishCommand(ctx, commandLifetime(turn), slotBytes, err) }()
	if err = validateWC008ReclaimEffect(q, e); err != nil {
		return 0, err
	}
	if request == (RequestDigest{}) || result == (ResultDigest{}) {
		return 0, ErrResolveRequired
	}
	p := e.prepared
	var due, removedSequence any
	var removedCount, noNext int64
	if p.remove {
		removedSequence, removedCount = p.key.sequence, 1
	}
	if p.noNext {
		noNext = 1
	} else {
		due = q.owner.dueMS
	}
	args := []any{q.identity.Sequence, p.remainingRows, p.remainingVariableBytes, p.afterSequence, due,
		q.identity.Sequence, q.identity.OperationID[:], request[:], q.owner.acknowledgedSequence, q.owner.afterSequence,
		removedSequence, removedCount, p.removedVariableBytes, p.afterSequence, noNext, q.appliedMS, q.resultVersion, result[:],
		q.owner.playbackID, q.owner.incarnation[:], q.owner.lastSequence, q.owner.acknowledgedSequence,
		q.owner.rows, q.owner.variableBytes, q.owner.afterSequence, q.owner.dueMS}

	old := q.oldSlot
	statement := wc008ReclaimStoreEmpty
	if old.present {
		statement = wc008ReclaimStoreZero
		if old.removedCount == 1 {
			statement = wc008ReclaimStoreRemoved
		}
		args = append(args, old.reference.Identity.Sequence, old.reference.Identity.OperationID[:], old.reference.RequestDigest[:], old.floor, old.oldCursor)
		if old.removedCount == 1 {
			args = append(args, old.removedSequence)
		}
		var oldNoNext int64
		if old.noNext {
			oldNoNext = 1
		}
		args = append(args, old.removedCount, old.removedBytes, old.nextCursor, oldNoNext, old.appliedMS, int64(1), old.resultDigest[:], "progress_receipt_reclaim", q.oldResultVersion)
	}
	args = append(args, q.oldControlBytes)
	slotBytes, err = wc008DraftInteger(ctx, turn, statement, args...)
	if err != nil {
		return 0, err
	}
	if slotBytes <= 0 {
		return 0, ErrResolveRequired
	}
	return slotBytes, turn.checkLifetimes(ctx)
}
