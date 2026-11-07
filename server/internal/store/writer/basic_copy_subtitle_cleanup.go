package writer

import (
	"context"
	"database/sql"
	"errors"
	"unicode/utf8"
)

// The parent freezes before with its request and persists the returned native
// cursor in this SAME commandTurn. Retained rows consume the eight-row budget.
type basicSubtitleCleanupEffects struct {
	basicStreamCopyFence
	before []basicSubtitle
}
type basicSubtitleCleanupResult struct {
	lastID             string
	hasAfter, more     bool
	processed, deleted int64
}

func preflightBasicSubtitleCleanup(in basicSubtitleCleanupEffects) error {
	f, p := in.family, in.position
	if f == nil || !f.valid || f.family != 8 || p.changeID != f.source.changeID || p.changeRevision <= 0 || p.phase != 2 || p.afterEntity <= 0 || p.family != 8 || p.copyPhase != 1 || p.id != 0 || p.aux < 0 || p.aux > 1 || (p.aux == 0 && p.key != "") || p.key2 != "" || p.key3 != "" || in.shapeID <= 0 || in.fileSizeRevision <= 0 || in.invalidation <= 0 {
		return ErrInvalid
	}
	if len(in.before) > 8 || int64(len(f.rows)) > basicMediaFamilyLimit(8) {
		return ErrCap
	}
	s := f.source
	remaining := maxCanonicalRequestBytes
	admit := func(v string) error {
		if len(v) > remaining {
			return ErrCap
		}
		remaining -= len(v)
		if !utf8.ValidString(v) {
			return ErrInvalid
		}
		return nil
	}
	for _, v := range []string{s.assetToken, s.runID, s.sourceID, s.incarnation, s.objectID, s.objectRevision, s.fingerprint, p.key} {
		if err := admit(v); err != nil {
			return err
		}
	}
	previous, hasPrevious := p.key, p.aux == 1
	for _, r := range in.before {
		if (hasPrevious && r.id <= previous) || (r.origin != "embedded" && r.origin != "sidecar") || (r.origin == "embedded" && (r.sidecarID != "" || r.streamIndex < 0)) || (r.origin == "sidecar" && r.streamIndex != -1) {
			return ErrInvalid
		}
		for _, v := range []string{r.id, r.origin, r.locator, r.sidecarID, r.format, r.language, r.title, r.reason} {
			if err := admit(v); err != nil {
				return err
			}
		}
		previous, hasPrevious = r.id, true
	}
	return nil
}

func basicSubtitleCleanupTuple(asset string, r basicSubtitle) []any {
	var sidecar any
	if r.origin == "sidecar" {
		sidecar = r.sidecarID
	}
	return []any{r.id, asset, r.origin, r.locator, r.streamIndex, sidecar, r.format, r.language, r.title, basicBool(r.isDefault), basicBool(r.isForced), r.reason, basicBool(r.hearingImpaired)}
}

// No unknown canonical TEXT crosses the driver boundary. Gap probes return one
// constant; point checks and deletes compare the complete caller-captured tuple.
func cleanupBasicSubtitles(ctx context.Context, turn *commandTurn, in basicSubtitleCleanupEffects) (value basicSubtitleCleanupResult, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil {
		return value, ErrInvalid
	}
	if err = preflightBasicSubtitleCleanup(in); err != nil {
		return value, err
	}
	if err = checkBasicStreamCopyFence(ctx, turn, in.basicStreamCopyFence); err != nil {
		return value, err
	}
	s, p := in.family.source, in.position
	result := basicSubtitleCleanupResult{lastID: p.key, hasAfter: p.aux == 1}
	for _, r := range in.before {
		id, args := basicCleanupSubtitleGapFirst, []any{s.assetToken, r.id}
		if result.hasAfter {
			id, args = basicCleanupSubtitleGapAfter, []any{s.assetToken, result.lastID, r.id}
		}
		rows, e := basicRows(ctx, turn, id, args...)
		if e != nil {
			return value, e
		}
		if e = basicSubtitleCapturedRow(rows, nil); e != nil {
			return value, e
		}
		tuple := basicSubtitleCleanupTuple(s.assetToken, r)
		rows, e = basicRows(ctx, turn, basicCleanupSubtitleExact, tuple...)
		if e != nil {
			return value, e
		}
		if e = basicMediaExactRow(rows, []any{int64(1)}); e != nil {
			return value, e
		}
		row, e := basicStageRead(ctx, turn, basicCleanupSubtitleRetained, s.changeID, r.id, s.assetToken)
		if e != nil {
			return value, e
		}
		var ordinal int64
		e = row.Scan(&ordinal)
		if errors.Is(e, sql.ErrNoRows) {
			rows, e = basicRows(ctx, turn, basicCleanupSubtitleDelete, tuple...)
			if e != nil {
				return value, e
			}
			if e = basicMediaExactRow(rows, tuple); e != nil {
				return value, e
			}
			if e = applyBasicSubtitleLanguageDelta(ctx, turn, basicSubtitleLanguageDelta{hadBefore: true, before: r.language}); e != nil {
				return value, e
			}
			result.deleted++
		} else if e != nil {
			return value, e
		} else if ordinal < 0 || ordinal >= int64(len(in.family.rows)) {
			return value, ErrConflict
		}
		result.lastID, result.hasAfter = r.id, true
		result.processed++
	}
	id, args := basicCleanupSubtitleMoreFirst, []any{s.assetToken}
	if result.hasAfter {
		id, args = basicCleanupSubtitleMoreAfter, []any{s.assetToken, result.lastID}
	}
	row, e := basicStageRead(ctx, turn, id, args...)
	if e != nil {
		return value, e
	}
	var one int64
	e = row.Scan(&one)
	if e == nil {
		if one != 1 {
			return value, ErrConflict
		}
		result.more = true
	} else if !errors.Is(e, sql.ErrNoRows) {
		return value, e
	}
	return result, nil
}
