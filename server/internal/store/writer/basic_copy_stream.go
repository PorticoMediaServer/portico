package writer

import (
	"context"
	"database/sql"
	"errors"
	"unicode/utf8"
)

// These inert inputs copy one captured row. The immutable family owns source
// and next-row strings; before is consumed synchronously and is never retained.
// The caller owns command composition, rollback, cursor and receipt persistence.
type basicStreamCopyFence struct {
	family                                  *basicMediaFamily
	position                                basicCopyPosition
	shapeID, fileSizeRevision, invalidation int64
	ordinal                                 int64
}
type basicStreamRowCopyEffects struct {
	basicStreamCopyFence
	hadBefore bool
	before    basicStreamRow
}
type basicStreamBreadthCopyEffects struct {
	basicStreamCopyFence
	hadBefore bool
	before    basicStreamBreadth
}

// Admit the COMPLETE raw row and before payload before JSON validation, args,
// cloning or native reads. Global per-class caps belong to the frozen family.
func preflightBasicStreamCopy(in basicStreamCopyFence, next, before basicMediaRow) error {
	f, p := in.family, in.position
	if f == nil || !f.valid || (f.family != 2 && f.family != 3) || in.ordinal < 0 || in.ordinal >= int64(len(f.rows)) ||
		p.changeID != f.source.changeID || p.changeRevision <= 0 || p.phase != 2 || p.afterEntity <= 0 ||
		p.family < 0 || p.copyPhase < 0 || p.id < 0 || p.aux < 0 || p.aux > 1 || in.shapeID <= 0 || in.fileSizeRevision <= 0 || in.invalidation <= 0 {
		return ErrInvalid
	}
	if int64(len(f.rows)) > basicMediaFamilyLimit(f.family) {
		return ErrCap
	}
	s := f.source
	remaining := maxCanonicalRequestBytes
	source := [...]string{s.assetToken, s.runID, s.sourceID, s.incarnation, s.objectID, s.objectRevision, s.fingerprint, p.key, p.key2, p.key3}
	a, af, _, abase, err := basicMediaTextFields(s, next)
	if err != nil || af != f.family {
		return ErrInvalid
	}
	b, bf, _, bbase, err := basicMediaTextFields(s, before)
	if err != nil || bf != f.family {
		return ErrInvalid
	}
	if abase+bbase > int64(remaining) {
		return ErrCap
	}
	remaining -= int(abase + bbase)
	for _, texts := range [][]string{source[:], a[:], b[:]} {
		for _, text := range texts {
			if len(text) > remaining {
				return ErrCap
			}
			remaining -= len(text)
		}
	}
	for _, texts := range [][]string{source[:], a[:], b[:]} {
		for _, text := range texts {
			if !utf8.ValidString(text) {
				return ErrInvalid
			}
		}
	}
	return nil
}

