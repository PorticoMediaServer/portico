package writer

import (
	"context"
	"database/sql"
	"math"
)

type basicPrefenceEffects struct {
	input basicOrdinaryInputAccept
}
type basicCopyPosition struct {
	changeID, changeRevision, phase, afterEntity int64
	family, copyPhase, id, aux                   int64
	key, key2, key3                              string
}

// prefenceBasicOrdinary fences the sole actual ordinary holder BEFORE any
// canonical copy. Multi-owner/CUE/episode cases retain their own closure gate.
// Its native post-cursor is real catalog_info_changes state, not rows_staged.
func prefenceBasicOrdinary(ctx context.Context, turn *commandTurn, in basicPrefenceEffects) (value basicCopyPosition, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	c, s := in.input, in.input.stream.source
	if ctx == nil || turn == nil || turn.transaction == nil || c.stream.changeRevision <= 0 || c.stream.changeRevision == math.MaxInt64 {
		return value, ErrInvalid
	}
	if err = validateBasicOrdinaryCounts(s.libraryID, c.stream.rawShape, c.counts); err != nil {
		return value, err
	}
	if c.snapshot.source != s || c.snapshot.changeRevision != c.stream.changeRevision || c.snapshot.before != c.snapshot.after {
		return value, ErrInvalid
	}
	if err = validateBasicProbeEffects(c.snapshot); err != nil {
		return value, err
	}
	if err = checkBasicInputAtPhase(ctx, turn, s, 1); err != nil {
		return value, err
	}
	row, err := basicStageRead(ctx, turn, basicProbeAsset, basicProbeBeforeArgs(c.snapshot)...)
	if err != nil {
		return value, err
	}
	var actual int64
	if err = row.Scan(&actual); err != nil {
		return value, basicStageError(err)
	}
	if actual != s.assetID {
		return value, ErrConflict
	}
	_, owner, err := readBasicStreamCaps(ctx, turn, c.stream)
	if err != nil {
		return value, err
	}
	row, err = basicStageRead(ctx, turn, basicPrefenceHeader, s.changeID)
	if err != nil {
		return value, err
	}
	var revision, mask, upper, hasAfter, after, copyFamily, copyPhase, copyID, aux, leaseOwner, leaseGeneration, leaseExpires int64
	var target, available sql.NullInt64
	var object, objectRevision sql.NullString
	var key, key2, key3 string
	if err = row.Scan(&revision, &mask, &upper, &hasAfter, &after, &target, &available, &object, &objectRevision, &copyFamily, &copyPhase, &copyID, &aux, &key, &key2, &key3, &leaseOwner, &leaseGeneration, &leaseExpires); err != nil {
		return value, basicStageError(err)
	}
	if revision != c.stream.changeRevision || mask != 511 || upper != owner || hasAfter != 0 || after != 0 || !target.Valid || target.Int64 != c.stream.shapeID || !available.Valid || available.Int64 != 1 || object.Valid || objectRevision.Valid || copyFamily != 1 || copyPhase != 0 || copyID != 0 || aux != 0 || key != "" || key2 != "" || key3 != "" || leaseOwner != 0 || leaseGeneration != 0 || leaseExpires != 0 {
		return value, ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicPrefenceAssetCAS, int64(1), s.assetID, s.assetToken, s.libraryID, s.expectedEpoch, s.size, s.modifiedNS, c.snapshot.fileSizeRevision, c.snapshot.invalidationRevision, int64(0))
	if err != nil {
		return value, err
	}
	var pending int64
	if err = row.Scan(&actual, &pending); err != nil {
		return value, basicStageError(err)
	}
	if actual != s.assetID || pending != 1 {
		return value, ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicPrefenceLinkCAS, s.changeID, owner, s.assetID, s.libraryID)
	if err != nil {
		return value, err
	}
	var entity, asset int64
	if err = row.Scan(&entity, &asset, &pending); err != nil {
		return value, basicStageError(err)
	}
	if entity != owner || asset != s.assetID || pending != s.changeID {
		return value, ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicPrefenceChangeCAS, int64(2), int64(1), owner, int64(0), c.stream.changeRevision+1, s.changeID, s.assetID, s.assetToken, int64(0), int64(1), s.expectedEpoch, s.inputRevision, int64(511), s.witnessFingerprint[:], c.stream.changeRevision, owner, int64(0), int64(0), c.stream.shapeID, int64(1), int64(1), int64(0), int64(0), int64(0), "", "", "", int64(0), int64(0), int64(0), "")
	if err != nil {
		return value, err
	}
	if err = row.Scan(&value.changeID, &value.changeRevision, &value.phase, &value.afterEntity, &value.family, &value.copyPhase, &value.id, &value.aux, &value.key, &value.key2, &value.key3); err != nil {
		return value, basicStageError(err)
	}
	if value != (basicCopyPosition{changeID: s.changeID, changeRevision: c.stream.changeRevision + 1, phase: 2, afterEntity: owner}) {
		return value, ErrConflict
	}
	return value, nil
}
