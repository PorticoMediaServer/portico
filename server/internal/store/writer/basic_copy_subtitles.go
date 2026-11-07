package writer

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"
)

// The parent owns command custody, rollback and final availability.
type basicSubtitleSidecarCopyEffects struct {
	basicStreamCopyFence
	hadBefore bool
	before    basicSubtitleSidecar
}
type basicSubtitleCopyEffects struct {
	basicStreamCopyFence
	hadBefore bool
	before    basicSubtitle
}

// Admit both raw payloads completely before constructing native arguments.
func preflightBasicSubtitleCopy(in basicStreamCopyFence, next, before basicMediaRow) error {
	f, p := in.family, in.position
	if f == nil || !f.valid || (f.family != 7 && f.family != 8) || in.ordinal < 0 || in.ordinal >= int64(len(f.rows)) ||
		p.changeID != f.source.changeID || p.changeRevision <= 0 || p.phase != 2 || p.afterEntity <= 0 ||
		p.family < 0 || p.copyPhase < 0 || p.id < 0 || p.aux < 0 || p.aux > 1 || in.shapeID <= 0 || in.fileSizeRevision <= 0 || in.invalidation <= 0 {
		return ErrInvalid
	}
	if int64(len(f.rows)) > basicMediaFamilyLimit(f.family) {
		return ErrCap
	}
	s := f.source
	a, af, _, ab, e := basicMediaTextFields(s, next)
	if e != nil || af != f.family {
		return ErrInvalid
	}
	b, bf, _, bb, e := basicMediaTextFields(s, before)
	if e != nil || bf != f.family {
		return ErrInvalid
	}
	remaining := int64(maxCanonicalRequestBytes) - ab - bb
	if remaining < 0 {
		return ErrCap
	}
	source := [...]string{s.assetToken, s.runID, s.sourceID, s.incarnation, s.objectID, s.objectRevision, s.fingerprint, p.key, p.key2, p.key3}
	for _, texts := range [][]string{source[:], a[:], b[:]} {
		for _, v := range texts {
			if int64(len(v)) > remaining {
				return ErrCap
			}
			remaining -= int64(len(v))
		}
	}
	for _, r := range []basicMediaRow{next, before} {
		var path string
		switch v := r.(type) {
		case basicSubtitleSidecar:
			path = v.path
		case basicSubtitle:
			if v.origin == "sidecar" {
				path = v.locator
			}
		}
		if strings.IndexByte(path, 0) >= 0 {
			return ErrInvalid
		}
		if !utf8.ValidString(path) {
			return &PolicyError{Field: "basic_subtitle_path", Cause: ErrFilenameUnrepresentable}
		}
	}
	for _, texts := range [][]string{source[:], a[:], b[:]} {
		for _, v := range texts {
			if !utf8.ValidString(v) {
				return ErrInvalid
			}
		}
	}
	return nil
}

func installBasicSubtitleSidecar(ctx context.Context, turn *commandTurn, in basicSubtitleSidecarCopyEffects) (value basicSubtitleSidecar, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil || in.family == nil || in.family.family != 7 || in.ordinal < 0 || in.ordinal >= int64(len(in.family.rows)) {
		return value, ErrInvalid
	}
	next, ok := in.family.rows[in.ordinal].(basicSubtitleSidecar)
	if !ok {
		return value, ErrInvalid
	}
	if err = preflightBasicSubtitleCopy(in.basicStreamCopyFence, next, in.before); err != nil {
		return value, err
	}
	if !in.hadBefore && in.before != (basicSubtitleSidecar{}) {
		return value, ErrInvalid
	}
	if in.hadBefore && (in.before.id != next.id || in.before.path != next.path) {
		return value, ErrConflict
	}
	s := in.family.source
	if in.hadBefore {
		if _, _, _, _, err = basicMediaValues(s, in.ordinal, in.before); err != nil {
			return value, err
		}
	}
	if _, _, _, _, err = basicMediaValues(s, in.ordinal, next); err != nil {
		return value, err
	}
	if err = checkBasicStreamCopyFence(ctx, turn, in.basicStreamCopyFence); err != nil {
		return value, err
	}
	expected := []any{next.id, next.path, next.size, next.modifiedNS, next.digest}
	rows, err := basicRows(ctx, turn, basicCopySidecarStaged, s.changeID, in.ordinal)
	if err != nil {
		return value, err
	}
	if err = basicMediaExactRow(rows, append([]any{in.ordinal}, expected...)); err != nil {
		return value, err
	}
	rows, err = basicRows(ctx, turn, basicCopySidecarID, next.id)
	if err != nil {
		return value, err
	}
	var captured []any
	if in.hadBefore {
		captured = []any{in.before.id, in.before.path, in.before.size, in.before.modifiedNS, in.before.digest}
	}
	if err = basicSubtitleCapturedRow(rows, captured); err != nil {
		return value, err
	}
	rows, err = basicRows(ctx, turn, basicCopySidecarKey, next.path)
	if err != nil {
		return value, err
	}
	var key []any
	if in.hadBefore {
		key = []any{next.id}
	}
	if err = basicSubtitleCapturedRow(rows, key); err != nil {
		return value, err
	}
	id := basicCopySidecarInsert
	args := expected
	if in.hadBefore {
		id = basicCopySidecarUpdate
		args = append(append([]any{}, expected[2:]...), captured...)
	}
	rows, err = basicRows(ctx, turn, id, args...)
	if err != nil {
		return value, err
	}
	if err = basicMediaExactRow(rows, expected); err != nil {
		return value, err
	}
	return next, nil
}

