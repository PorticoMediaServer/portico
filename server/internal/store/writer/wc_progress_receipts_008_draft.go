package writer

import (
	"context"
	"math"
)

// These private native storage components consume digests from W's common closed
// codecs; they do not implement those codecs or create an authority/callback.
// Every domain effect, immutable result, exact debt/control delta and required
// bounded event must remain in W's SAME transaction and physical terminal path.
// Native fields/NULL groups follow adopted CHANGE-008 schema debd5c77.
const wc008ProgressReceipt = "wc.progress.receipt"
const wc008ProgressOwnerAdvance = "wc.progress.owner_advance"

func validateWC008ProgressEffect(q wc008ProgressCommand, effect wc008ProgressEffect) error {
	piece, err := q.piece()
	if err != nil {
		return err
	}
	finished := piece.untilMS == q.untilMS
	if effect.consumedFrom != piece.fromMS || effect.consumedUntil != piece.untilMS ||
		effect.creditedMS != piece.untilMS-piece.fromMS || effect.nextCursor != piece.untilMS ||
		effect.finished != finished || effect.observationRevision <= 0 || effect.historyRevision <= 0 ||
		effect.viewerRevision < 0 || effect.personalRevision <= 0 || effect.controlVariableBytes <= 0 || effect.adminVariableBytes <= 0 ||
		effect.remainingUntil.Valid == finished || !effect.remainingUntil.Valid && effect.remainingUntil.Int64 != 0 ||
		effect.remainingUntil.Valid && effect.remainingUntil.Int64 != q.untilMS {
		return ErrResolveRequired
	}
	return nil
}

// The returned debit is measured by the actual INSERT RETURNING expression
// over EVERY persisted TEXT/BLOB field in this adopted native layout. It is
// not an input estimate or a quota. Owner rows/bytes advance in the SAME turn.
func storeWC008ProgressResult(ctx context.Context, turn *commandTurn, q wc008ProgressCommand, effect wc008ProgressEffect,
	request RequestDigest, result ResultDigest) (variableBytes int64, err error) {
	if turn == nil {
		return 0, ErrInvalid
	}
	defer func() { variableBytes, err = finishCommand(ctx, commandLifetime(turn), variableBytes, err) }()
	if err = validateWC008ProgressEffect(q, effect); err != nil {
		return 0, err
	}
	if request == (RequestDigest{}) || result == (ResultDigest{}) || q.acceptedOrdinal < 0 ||
		q.receiptRows < 0 || q.receiptRows == math.MaxInt64 || q.receiptVariableBytes < 0 ||
		(q.receiptRows == 0) != (q.receiptVariableBytes == 0) {
		return 0, ErrResolveRequired
	}
	var remaining, pendingFrom, pendingUntil, pendingCursor any
	if effect.remainingUntil.Valid {
		remaining = effect.remainingUntil.Int64
		pendingFrom, pendingUntil, pendingCursor = q.fromMS, q.untilMS, effect.nextCursor
	}
	finished := int64(0)
	if effect.finished {
		finished = 1
	}
	variableBytes, err = wc008DraftInteger(ctx, turn, wc008ProgressReceipt,
		q.proof.playbackID, q.identity.Incarnation[:], q.identity.Sequence, q.identity.OperationID[:], request[:], "drain_piece",
		q.resultVersion, q.appliedMS, q.acceptedOrdinal, q.lastAcceptedSequence, q.proof.reportSequence,
		q.proof.occurrenceGeneration, q.proof.activityGeneration, q.proof.rememberGeneration, q.proof.clockIncarnation[:], q.startEventID,
		q.fromMS, q.untilMS, q.cursorMS, q.startEventID, q.lastAcceptedSequence, q.proof.reportSequence,
		pendingFrom, pendingUntil, pendingCursor, effect.viewerRevision, effect.observationRevision, effect.historyRevision,
		effect.personalRevision, effect.consumedFrom, effect.consumedUntil, effect.nextCursor, remaining, finished, effect.creditedMS, int64(1), result[:])
	if err != nil {
		return 0, err
	}
	if variableBytes <= 0 || variableBytes > math.MaxInt64-q.receiptVariableBytes {
		return 0, ErrResolveRequired
	}
	err = wc008DraftChanged(ctx, turn, wc008ProgressOwnerAdvance, q.identity.Sequence, q.receiptRows+1,
		q.receiptVariableBytes+variableBytes, q.proof.playbackID, q.identity.Incarnation[:], q.last, q.acknowledged,
		q.receiptRows, q.receiptVariableBytes)
	if err != nil {
		return 0, err
	}
	return variableBytes, turn.checkLifetimes(ctx)
}
