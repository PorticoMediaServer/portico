package writer

import (
	"context"
	"math"
)

// The scalar families have one sealed native row. Their actual row write and
// next-family CAS belong to the same receipt turn; neither clears a fence.
func copyBasicChapterFactStep(ctx context.Context, turn *commandTurn, in basicChapterFactCopyEffects) (value basicCopyPosition, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if !basicCopyFamilyStart(in.position, 4) || in.ordinal != 0 {
		return value, ErrInvalid
	}
	if _, err = installBasicChapterFact(ctx, turn, in); err != nil {
		return value, err
	}
	next := in.position
	next.family = 5
	return advanceBasicCopiedPosition(ctx, turn, in.family.source, in.position, next, in.shapeID)
}

func copyBasicSubtitleFactStep(ctx context.Context, turn *commandTurn, in basicSubtitleFactCopyEffects) (value basicCopyPosition, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if !basicCopyFamilyStart(in.position, 6) || in.ordinal != 0 {
		return value, ErrInvalid
	}
	if _, err = installBasicSubtitleFact(ctx, turn, in); err != nil {
		return value, err
	}
	next := in.position
	next.family = 7
	return advanceBasicCopiedPosition(ctx, turn, in.family.source, in.position, next, in.shapeID)
}

func basicCopyFamilyStart(p basicCopyPosition, family int64) bool {
	return p.phase == 2 && p.family == family && p.copyPhase == 0 && p.id == 0 && p.aux == 0 && p.key == "" && p.key2 == "" && p.key3 == ""
}

// Empty child input is a real sealed zero-row family, not completion of the
// old canonical population. Families 2/3/5 must still walk their cleanup keys.
// Global sidecars (7) are retained physical facts: this asset never deletes a
// shared sidecar merely because its new observation has no sidecars.
func startBasicEmptyFamily(ctx context.Context, turn *commandTurn, in basicStreamCopyFence) (value basicCopyPosition, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	f, p := in.family, in.position
	if ctx == nil || turn == nil || f == nil || !f.valid || len(f.rows) != 0 || !basicCopyFamilyStart(p, f.family) ||
		(f.family != 2 && f.family != 3 && f.family != 5 && f.family != 7) || in.ordinal != 0 || in.shapeID <= 0 || in.fileSizeRevision <= 0 || in.invalidation <= 0 {
		return value, ErrInvalid
	}
	if f.family == 5 && (f.rawChapterCount < 0 || f.rawChapterCount > MaxRawChapters) {
		return value, ErrCap
	}
	if err = checkBasicStreamCopyFence(ctx, turn, in); err != nil {
		return value, err
	}
	next := p
	if f.family == 7 {
		next.family = 8
	} else {
		next.copyPhase = 1
	}
	return advanceBasicCopiedPosition(ctx, turn, f.source, p, next, in.shapeID)
}

// Only a fully exhausted indexed native walk advances the family. A retained
// key consumes the same bounded page as a removed key.
func cleanupBasicMediaStep(ctx context.Context, turn *commandTurn, in basicStreamCopyFence) (value basicCopyPosition, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	cleaned, err := cleanupBasicMedia(ctx, turn, in)
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
		next.family++
		next.copyPhase, next.id, next.aux = 0, 0, 0
	}
	return advanceBasicCopiedPosition(ctx, turn, in.family.source, in.position, next, in.shapeID)
}

// The last family writes its folded evidence before the fully captured CJR
// phase 2 -> 3 CAS. This is copy completion only; asset and link pending fences,
// info_epoch, run progress and the original receipt remain parent-owned.
func copyBasicSubtitleEvidenceStep(ctx context.Context, turn *commandTurn, in basicSubtitleEvidenceCopyEffects) (value basicCopyPosition, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	p := in.position
	if !basicCopyFamilyStart(p, 9) || in.ordinal != 0 || p.changeRevision <= 0 || p.changeRevision == math.MaxInt64 {
		return value, ErrInvalid
	}
	if _, err = installBasicSubtitleEvidence(ctx, turn, in); err != nil {
		return value, err
	}
	s := in.family.source
	// Reuse the finite captured phase CAS; every old field is restated, rather
	// than treating a source hash or empty directory cursor as completion proof.
	row, err := basicStageRead(ctx, turn, basicPrefenceChangeCAS,
		int64(3), int64(1), p.afterEntity, int64(0), p.changeRevision+1,
		s.changeID, s.assetID, s.assetToken, int64(0), int64(2), s.expectedEpoch, s.inputRevision, int64(511), s.witnessFingerprint[:], p.changeRevision,
		p.afterEntity, int64(1), p.afterEntity, in.shapeID, int64(1), p.family, p.copyPhase, p.id, p.aux, p.key, p.key2, p.key3, int64(0), int64(0), int64(0), "")
	if err != nil {
		return value, err
	}
	if err = row.Scan(&value.changeID, &value.changeRevision, &value.phase, &value.afterEntity, &value.family, &value.copyPhase, &value.id, &value.aux, &value.key, &value.key2, &value.key3); err != nil {
		return value, basicStageError(err)
	}
	expected := basicCopyPosition{changeID: s.changeID, changeRevision: p.changeRevision + 1, phase: 3, afterEntity: p.afterEntity}
	if value != expected {
		return value, ErrConflict
	}
	return value, nil
}
