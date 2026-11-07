package writer

import "context"

// The parent owns command custody, receipt, rollback and final publication.
// One track, its actual language membership and its continuation share a turn.
func copyBasicSubtitleStep(ctx context.Context, turn *commandTurn, in basicSubtitleCopyEffects) (value basicCopyPosition, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	f, p := in.family, in.position
	if f == nil || !f.valid || f.family != 8 || p.phase != 2 || p.family != 8 || p.copyPhase != 0 || p.id != in.ordinal || p.aux != 0 || p.key != "" || p.key2 != "" || p.key3 != "" {
		return value, ErrInvalid
	}
	actual, err := installBasicSubtitle(ctx, turn, in)
	if err != nil {
		return value, err
	}
	if err = applyBasicSubtitleLanguageDelta(ctx, turn, basicSubtitleLanguageDelta{hadBefore: in.hadBefore, hasAfter: true, before: in.before.language, after: actual.language}); err != nil {
		return value, err
	}
	next := p
	next.id++ // bounded frozen input ordinal
	if next.id == int64(len(f.rows)) {
		next.copyPhase, next.id = 1, 0
	}
	return advanceBasicCopiedPosition(ctx, turn, f.source, p, next, in.shapeID)
}
