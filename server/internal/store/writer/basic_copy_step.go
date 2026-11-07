package writer

import (
	"context"
	"math"
)

// These components install one canonical row and its actual native continuation
// in one turn. The command caller still owns the run CAS, immutable receipt,
// rollback and publication. A copy prefix never clears either pending fence.
func copyBasicStreamFactStep(ctx context.Context, turn *commandTurn, in basicStreamCopyEffects) (value basicCopyPosition, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	p := in.position
	if p.family != 0 || p.copyPhase != 0 || p.id != 0 || p.aux != 0 || p.key != "" || p.key2 != "" || p.key3 != "" {
		return value, ErrInvalid
	}
	if _, err = installBasicStreamFact(ctx, turn, in); err != nil {
		return value, err
	}
	next := p
	next.family = 2
	return advanceBasicCopiedPosition(ctx, turn, in.source, p, next, in.shapeID)
}

type basicMediaCopyStep struct {
	fence     basicStreamCopyFence
	hadBefore bool
	before    basicMediaRow
}

func copyBasicMediaRowStep(ctx context.Context, turn *commandTurn, in basicMediaCopyStep) (value basicCopyPosition, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	f, p := in.fence.family, in.fence.position
	if f == nil || !f.valid || p.family != f.family || p.copyPhase != 0 || p.id != in.fence.ordinal || p.aux != 0 || p.key != "" || p.key2 != "" || p.key3 != "" {
		return value, ErrInvalid
	}
	switch f.family {
	case 2:
		before, ok := in.before.(basicStreamRow)
		if !ok {
			return value, ErrInvalid
		}
		_, err = installBasicStreamRow(ctx, turn, basicStreamRowCopyEffects{in.fence, in.hadBefore, before})
	case 3:
		before, ok := in.before.(basicStreamBreadth)
		if !ok {
			return value, ErrInvalid
		}
		_, err = installBasicStreamBreadth(ctx, turn, basicStreamBreadthCopyEffects{in.fence, in.hadBefore, before})
	case 7:
		before, ok := in.before.(basicSubtitleSidecar)
		if !ok {
			return value, ErrInvalid
		}
		_, err = installBasicSubtitleSidecar(ctx, turn, basicSubtitleSidecarCopyEffects{in.fence, in.hadBefore, before})
	case 5:
		before, ok := in.before.(basicChapter)
		if !ok {
			return value, ErrInvalid
		}
		_, err = installBasicChapter(ctx, turn, basicChapterCopyEffects{family: f, position: p, shapeID: in.fence.shapeID, fileSizeRevision: in.fence.fileSizeRevision, invalidation: in.fence.invalidation, ordinal: in.fence.ordinal, hadBefore: in.hadBefore, before: before})
	default:
		return value, ErrNotImplemented
	}
	if err != nil {
		return value, err
	}
	next := p
	next.id++ // bounded frozen input_ordinal, never a filesystem cursor
	if next.id == int64(len(f.rows)) {
		// Cleanup has its own indexed canonical-key walk. No family is declared
		// complete merely because its last new input row was installed.
		if f.family == 7 {
			// Global physical sidecars are shared; only this asset's tracks
			// are later reconciled and removed.
			next.family, next.id = 8, 0
		} else {
			next.copyPhase, next.id = 1, 0
		}
	}
	return advanceBasicCopiedPosition(ctx, turn, f.source, p, next, in.fence.shapeID)
}

func advanceBasicCopiedPosition(ctx context.Context, turn *commandTurn, s basicStreamFactStage, before, next basicCopyPosition, shapeID int64) (value basicCopyPosition, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if before.changeRevision <= 0 || before.changeRevision == math.MaxInt64 || before.changeID != s.changeID || before.phase != 2 || before.afterEntity <= 0 || next.changeID != before.changeID || next.changeRevision != before.changeRevision || next.phase != before.phase || next.afterEntity != before.afterEntity || shapeID <= 0 {
		return value, ErrInvalid
	}
	next.changeRevision++
	row, err := basicStageRead(ctx, turn, basicCopyPositionCAS, next.changeRevision, next.family, next.copyPhase, next.id, next.aux, next.key, next.key2, next.key3,
		s.changeID, s.assetID, s.assetToken, int64(0), before.phase, s.expectedEpoch, s.inputRevision, int64(511), s.witnessFingerprint[:], before.changeRevision, before.afterEntity, int64(1), before.afterEntity, shapeID, int64(1), before.family, before.copyPhase, before.id, before.aux, before.key, before.key2, before.key3, int64(0), int64(0), int64(0), "")
	if err != nil {
		return value, err
	}
	if err = row.Scan(&value.changeID, &value.changeRevision, &value.phase, &value.afterEntity, &value.family, &value.copyPhase, &value.id, &value.aux, &value.key, &value.key2, &value.key3); err != nil {
		return value, basicStageError(err)
	}
	if value != next {
		return value, ErrConflict
	}
	return value, nil
}

// A cleanup prefix and its true canonical-key continuation share one turn.
// Only an exhausted native lookahead moves to family3; neither branch marks
// the complete copy or publication ready.
func cleanupBasicStreamStep(ctx context.Context, turn *commandTurn, in basicStreamCopyFence) (value basicCopyPosition, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	cleaned, err := cleanupBasicStreams(ctx, turn, in)
	if err != nil {
		return value, err
	}
	next := in.position
	if cleaned.more {
		if !cleaned.hasAfter {
			return value, ErrConflict
		}
		next.id, next.aux = cleaned.id, 1
	} else {
		next.family, next.copyPhase, next.id, next.aux = 3, 0, 0, 0
	}
	return advanceBasicCopiedPosition(ctx, turn, in.family.source, in.position, next, in.shapeID)
}
