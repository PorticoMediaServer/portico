package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
)

type basicShapeTarget struct{ shapeID, changeRevision int64 }

// pinBasicProbeShape is a separate bounded stage, after coherent raw scalar
// installation. The original/current snapshot must contain that actual NEW
// tuple (before==after here). It pins exactly one dictionary reference in the
// input header, and resolves FIRST's genuine NULL owner without inventing a
// probe-object owner. It does not freeze/seal or advance an accepted epoch.
func pinBasicProbeShape(ctx context.Context, turn *commandTurn, in basicProbeEffects) (value basicShapeTarget, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if ctx == nil || turn == nil || in.before != in.after {
		return value, ErrInvalid
	}
	if err = validateBasicProbeEffects(in); err != nil {
		return value, err
	}
	if in.changeRevision == math.MaxInt64 {
		return value, ErrCap
	}
	s := in.source
	if err = checkBasicStreamInput(ctx, turn, s); err != nil {
		return value, err
	}
	row, err := basicStageRead(ctx, turn, basicProbeHeader, s.changeID, s.assetID, s.assetToken, int64(0), int64(511), s.inputRevision, in.changeRevision, int64(0), int64(0), int64(0), int64(0), int64(0))
	if err != nil {
		return value, err
	}
	var actual int64
	if err = row.Scan(&actual); err != nil {
		return value, basicStageError(err)
	}
	if actual != s.changeID {
		return value, ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicProbeAsset, basicProbeBeforeArgs(in)...)
	if err != nil {
		return value, err
	}
	if err = row.Scan(&actual); err != nil {
		return value, basicStageError(err)
	}
	if actual != s.assetID {
		return value, ErrConflict
	}
	p := in.after
	row, err = basicStageRead(ctx, turn, basicShapeLookup, p.container, p.videoCodec, p.height)
	if err != nil {
		return value, err
	}
	var refs int64
	err = row.Scan(&value.shapeID, &refs)
	if errors.Is(err, sql.ErrNoRows) {
		charge := int64(168 + 2*len(p.container) + 2*len(p.videoCodec))
		row, err = basicStageRead(ctx, turn, basicStageBudget, int64(1))
		if err != nil {
			return value, err
		}
		var used, cap int64
		if err = row.Scan(&used, &cap); err != nil {
			return value, basicStageError(err)
		}
		if cap != 33554432 || used < 0 || used > cap || charge > cap-used {
			return value, ErrCap
		}
		row, err = basicStageRead(ctx, turn, basicStageBudgetCAS, used+charge, int64(1), used, cap)
		if err != nil {
			return value, err
		}
		if err = row.Scan(&actual); err != nil {
			return value, basicStageError(err)
		}
		if actual != used+charge {
			return value, ErrConflict
		}
		row, err = basicStageRead(ctx, turn, basicShapeInsert, p.container, p.videoCodec, p.height, int64(1))
		if err != nil {
			return value, err
		}
		var container, codec string
		var height int64
		if err = row.Scan(&value.shapeID, &container, &codec, &height, &refs); err != nil {
			return value, err
		}
		if value.shapeID <= 0 || container != p.container || codec != p.videoCodec || height != p.height || refs != 1 {
			return value, ErrConflict
		}
	} else {
		if err != nil {
			return value, err
		}
		if value.shapeID <= 0 || refs < 0 {
			return value, ErrConflict
		}
		if refs == math.MaxInt64 {
			return value, ErrCap
		}
		row, err = basicStageRead(ctx, turn, basicShapeRefCAS, refs+1, value.shapeID, refs, p.container, p.videoCodec, p.height)
		if err != nil {
			return value, err
		}
		var actualRefs int64
		if err = row.Scan(&actual, &actualRefs); err != nil {
			return value, basicStageError(err)
		}
		if actual != value.shapeID || actualRefs != refs+1 {
			return value, ErrConflict
		}
	}
	row, err = basicStageRead(ctx, turn, basicShapeHeaderCAS, value.shapeID, int64(1), in.changeRevision+1, s.changeID, s.assetID, s.assetToken, int64(0), int64(0), s.expectedEpoch, s.inputRevision, int64(511), s.witnessFingerprint[:], in.changeRevision, int64(0), int64(0), "", int64(0), int64(0), int64(0))
	if err != nil {
		return value, err
	}
	var shape, copyFamily int64
	if err = row.Scan(&shape, &copyFamily, &value.changeRevision); err != nil {
		return value, basicStageError(err)
	}
	if shape != value.shapeID || copyFamily != 1 || value.changeRevision != in.changeRevision+1 {
		return value, ErrConflict
	}
	return value, nil
}
