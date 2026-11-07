package writer

import (
	"context"
	"database/sql"
	"errors"
	"unicode/utf8"
)

type basicCanonicalStreamFact struct {
	revision, size, modifiedNS, detailVersion int64
	fingerprint                               string
}

type basicStreamCopyEffects struct {
	source                                  basicStreamFactStage
	position                                basicCopyPosition
	shapeID, fileSizeRevision, invalidation int64
	hadBefore                               bool
	before                                  basicCanonicalStreamFact
}

// installBasicStreamFact installs ONE sealed canonical parent without deleting
// it or cascading into its children. It does not advance the copy cursor: its
// caller must persist the actual next cursor and receipt in the SAME turn.
// The publication fences remain set; this return is never Basic availability.
func installBasicStreamFact(ctx context.Context, turn *commandTurn, in basicStreamCopyEffects) (value basicCanonicalStreamFact, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	s, p := in.source, in.position
	if ctx == nil || turn == nil || turn.transaction == nil || p.changeID != s.changeID || p.changeRevision <= 0 || p.phase != 2 || p.afterEntity <= 0 ||
		p.family < 0 || p.copyPhase < 0 || p.id < 0 || p.aux < 0 || p.aux > 1 || in.shapeID <= 0 || in.fileSizeRevision <= 0 || in.invalidation <= 0 {
		return value, ErrInvalid
	}
	remaining := maxCanonicalRequestBytes
	for _, text := range [...]string{s.assetToken, s.runID, s.sourceID, s.incarnation, s.objectID, s.objectRevision, s.fingerprint, p.key, p.key2, p.key3, in.before.fingerprint} {
		if len(text) > remaining {
			return value, ErrCap
		}
		remaining -= len(text)
	}
	for _, text := range [...]string{p.key, p.key2, p.key3} {
		if !utf8.ValidString(text) {
			return value, ErrInvalid
		}
	}
	if !in.hadBefore && in.before != (basicCanonicalStreamFact{}) {
		return value, ErrInvalid
	}
	if in.hadBefore && (in.before.revision < 0 || in.before.size < 0 || in.before.detailVersion < 0 || !utf8.ValidString(in.before.fingerprint)) {
		return value, ErrInvalid
	}
	if err = checkBasicInputAtPhase(ctx, turn, s, 2); err != nil {
		return value, err
	}
	row, err := basicStageRead(ctx, turn, basicPrefenceHeader, s.changeID)
	if err != nil {
		return value, err
	}
	var actual basicCopyPosition
	actual.changeID = s.changeID
	actual.phase = 2 // proved by checkBasicInputAtPhase, not an inferred transition
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
	row, err = basicStageRead(ctx, turn, basicCopyFactScope, s.assetID, p.afterEntity, s.assetID)
	if err != nil {
		return value, err
	}
	var pending, change, sizeRevision, invalidation, library int64
	if err = row.Scan(&pending, &change, &sizeRevision, &invalidation, &library); err != nil {
		return value, basicStageError(err)
	}
	if pending != 1 || change != s.changeID || sizeRevision != in.fileSizeRevision || invalidation != in.invalidation || library != s.libraryID {
		return value, ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicStageFamily, s.changeID, int64(1))
	if err != nil {
		return value, err
	}
	var ordinal, expected, staged, sealed int64
	if err = row.Scan(&ordinal, &expected, &staged, &sealed); err != nil {
		return value, basicStageError(err)
	}
	if ordinal != 0 || expected != 1 || staged != 1 || sealed != 1 {
		return value, ErrConflict
	}
	step, ok := turn.steps[basicSealStreamFact]
	if !ok {
		return value, invalid("unregistered_basic_step")
	}
	rows, err := turn.query(ctx, step.statementStep, s.changeID, int64(2))
	if err != nil {
		return value, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	if !rows.Next() {
		if err = rows.Err(); err != nil {
			return value, err
		}
		return value, ErrConflict
	}
	var token string
	var next basicCanonicalStreamFact
	if err = rows.Scan(&ordinal, &token, &next.revision, &next.size, &next.modifiedNS, &next.fingerprint, &next.detailVersion); err != nil {
		return value, err
	}
	if rows.Next() {
		return value, ErrConflict
	}
	if err = rows.Err(); err != nil {
		return value, err
	}
	if ordinal != 0 || token != s.assetToken || next != (basicCanonicalStreamFact{s.factRevision, s.size, s.modifiedNS, s.detailVersion, s.fingerprint}) ||
		(in.hadBefore && in.before.revision > next.revision) {
		return value, ErrConflict
	}
	if err = rows.Close(); err != nil {
		return value, err
	}
	row, err = basicStageRead(ctx, turn, basicCopyFactPresent, s.assetToken)
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
	args := []any{s.assetToken, next.revision, next.size, next.modifiedNS, next.fingerprint, next.detailVersion}
	id := basicCopyFactInsert
	if in.hadBefore {
		id = basicCopyFactUpdate
		args = []any{next.revision, next.size, next.modifiedNS, next.fingerprint, next.detailVersion, s.assetToken,
			in.before.revision, in.before.size, in.before.modifiedNS, in.before.fingerprint, in.before.detailVersion}
	}
	row, err = basicStageRead(ctx, turn, id, args...)
	if err != nil {
		return value, err
	}
	if err = row.Scan(&token, &value.revision, &value.size, &value.modifiedNS, &value.fingerprint, &value.detailVersion); err != nil {
		return value, basicStageError(err)
	}
	if token != s.assetToken || value != next {
		return value, ErrConflict
	}
	return value, nil
}