// Recheck the original source, pending fences, entire frozen CJR position and
// sole ordinary holder, including its retained same-library subject.
func checkBasicStreamCopyFence(ctx context.Context, turn *commandTurn, in basicStreamCopyFence) error {
	f, p := in.family, in.position
	s := f.source
	if err := checkBasicInputAtPhase(ctx, turn, s, 2); err != nil {
		return err
	}
	row, err := basicStageRead(ctx, turn, basicPrefenceHeader, s.changeID)
	if err != nil {
		return err
	}
	actual := basicCopyPosition{changeID: s.changeID, phase: 2}
	var mask, upper, hasAfter, owner, generation, expires int64
	var target, available sql.NullInt64
	var object, revision sql.NullString
	if err = row.Scan(&actual.changeRevision, &mask, &upper, &hasAfter, &actual.afterEntity, &target, &available, &object, &revision,
		&actual.family, &actual.copyPhase, &actual.id, &actual.aux, &actual.key, &actual.key2, &actual.key3, &owner, &generation, &expires); err != nil {
		return basicStageError(err)
	}
	if actual != p || mask != 511 || upper != p.afterEntity || hasAfter != 1 || !target.Valid || target.Int64 != in.shapeID || !available.Valid || available.Int64 != 1 || object.Valid || revision.Valid || owner != 0 || generation != 0 || expires != 0 {
		return ErrConflict
	}
	rows, err := basicRows(ctx, turn, basicAcceptOwners, s.assetID, int64(2))
	if err != nil {
		return err
	}
	if err = basicMediaExactRow(rows, []any{p.afterEntity, s.libraryID, nil}); err != nil {
		return err
	}
	row, err = basicStageRead(ctx, turn, basicCopyStreamScope, s.assetID, p.afterEntity, s.assetID)
	if err != nil {
		return err
	}
	var pending, change, sizeRevision, invalidation, library, entityLibrary, kind, retired, subject, subjectLibrary, subjectKind int64
	if err = row.Scan(&pending, &change, &sizeRevision, &invalidation, &library, &entityLibrary, &kind, &retired, &object, &revision, &subject, &subjectLibrary, &subjectKind); err != nil {
		return basicStageError(err)
	}
	if pending != 1 || change != s.changeID || sizeRevision != in.fileSizeRevision || invalidation != in.invalidation || library != s.libraryID || entityLibrary != s.libraryID || kind != 1 || retired != 0 || object.Valid || revision.Valid || subject != p.afterEntity || subjectLibrary != s.libraryID || subjectKind != 1 {
		return ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicStageFamily, s.changeID, f.family)
	if err != nil {
		return err
	}
	var ordinal, expected, staged, sealed int64
	if err = row.Scan(&ordinal, &expected, &staged, &sealed); err != nil {
		return basicStageError(err)
	}
	if ordinal != 0 || expected != int64(len(f.rows)) || staged != expected || sealed != 1 {
		return ErrConflict
	}
	return nil
}

// installBasicStreamRow changes only one family2 core payload. In particular,
// it never replaces/deletes its native row or modifies retained breadth.
func installBasicStreamRow(ctx context.Context, turn *commandTurn, in basicStreamRowCopyEffects) (value basicStreamRow, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil || in.family == nil || in.family.family != 2 || in.ordinal < 0 || in.ordinal >= int64(len(in.family.rows)) {
		return value, ErrInvalid
	}
	next, ok := in.family.rows[in.ordinal].(basicStreamRow)
	if !ok {
		return value, ErrInvalid
	}
	if err = preflightBasicStreamCopy(in.basicStreamCopyFence, next, in.before); err != nil {
		return value, err
	}
	if !in.hadBefore && in.before != (basicStreamRow{}) {
		return value, ErrInvalid
	}
	if in.hadBefore && (in.before.index != next.index || validateBasicStreamRow(in.before) != nil) {
		return value, ErrInvalid
	}
	s := in.family.source
	_, _, args, _, err := basicMediaValues(s, in.ordinal, next)
	if err != nil {
		return value, err
	}
	if err = checkBasicStreamCopyFence(ctx, turn, in.basicStreamCopyFence); err != nil {
		return value, err
	}
	row, err := basicStageRead(ctx, turn, basicCopyStreamStaged, s.changeID, in.ordinal)
	if err != nil {
		return value, err
	}
	var ordinal int64
	var token string
	var captured basicStreamRow
	if err = row.Scan(&ordinal, &token, &captured.index, &captured.streamType, &captured.codec, &captured.language, &captured.title, &captured.channels, &captured.channelLayout, &captured.isDefault, &captured.isForced, &captured.colorTransfer, &captured.colorPrimaries, &captured.detail); err != nil {
		return value, basicStageError(err)
	}
	if ordinal != in.ordinal || token != s.assetToken || captured != next {
		return value, ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicCopyStreamPresent, s.assetToken, next.index)
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
	id := basicCopyStreamInsert
	args = args[1:] // native asset token, key, and complete next core payload
	if in.hadBefore {
		_, _, before, _, e := basicMediaValues(s, in.ordinal, in.before)
		if e != nil {
			return value, e
		}
		id = basicCopyStreamUpdate
		update := append([]any{}, args[2:]...)
		update = append(update, s.assetToken, next.index)
		args = append(update, before[3:]...)
	}
	row, err = basicStageRead(ctx, turn, id, args...)
	if err != nil {
		return value, err
	}
	if err = row.Scan(&token, &value.index, &value.streamType, &value.codec, &value.language, &value.title, &value.channels, &value.channelLayout, &value.isDefault, &value.isForced, &value.colorTransfer, &value.colorPrimaries, &value.detail); err != nil {
		return value, basicStageError(err)
	}
	if token != s.assetToken || value != next {
		return value, ErrConflict
	}
	return value, nil
}

