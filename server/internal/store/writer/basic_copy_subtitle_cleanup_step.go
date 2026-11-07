package writer

import "context"

// Physical canonical deletion and exact language deltas precede the native
// continuation CAS in the SAME Stage receipt transaction. True exhaustion,
// including an empty old set, is the only transition to family9.
func cleanupBasicSubtitleStep(ctx context.Context, turn *commandTurn, in basicSubtitleCleanupEffects) (value basicCopyPosition, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	cleaned, err := cleanupBasicSubtitles(ctx, turn, in)
	if err != nil {
		return value, err
	}
	next := in.position
	if cleaned.more {
		if !cleaned.hasAfter || cleaned.processed == 0 {
			return value, ErrConflict
		}
		next.key, next.aux = cleaned.lastID, 1
	} else {
		next.family, next.copyPhase, next.id, next.aux, next.key = 9, 0, 0, 0, ""
	}
	return advanceBasicCopiedPosition(ctx, turn, in.family.source, in.position, next, in.shapeID)
}
