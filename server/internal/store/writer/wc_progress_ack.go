package writer

import (
	"context"
	"database/sql"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	wcProgressAckEmptyDue stmt.ID = "wc.progress.ack_empty_due"
	wcProgressAckDue      stmt.ID = "wc.progress.ack_due"
)

// Native facts from W's SAME-turn current disclosure and original classifier.
// W must validate the complete original typed result before receiptReplay. These
// facts do not grant authority, mint a pending capability, or decode a receipt.
// This is the source ProgressAccept ACK; cleanup ACK is a separate closed purpose.
type wcProgressAck struct {
	playbackID                                       string
	reference                                        ReceiptReference
	owner                                            receiptOwnerFacts
	decision                                         receiptDecision
	sourceKind                                       string
	receiptRows, receiptVariableBytes, afterSequence int64
	due                                              sql.NullInt64
	appliedMS                                        int64 // W's trusted ACK clock, never time.Now in the component
}

// Floor-only, refund0. No receipt, result, debt, source or fixed-slot clearing.
// Newly eligible SOURCE-child debt is discoverably due in the same CAS. W owns
// NORMAL/Foreground admission, current scope, idempotent classification and finish.
func acknowledgeWCProgress(ctx context.Context, turn *commandTurn, q wcProgressAck) (floor int64, err error) {
	if turn == nil {
		return 0, ErrInvalid
	}
	defer func() { floor, err = finishCommand(ctx, commandLifetime(turn), floor, err) }()
	if err = turn.checkLifetimes(ctx); err != nil {
		return 0, err
	}
	o, i := q.owner, q.reference.Identity
	if q.playbackID == "" || !utf8.ValidString(q.playbackID) || i.Incarnation == [16]byte{} || i.OperationID == [16]byte{} || i.Sequence <= 0 || q.reference.RequestDigest == (RequestDigest{}) || !o.exists || o.incarnation != i.Incarnation || o.lastSequence < 0 || o.acknowledgedSequence < 0 || o.acknowledgedSequence > o.lastSequence || o.lastSequence-o.acknowledgedSequence > 1 {
		return 0, ErrResolveRequired
	}
	// Historical floor proof precedes source result/debt/due freshness. Do not
	// decode a reclaimed child or redue an ACK retry after the original commit.
	if q.decision == receiptRetired && i.Sequence <= o.acknowledgedSequence {
		return o.acknowledgedSequence, nil
	}
	if q.decision != receiptReplay || i.Sequence != o.lastSequence || i.Sequence <= o.acknowledgedSequence || (q.sourceKind != "capture_report" && q.sourceKind != "drain_piece") || q.appliedMS < 0 || q.receiptRows <= 0 || q.receiptVariableBytes <= 0 || q.afterSequence < 0 || q.afterSequence > o.acknowledgedSequence || q.due.Valid && q.due.Int64 < 0 || !q.due.Valid && q.due.Int64 != 0 {
		return 0, ErrResolveRequired
	}
	nextDue := q.appliedMS
	if q.due.Valid && q.due.Int64 < nextDue {
		nextDue = q.due.Int64
	}
	args := []any{i.Sequence, nextDue, q.playbackID, i.Incarnation[:], o.lastSequence, o.acknowledgedSequence, q.receiptRows, q.receiptVariableBytes, q.afterSequence}
	statement := wcProgressAckEmptyDue
	if q.due.Valid {
		statement = wcProgressAckDue
		args = append(args, q.due.Int64)
	}
	floor, err = wc008DraftInteger(ctx, turn, statement, args...)
	if err != nil {
		return 0, err
	}
	if floor != i.Sequence {
		return 0, ErrResolveRequired
	}
	return floor, turn.checkLifetimes(ctx)
}

func wcProgressAckDefinitions() []stmt.Definition {
	return []stmt.Definition{
		{ID: wcProgressAckEmptyDue, Owner: "store/writer WC Progress ACK", CallSites: []string{"store/writer/wc_progress_ack.go"}, Kind: stmt.Write,
			SQL:     `UPDATE playback_personal_claims SET command_acknowledged_sequence=?,receipt_reclaim_due_ms=? WHERE playback_id=? AND command_incarnation=? AND command_last_sequence=? AND command_acknowledged_sequence=? AND receipt_rows=? AND receipt_variable_bytes=? AND receipt_reclaim_after_sequence=? AND receipt_reclaim_due_ms IS NULL RETURNING command_acknowledged_sequence`,
			Params:  []stmt.Param{{Name: "next_floor", Type: stmt.Integer}, {Name: "next_due", Type: stmt.Integer}, {Name: "playback", Type: stmt.Text}, {Name: "incarnation", Type: stmt.Blob}, {Name: "last", Type: stmt.Integer}, {Name: "old_floor", Type: stmt.Integer}, {Name: "rows", Type: stmt.Integer}, {Name: "bytes", Type: stmt.Integer}, {Name: "cursor", Type: stmt.Integer}},
			Cohorts: []stmt.Cohort{{Name: "source_ack_first_due", Bindings: []any{int64(1), int64(2000), "playback-音", []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, int64(1), int64(0), int64(1), int64(135), int64(0)}}},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_playback_personal_claims_2"}}},
		{ID: wcProgressAckDue, Owner: "store/writer WC Progress ACK", CallSites: []string{"store/writer/wc_progress_ack.go"}, Kind: stmt.Write,
			SQL:     `UPDATE playback_personal_claims SET command_acknowledged_sequence=?,receipt_reclaim_due_ms=? WHERE playback_id=? AND command_incarnation=? AND command_last_sequence=? AND command_acknowledged_sequence=? AND receipt_rows=? AND receipt_variable_bytes=? AND receipt_reclaim_after_sequence=? AND receipt_reclaim_due_ms=? RETURNING command_acknowledged_sequence`,
			Params:  []stmt.Param{{Name: "next_floor", Type: stmt.Integer}, {Name: "next_due", Type: stmt.Integer}, {Name: "playback", Type: stmt.Text}, {Name: "incarnation", Type: stmt.Blob}, {Name: "last", Type: stmt.Integer}, {Name: "old_floor", Type: stmt.Integer}, {Name: "rows", Type: stmt.Integer}, {Name: "bytes", Type: stmt.Integer}, {Name: "cursor", Type: stmt.Integer}, {Name: "old_due", Type: stmt.Integer}},
			Cohorts: []stmt.Cohort{{Name: "source_ack_existing_due", Bindings: []any{int64(1), int64(17), "playback-音", []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, int64(1), int64(0), int64(1), int64(135), int64(0), int64(17)}}},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_playback_personal_claims_2"}}},
	}
}