// The native schema folds family3 into asset_streams: absence is the explicit
// has_asset_stream_breadth=0 discriminator. Its self-FK is not a missing-parent
// proof, so both logical insertion and replacement require the actual row.
func installBasicStreamBreadth(ctx context.Context, turn *commandTurn, in basicStreamBreadthCopyEffects) (value basicStreamBreadth, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil || in.family == nil || in.family.family != 3 || in.ordinal < 0 || in.ordinal >= int64(len(in.family.rows)) {
		return value, ErrInvalid
	}
	next, ok := in.family.rows[in.ordinal].(basicStreamBreadth)
	if !ok {
		return value, ErrInvalid
	}
	if err = preflightBasicStreamCopy(in.basicStreamCopyFence, next, in.before); err != nil {
		return value, err
	}
	if !in.hadBefore && in.before != (basicStreamBreadth{}) {
		return value, ErrInvalid
	}
	if in.hadBefore && (in.before.index != next.index || validateBasicStreamBreadth(in.before) != nil) {
		return value, ErrInvalid
	}
	s := in.family.source
	_, _, args, _, err := basicMediaValues(s, in.ordinal, next)
	if err != nil {
		return value, err
	}
	if err = checkBasicStreamCopyFence(ctx, turn, in.basicStreamCopyFence); err != nil {
		return value, err
	}
	row, err := basicStageRead(ctx, turn, basicCopyBreadthStaged, s.changeID, in.ordinal)
	if err != nil {
		return value, err
	}
	var ordinal int64
	var token string
	var captured basicStreamBreadth
	if err = row.Scan(&ordinal, &token, &captured.index, &captured.audioDescription, &captured.audioVariant, &captured.chroma, &captured.fpsNumerator, &captured.fpsDenominator, &captured.frameMode, &captured.interlace, &captured.telecine, &captured.doviRPU, &captured.doviBase, &captured.doviKnown, &captured.doviEnhancement, &captured.doviCM, &captured.playbackFramesRevision, &captured.packetPeakRevision); err != nil {
		return value, basicStageError(err)
	}
	if ordinal != in.ordinal || token != s.assetToken || captured != next {
		return value, ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicCopyBreadthParent, s.assetToken, next.index)
	if err != nil {
		return value, err
	}
	var index, present int64
	var streamType string
	if err = row.Scan(&index, &streamType, &present); err != nil {
		return value, basicStageError(err)
	}
	if index != next.index || index < 0 || basicStreamClass(streamType) == 0 || present != basicBool(in.hadBefore) {
		return value, ErrConflict
	}
	id := basicCopyBreadthInsert
	update := append([]any{int64(1)}, args[3:]...)
	update = append(update, s.assetToken, next.index, basicBool(in.hadBefore))
	if in.hadBefore {
		_, _, before, _, e := basicMediaValues(s, in.ordinal, in.before)
		if e != nil {
			return value, e
		}
		id = basicCopyBreadthUpdate
		update = append(update, before[3:]...)
	}
	row, err = basicStageRead(ctx, turn, id, update...)
	if err != nil {
		return value, err
	}
	if err = row.Scan(&token, &value.index, &present, &value.audioDescription, &value.audioVariant, &value.chroma, &value.fpsNumerator, &value.fpsDenominator, &value.frameMode, &value.interlace, &value.telecine, &value.doviRPU, &value.doviBase, &value.doviKnown, &value.doviEnhancement, &value.doviCM, &value.playbackFramesRevision, &value.packetPeakRevision); err != nil {
		return value, basicStageError(err)
	}
	if token != s.assetToken || present != 1 || value != next {
		return value, ErrConflict
	}
	return value, nil
}
