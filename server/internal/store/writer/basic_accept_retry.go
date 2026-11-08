package writer

import (
	"context"
	"database/sql"
)

// Completed-copy retry bookkeeping belongs to the same transaction as Accept.
// Ordinary copy turns may follow a resume and leave attempts nonzero at phase
// 3; publication must not strand that phase-5 header behind retirement's idle
// guard. This does not recover a job, clear a fault/lease, or change its cursor.
func idleBasicAcceptedRetry(ctx context.Context, turn *commandTurn, s basicStreamFactStage, published basicPublishedLeaf, shape int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != BasicAccept || published.assetID != s.assetID || published.infoEpoch != s.expectedEpoch+1 || published.entityID <= 0 || published.changeRevision <= 0 || shape <= 0 {
		return ErrInvalid
	}
	row, err := basicStageRead(ctx, turn, basicAcceptRetryIdle, nil, s.changeID, s.assetID, s.assetToken, s.expectedEpoch, s.inputRevision, s.witnessFingerprint[:], published.changeRevision, shape, published.entityID, published.entityID)
	if err != nil {
		return err
	}
	var id, revision, attempts int64
	var next sql.NullInt64
	if err = row.Scan(&id, &revision, &attempts, &next); err != nil {
		return basicStageError(err)
	}
	if id != s.changeID || revision != published.changeRevision || attempts != 0 || next.Valid {
		return ErrConflict
	}
	return nil
}
