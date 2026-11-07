package writer

import (
	"context"
	"database/sql"
	"errors"
	"unicode/utf8"
)

// Only the family6 source payload is captured here. Folded family9 inventory
// evidence and subtitle children are preserved by the literal UPDATE.
type basicCanonicalSubtitleFact struct {
	revision, size, modifiedNS, originUS int64
	timingKnown                          bool
	status, fingerprint                  string
}

type basicSubtitleFactCopyEffects struct {
	family                                  *basicMediaFamily
	position                                basicCopyPosition
	shapeID, fileSizeRevision, invalidation int64
	ordinal                                 int64
	hadBefore                               bool
	before                                  basicCanonicalSubtitleFact
}

// installBasicSubtitleFact copies ONE exact sealed family6 row in the caller's
// commandTurn. The caller owns cursor/receipt persistence and whole-command
// rollback on error. No family deletion, publication or cursor advance occurs.
func installBasicSubtitleFact(ctx context.Context, turn *commandTurn, in basicSubtitleFactCopyEffects) (value basicCanonicalSubtitleFact, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	f, p := in.family, in.position
	if ctx == nil || turn == nil || turn.transaction == nil || f == nil || !f.valid || f.family != 6 ||
		in.ordinal < 0 || in.ordinal >= int64(len(f.rows)) ||
		p.changeID != f.source.changeID || p.changeRevision <= 0 || p.phase != 2 || p.afterEntity <= 0 ||
		p.family < 0 || p.copyPhase < 0 || p.id < 0 || p.aux < 0 || p.aux > 1 || in.shapeID <= 0 || in.fileSizeRevision <= 0 || in.invalidation <= 0 {
		return value, ErrInvalid
	}
	if len(f.rows) != 1 {
		return value, ErrInvalid
	}

	s := f.source
	next, ok := f.rows[in.ordinal].(basicSubtitleFact)
	if !ok {
		return value, ErrInvalid
	}
	remaining := maxCanonicalRequestBytes
	for _, text := range [...]string{s.assetToken, s.runID, s.sourceID, s.incarnation, s.objectID, s.objectRevision, s.fingerprint, p.key, p.key2, p.key3, next.status, next.fingerprint, in.before.status, in.before.fingerprint} {
		if len(text) > remaining {
			return value, ErrCap
		}
		remaining -= len(text)
	}
	for _, text := range [...]string{p.key, p.key2, p.key3, in.before.status, in.before.fingerprint} {
		if !utf8.ValidString(text) {
			return value, ErrInvalid
		}
	}
	if !in.hadBefore && in.before != (basicCanonicalSubtitleFact{}) {
		return value, ErrInvalid
	}
	if in.hadBefore && in.before.revision > next.revision {
		return value, ErrConflict
	}
	if in.hadBefore && (in.before.revision < 0 || in.before.size < 0) {
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
	row, err = basicStageRead(ctx, turn, basicCopySubtitleFactScope, s.assetID, p.afterEntity, s.assetID)
	if err != nil {
		return value, err
	}
	var pending, change, sizeRevision, invalidation, library, entityLibrary, kind, retired, subject, subjectLibrary, subjectKind int64
	if err = row.Scan(&pending, &change, &sizeRevision, &invalidation, &library, &entityLibrary, &kind, &retired, &object, &revision, &subject, &subjectLibrary, &subjectKind); err != nil {
		return value, basicStageError(err)
	}
	if pending != 1 || change != s.changeID || sizeRevision != in.fileSizeRevision || invalidation != in.invalidation || library != s.libraryID || entityLibrary != s.libraryID || kind != 1 || retired != 0 || object.Valid || revision.Valid || subject != p.afterEntity || subjectLibrary != s.libraryID || subjectKind != 1 {
		return value, ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicStageFamily, s.changeID, int64(6))
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
	row, err = basicStageRead(ctx, turn, basicCopySubtitleFactStaged, s.changeID, in.ordinal)
	if err != nil {
		return value, err
	}
	var token string
	var captured basicCanonicalSubtitleFact
	if err = row.Scan(&ordinal, &token, &captured.revision, &captured.size, &captured.modifiedNS, &captured.originUS, &captured.timingKnown, &captured.status, &captured.fingerprint); err != nil {
		return value, basicStageError(err)
	}
	if ordinal != in.ordinal || token != s.assetToken || captured != (basicCanonicalSubtitleFact{next.revision, s.size, s.modifiedNS, next.originUS, next.timingKnown, next.status, next.fingerprint}) {
		return value, ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicCopySubtitleFactPresent, s.assetToken)
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
	id := basicCopySubtitleFactInsert
	args := []any{s.assetToken, next.revision, s.size, s.modifiedNS, next.originUS, basicBool(next.timingKnown), next.status, next.fingerprint}
	if in.hadBefore {
		id = basicCopySubtitleFactUpdate
		args = []any{next.revision, s.size, s.modifiedNS, next.originUS, basicBool(next.timingKnown), next.status, next.fingerprint, s.assetToken, in.before.revision, in.before.size, in.before.modifiedNS, in.before.originUS, basicBool(in.before.timingKnown), in.before.status, in.before.fingerprint}
	}

	row, err = basicStageRead(ctx, turn, id, args...)
	if err != nil {
		return value, err
	}
	if err = row.Scan(&token, &value.revision, &value.size, &value.modifiedNS, &value.originUS, &value.timingKnown, &value.status, &value.fingerprint); err != nil {
		return value, basicStageError(err)
	}
	if token != s.assetToken || value != captured {
		return value, ErrConflict
	}
	return value, nil
}
