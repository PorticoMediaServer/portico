package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"unicode/utf8"
)

type basicChapterCopyEffects struct {
	family                                  *basicMediaFamily
	position                                basicCopyPosition
	shapeID, fileSizeRevision, invalidation int64
	ordinal                                 int64
	hadBefore                               bool
	before                                  basicChapter
}

// installBasicChapter copies ONE exact sealed family5 row in the caller's
// commandTurn. The caller owns cursor/receipt persistence and whole-command
// rollback on error. No family deletion, publication or cursor advance occurs.
func installBasicChapter(ctx context.Context, turn *commandTurn, in basicChapterCopyEffects) (value basicChapter, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	f, p := in.family, in.position
	if ctx == nil || turn == nil || turn.transaction == nil || f == nil || !f.valid || f.family != 5 ||
		in.ordinal < 0 || in.ordinal >= int64(len(f.rows)) || f.rawChapterCount < 0 ||
		p.changeID != f.source.changeID || p.changeRevision <= 0 || p.phase != 2 || p.afterEntity <= 0 ||
		p.family < 0 || p.copyPhase < 0 || p.id < 0 || p.aux < 0 || p.aux > 1 || in.shapeID <= 0 || in.fileSizeRevision <= 0 || in.invalidation <= 0 {
		return value, ErrInvalid
	}
	// Preserve the producer's RAW pre-cleanup chapter bound, even for one row.
	if f.rawChapterCount > MaxRawChapters || len(f.rows) > MaxRawChapters {
		return value, ErrCap
	}
	if int64(len(f.rows)) > f.rawChapterCount {
		return value, ErrInvalid
	}
	s := f.source
	next, ok := f.rows[in.ordinal].(basicChapter)
	if !ok {
		return value, ErrInvalid
	}
	remaining := maxCanonicalRequestBytes
	for _, text := range [...]string{s.assetToken, s.runID, s.sourceID, s.incarnation, s.objectID, s.objectRevision, s.fingerprint, p.key, p.key2, p.key3, next.title, in.before.title} {
		if len(text) > remaining {
			return value, ErrCap
		}
		remaining -= len(text)
	}
	for _, text := range [...]string{p.key, p.key2, p.key3, in.before.title} {
		if !utf8.ValidString(text) {
			return value, ErrInvalid
		}
	}
	if !in.hadBefore && in.before != (basicChapter{}) {
		return value, ErrInvalid
	}
	if in.hadBefore && (in.before.index != next.index || math.IsNaN(in.before.start) || math.IsNaN(in.before.end) || math.IsInf(in.before.start, 0) || math.IsInf(in.before.end, 0)) {
		return value, ErrInvalid
	}
	if _, _, _, _, err = basicMediaValues(s, in.ordinal, next); err != nil {
		return value, err
	}
	if err = checkBasicInputAtPhase(ctx, turn, s, 2); err != nil {
		return value, err
	}
	row, err := basicStageRead(ctx, turn, basicPrefenceHeader, s.changeID)
	if err != nil {
		return value, err
	}
	actual := basicCopyPosition{changeID: s.changeID, phase: 2}
	var mask, upper, hasAfter, owner, generation, expires int64
	var target, available sql.NullInt64
	var object, revision sql.NullString
	if err = row.Scan(&actual.changeRevision, &mask, &upper, &hasAfter, &actual.afterEntity, &target, &available, &object, &revision,
		&actual.family, &actual.copyPhase, &actual.id, &actual.aux, &actual.key, &actual.key2, &actual.key3, &owner, &generation, &expires); err != nil {
		return value, basicStageError(err)
	}
	if actual != p || mask != 511 || upper != p.afterEntity || hasAfter != 1 || !target.Valid || target.Int64 != in.shapeID || !available.Valid || available.Int64 != 1 || object.Valid || revision.Valid || owner != 0 || generation != 0 || expires != 0 {
		return value, ErrConflict
	}
	// Read the entire one-owner set (LIMIT+1), never just a matching holder.
	rows, err := basicRows(ctx, turn, basicAcceptOwners, s.assetID, int64(2))
	if err != nil {
		return value, err
	}
	if err = basicMediaExactRow(rows, []any{p.afterEntity, s.libraryID, nil}); err != nil {
		return value, err
	}
	row, err = basicStageRead(ctx, turn, basicCopyChapterScope, s.assetID, p.afterEntity, s.assetID)
	if err != nil {
		return value, err
	}
	var pending, change, sizeRevision, invalidation, library, entityLibrary, kind, retired int64
	if err = row.Scan(&pending, &change, &sizeRevision, &invalidation, &library, &entityLibrary, &kind, &retired, &object, &revision); err != nil {
		return value, basicStageError(err)
	}
	if pending != 1 || change != s.changeID || sizeRevision != in.fileSizeRevision || invalidation != in.invalidation || library != s.libraryID || entityLibrary != s.libraryID || kind != 1 || retired != 0 || object.Valid || revision.Valid {
		return value, ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicStageFamily, s.changeID, int64(5))
	if err != nil {
		return value, err
	}
	var ordinal, expected, staged, sealed int64
	if err = row.Scan(&ordinal, &expected, &staged, &sealed); err != nil {
		return value, basicStageError(err)
	}
	if ordinal != 0 || expected != int64(len(f.rows)) || staged != expected || sealed != 1 {
		return value, ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicCopyChapterStaged, s.changeID, in.ordinal)
	if err != nil {
		return value, err
	}
	var token string
	var captured basicChapter
	if err = row.Scan(&ordinal, &token, &captured.index, &captured.title, &captured.start, &captured.end); err != nil {
		return value, basicStageError(err)
	}
	if ordinal != in.ordinal || token != s.assetToken || captured != next {
		return value, ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicCopyChapterPresent, s.assetToken, next.index)
	if err != nil {
		return value, err
	}
	var present int64
	err = row.Scan(&present)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return value, err
	}
	if (!in.hadBefore && err == nil) || (in.hadBefore && (err != nil || present != 1)) {
		return value, ErrConflict
	}
	id := basicCopyChapterInsert
	args := []any{s.assetToken, next.index, next.title, next.start, next.end}
	if in.hadBefore {
		id = basicCopyChapterUpdate
		args = []any{next.title, next.start, next.end, s.assetToken, next.index, in.before.title, in.before.start, in.before.end}
	}
	row, err = basicStageRead(ctx, turn, id, args...)
	if err != nil {
		return value, err
	}
	if err = row.Scan(&token, &value.index, &value.title, &value.start, &value.end); err != nil {
		return value, basicStageError(err)
	}
	if token != s.assetToken || value != next {
		return value, ErrConflict
	}
	return value, nil
}