func installBasicSubtitle(ctx context.Context, turn *commandTurn, in basicSubtitleCopyEffects) (value basicSubtitle, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil || in.family == nil || in.family.family != 8 || in.ordinal < 0 || in.ordinal >= int64(len(in.family.rows)) {
		return value, ErrInvalid
	}
	next, ok := in.family.rows[in.ordinal].(basicSubtitle)
	if !ok {
		return value, ErrInvalid
	}
	if err = preflightBasicSubtitleCopy(in.basicStreamCopyFence, next, in.before); err != nil {
		return value, err
	}
	if !in.hadBefore && in.before != (basicSubtitle{}) {
		return value, ErrInvalid
	}
	if in.hadBefore && (in.before.id != next.id || in.before.origin != next.origin || in.before.locator != next.locator) {
		return value, ErrConflict
	}
	s := in.family.source
	if in.hadBefore {
		if _, _, _, _, err = basicMediaValues(s, in.ordinal, in.before); err != nil {
			return value, err
		}
	}
	if _, _, _, _, err = basicMediaValues(s, in.ordinal, next); err != nil {
		return value, err
	}
	if err = checkBasicStreamCopyFence(ctx, turn, in.basicStreamCopyFence); err != nil {
		return value, err
	}
	var sidecar, beforeSidecar any
	if next.origin == "sidecar" {
		sidecar = next.sidecarID
	}
	if in.before.origin == "sidecar" {
		beforeSidecar = in.before.sidecarID
	}
	expected := []any{next.id, s.assetToken, next.origin, next.locator, next.streamIndex, sidecar, next.format, next.language, next.title, basicBool(next.isDefault), basicBool(next.isForced), next.reason, basicBool(next.hearingImpaired)}
	rows, err := basicRows(ctx, turn, basicCopySubtitleStaged, s.changeID, in.ordinal)
	if err != nil {
		return value, err
	}
	if err = basicMediaExactRow(rows, append([]any{in.ordinal}, expected...)); err != nil {
		return value, err
	}
	if next.origin == "sidecar" {
		rows, err = basicRows(ctx, turn, basicCopySubtitleSourceSidecar, s.changeID, next.sidecarID, next.locator)
		if err != nil {
			return value, err
		}
		if err = basicMediaExactRow(rows, []any{next.sidecarID, next.locator}); err != nil {
			return value, err
		}
	} else {
		rows, err = basicRows(ctx, turn, basicCopySubtitleSourceStream, s.assetToken, next.streamIndex)
		if err != nil {
			return value, err
		}
		if err = basicMediaExactRow(rows, []any{next.streamIndex}); err != nil {
			return value, err
		}
	}
	rows, err = basicRows(ctx, turn, basicCopySubtitleID, next.id)
	if err != nil {
		return value, err
	}
	var captured []any
	if in.hadBefore {
		captured = []any{in.before.id, s.assetToken, in.before.origin, in.before.locator, in.before.streamIndex, beforeSidecar, in.before.format, in.before.language, in.before.title, basicBool(in.before.isDefault), basicBool(in.before.isForced), in.before.reason, basicBool(in.before.hearingImpaired)}
	}
	if err = basicSubtitleCapturedRow(rows, captured); err != nil {
		return value, err
	}
	rows, err = basicRows(ctx, turn, basicCopySubtitleKey, s.assetToken, next.origin, next.locator)
	if err != nil {
		return value, err
	}
	var key []any
	if in.hadBefore {
		key = []any{next.id}
	}
	if err = basicSubtitleCapturedRow(rows, key); err != nil {
		return value, err
	}
	id := basicCopySubtitleInsert
	args := expected
	if in.hadBefore {
		id = basicCopySubtitleUpdate
		args = append(append([]any{}, expected[4:]...), captured...)
	}
	rows, err = basicRows(ctx, turn, id, args...)
	if err != nil {
		return value, err
	}
	if err = basicMediaExactRow(rows, expected); err != nil {
		return value, err
	}
	return next, nil
}

// A missing before image requires zero rows, including the alternate unique key.
func basicSubtitleCapturedRow(rows *commandRows, want []any) (err error) {
	if want != nil {
		return basicMediaExactRow(rows, want)
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	if rows.Next() {
		return ErrConflict
	}
	return rows.Err()
}
