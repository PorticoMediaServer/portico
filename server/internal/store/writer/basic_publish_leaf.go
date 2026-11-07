package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
)

type basicLeafPublication struct {
	activation basicActivationEffects
	entityID   int64
	duration   float64
	settled    basicLeafTechnicalAfter
}
type basicPublishedLeaf struct{ entityID, assetID, infoEpoch, changeRevision int64 }

// This final effect is internal to the complete BasicAccept transaction. The
// parent must install/verify its complete NFO/art/subtitle observation, object
// analysis fields, run CAS and immutable receipt in that SAME transaction.
// No public result, Outcome, COMMIT or independent readiness entry exists here.
func publishBasicFirstLeaf(ctx context.Context, turn *commandTurn, in basicLeafPublication) (value basicPublishedLeaf, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	a, n := in.activation, in.settled
	s := a.source
	if ctx == nil || turn == nil || turn.transaction == nil || in.entityID <= 0 || a.familyMask != 511 || a.shapeID <= 0 ||
		a.changeRevision <= 0 || a.changeRevision == math.MaxInt64 || s.expectedEpoch <= 0 || s.expectedEpoch == math.MaxInt64 ||
		a.fileSizeRevision <= 0 || a.invalidationRevision <= 0 || a.ownerObject.Valid || a.ownerRevision.Valid || a.ownerObject.String != "" || a.ownerRevision.String != "" ||
		!n.duration.Valid || !n.resolution.Valid || n.duration.Float64 <= 0 || n.resolution.Int64 < 0 || n.durationKey != n.duration.Float64 || n.resolutionKey != n.resolution.Int64 ||
		math.IsNaN(n.duration.Float64) || math.IsInf(n.duration.Float64, 0) || in.duration <= 0 || math.IsNaN(in.duration) || math.IsInf(in.duration, 0) ||
		n.scalarRevision <= 0 || n.aggregateRevision <= 0 || n.membershipSequence <= 0 {
		return value, ErrInvalid
	}
	row, err := basicStageRead(ctx, turn, basicPublishLeafProof, in.entityID, s.changeID, s.assetID, s.assetToken, s.libraryID, s.expectedEpoch, s.inputRevision, s.witnessFingerprint[:], a.changeRevision, a.shapeID,
		s.expectedEpoch+1, s.size, s.modifiedNS, a.fileSizeRevision, a.invalidationRevision, in.duration, n.duration.Float64, n.resolution.Int64, n.membershipSequence, n.scalarRevision, n.aggregateRevision)
	if err != nil {
		return value, err
	}
	var change, epoch int64
	if err = row.Scan(&change, &epoch); err != nil {
		return value, basicStageError(err)
	}
	if change != s.changeID || epoch != s.expectedEpoch+1 {
		return value, ErrConflict
	}
	// FIRST never discharges old scalar/path obligations by clearing a flag.
	// old_path is guarded NULL in the proof; scalar-old is a bounded PK prefix.
	row, err = basicStageRead(ctx, turn, basicPublishScalarOld, s.changeID)
	if err != nil {
		return value, err
	}
	var field int64
	err = row.Scan(&field)
	if err == nil {
		return value, &Refusal{Code: RefusalUnqualified}
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return value, err
	}
	row, err = basicStageRead(ctx, turn, basicSettleShapeRead, a.shapeID)
	if err != nil {
		return value, err
	}
	var refs int64
	if err = row.Scan(&refs); err != nil {
		return value, basicStageError(err)
	}
	if refs < 2 {
		return value, ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicPublishLinkCAS, nil, in.entityID, s.assetID, s.libraryID, a.shapeID, in.duration, s.changeID)
	if err != nil {
		return value, err
	}
	var library, shape int64
	var duration float64
	var pending sql.NullInt64
	if err = row.Scan(&value.entityID, &value.assetID, &library, &shape, &duration, &pending); err != nil {
		return value, basicStageError(err)
	}
	if value.entityID != in.entityID || value.assetID != s.assetID || library != s.libraryID || shape != a.shapeID || duration != in.duration || pending.Valid {
		return value, ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicPublishAssetCAS, int64(0), s.assetID, s.assetToken, s.libraryID, epoch, s.size, s.modifiedNS, a.fileSizeRevision, a.invalidationRevision, int64(1), int64(1))
	if err != nil {
		return value, err
	}
	var asset, flag, sizeRevision, invalidation int64
	if err = row.Scan(&asset, &library, &value.infoEpoch, &flag, &sizeRevision, &invalidation); err != nil {
		return value, basicStageError(err)
	}
	if asset != s.assetID || library != s.libraryID || value.infoEpoch != epoch || flag != 0 || sizeRevision != a.fileSizeRevision || invalidation != a.invalidationRevision {
		return value, ErrConflict
	}
	args := []any{int64(5), a.changeRevision + 1, s.changeID, s.assetID, s.assetToken, int64(0), int64(4), s.expectedEpoch, s.inputRevision, int64(511), s.witnessFingerprint[:], a.changeRevision, a.shapeID, int64(1), int64(0), int64(0), int64(0), int64(0), "", "", "", int64(0), int64(0), int64(0), ""}
	row, err = basicStageRead(ctx, turn, basicActivationChangeCAS, args...)
	if err != nil {
		return value, err
	}
	var phase int64
	if err = row.Scan(&value.changeRevision, &phase); err != nil {
		return value, basicStageError(err)
	}
	if value.changeRevision != a.changeRevision+1 || phase != 5 {
		return value, ErrConflict
	}
	return value, nil
}
