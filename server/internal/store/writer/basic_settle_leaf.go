package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// A component of the parent's final BasicAccept transaction, after activation.
// This grants no publication authority: both pending obligations remain owned
// by the parent, as do the receipt, run and phase-four completion.
type basicLeafSettlement struct {
	activation basicActivationEffects
	entityID   int64
	duration   float64
	height     int64
}

func settleBasicFirstLeaf(ctx context.Context, turn *commandTurn, in basicLeafSettlement) (value basicLeafTechnicalAfter, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	a := in.activation
	s := a.source
	if ctx == nil || turn == nil || in.entityID <= 0 || s.expectedEpoch <= 0 || s.expectedEpoch == math.MaxInt64 || a.changeRevision <= 0 || a.familyMask != 511 || a.shapeID <= 0 || a.ownerObject.Valid || a.ownerRevision.Valid || a.ownerObject.String != "" || a.ownerRevision.String != "" || math.IsNaN(in.duration) || math.IsInf(in.duration, 0) || in.duration <= 0 || in.height < 0 {
		return value, ErrInvalid
	}
	// The phase-four header is checked directly: the pre-activation input helper
	// deliberately cannot authorize a post-activation epoch.
	row, e := basicStageRead(ctx, turn, basicSettleProof, in.entityID, s.changeID, s.assetID, s.assetToken, s.libraryID, s.expectedEpoch, s.inputRevision, s.witnessFingerprint[:], a.changeRevision, a.shapeID, s.assetToken, s.expectedEpoch+1, s.size, s.modifiedNS, a.fileSizeRevision, a.invalidationRevision, in.duration, in.height, in.entityID)
	if e != nil {
		return value, e
	}
	var entity int64
	if e = row.Scan(&entity); e != nil {
		return value, basicStageError(e)
	}
	if entity != in.entityID {
		return value, ErrConflict
	}
	ownerRows, e := basicRows(ctx, turn, basicAcceptOwners, s.assetID, int64(2))
	if e != nil {
		return value, e
	}
	if e = basicMediaExactRow(ownerRows, []any{in.entityID, s.libraryID, nil}); e != nil {
		return value, e
	}
	assets, e := basicRows(ctx, turn, basicBeginFirstEntityAssets, in.entityID)
	if e != nil {
		return value, e
	}
	if e = basicMediaExactRow(assets, []any{s.assetID}); e != nil {
		return value, e
	}
	for _, point := range []struct {
		id   stmt.ID
		args []any
	}{
		{basicAcceptSuccessor, []any{s.assetID, s.assetToken, int64(1)}},
		{basicSettleCollection, []any{in.entityID}},
	} {
		row, e = basicStageRead(ctx, turn, point.id, point.args...)
		if e != nil {
			return value, e
		}
		var unexpected int64
		e = row.Scan(&unexpected)
		if e == nil {
			return value, &Refusal{Code: RefusalUnqualified}
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return value, e
		}
	}
	forward, e := basicRows(ctx, turn, basicSettleForward, in.entityID)
	if e != nil {
		return value, e
	}
	if e = basicMediaExactRow(forward, []any{in.entityID, int64(0)}); e != nil {
		return value, e
	}
	step, ok := turn.steps[basicSettleReverse]
	if !ok {
		return value, ErrInvalid
	}
	rows, e := turn.query(ctx, step.statementStep, in.entityID)
	if e != nil {
		return value, e
	}
	var library, parent, contributor, source, count int64
	// LIMIT 2 is native and indexed; one self source0 edge is the only allowed
	// reverse obligation. The second row refuses rather than truncates fanout.
	defer rows.Close()
	for rows.Next() {
		if e = rows.Scan(&library, &parent, &contributor, &source); e != nil {
			return value, e
		}
		count++
		if library != s.libraryID || parent != in.entityID || contributor != in.entityID || source != 0 {
			return value, &Refusal{Code: RefusalUnqualified}
		}
	}
	if e = rows.Err(); e != nil {
		return value, e
	}
	if count != 1 {
		return value, &Refusal{Code: RefusalUnqualified}
	}
	if e = rows.Close(); e != nil {
		return value, e
	}
	row, e = basicStageRead(ctx, turn, basicSettleBefore, in.entityID, s.libraryID)
	if e != nil {
		return value, e
	}
	var before basicLeafTechnicalBefore
	var scalar int64
	if e = row.Scan(&before.duration, &before.resolution, &before.durationKey, &before.resolutionKey, &before.scalarRevision, &before.aggregateRevision, &before.membershipSequence, &scalar); e != nil {
		return value, basicStageError(e)
	}
	if scalar != before.scalarRevision {
		return value, ErrConflict
	}
	next, e := projectBasicFirstLeafTechnical(before, basicFirstLinkTechnical{}, in.duration, in.height)
	if e != nil {
		return value, e
	}
	row, e = basicStageRead(ctx, turn, basicSettleShapeRead, a.shapeID)
	if e != nil {
		return value, e
	}
	var shapeRefs int64
	if e = row.Scan(&shapeRefs); e != nil {
		return value, basicStageError(e)
	}
	if shapeRefs <= 0 {
		return value, ErrConflict
	}
	if shapeRefs == math.MaxInt64 {
		return value, ErrCap
	}
	row, e = basicStageRead(ctx, turn, basicSettleLinkCAS, a.shapeID, in.duration, in.entityID, s.assetID, s.libraryID, s.changeID)
	if e != nil {
		return value, e
	}
	var le, la, ll, ls, lp int64
	var ld float64
	if e = row.Scan(&le, &la, &ll, &ls, &ld, &lp); e != nil {
		return value, basicStageError(e)
	}
	if le != in.entityID || la != s.assetID || ll != s.libraryID || ls != a.shapeID || ld != in.duration || lp != s.changeID {
		return value, ErrConflict
	}
	scalarID := basicSettleScalarCAS
	switch {
	case !before.duration.Valid && !before.resolution.Valid:
		scalarID = basicSettleScalarBothNull
	case !before.duration.Valid:
		scalarID = basicSettleScalarDurationNull
	case !before.resolution.Valid:
		scalarID = basicSettleScalarResolutionNull
	}
	scalarArgs := []any{next.duration.Float64, next.resolution.Int64, next.scalarRevision, s.libraryID, in.entityID, in.entityID}
	if before.duration.Valid {
		scalarArgs = append(scalarArgs, before.duration.Float64)
	}
	if before.resolution.Valid {
		scalarArgs = append(scalarArgs, before.resolution.Int64)
	}
	scalarArgs = append(scalarArgs, before.scalarRevision, before.membershipSequence)
	row, e = basicStageRead(ctx, turn, scalarID, scalarArgs...)
	if e != nil {
		return value, e
	}
	var d sql.NullFloat64
	var r sql.NullInt64
	var rev, seq, l, p, c, src int64
	if e = row.Scan(&l, &p, &c, &src, &d, &r, &rev, &seq); e != nil {
		return value, basicStageError(e)
	}
	if l != s.libraryID || p != in.entityID || c != in.entityID || src != 0 || d != next.duration || r != next.resolution || rev != next.scalarRevision || seq != before.membershipSequence {
		return value, ErrConflict
	}
	row, e = basicStageRead(ctx, turn, basicSettleShapeCAS, shapeRefs+1, a.shapeID, shapeRefs)
	if e != nil {
		return value, e
	}
	var returnedShape, returnedRefs int64
	if e = row.Scan(&returnedShape, &returnedRefs); e != nil {
		return value, basicStageError(e)
	}
	if returnedShape != a.shapeID || returnedRefs != shapeRefs+1 {
		return value, ErrConflict
	}
	row, e = basicStageRead(ctx, turn, basicSettleEntityCAS, next.durationKey, next.resolutionKey, next.scalarRevision, next.aggregateRevision, in.entityID, s.libraryID, before.durationKey, before.resolutionKey, before.scalarRevision, before.aggregateRevision)
	if e != nil {
		return value, e
	}
	var dk float64
	var rk, sr, ar int64
	if e = row.Scan(&entity, &library, &dk, &rk, &sr, &ar); e != nil {
		return value, basicStageError(e)
	}
	if entity != in.entityID || library != s.libraryID || dk != next.durationKey || rk != next.resolutionKey || sr != next.scalarRevision || ar != next.aggregateRevision {
		return value, ErrConflict
	}
	return next, nil
}
