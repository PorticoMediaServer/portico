package writer

import (
	"context"
	"database/sql"
	"errors"
)

// One canonical asset_subtitles track. The canonical identity is
// (asset_id,origin,locator); id is the row that survives an install.
type basicCopy8Track struct {
	id, origin, locator                  string
	streamIndex                          int64
	sidecar                              sql.NullString
	format, language, title, reason      string
	isDefault, isForced, hearingImpaired int64
}

// copyBasicFamily8 installs the asset's subtitle tracks and then removes the
// canonical tracks the staged set no longer names. Phase 0 installs at most
// eight staged rows per turn (copy_id = last installed input_ordinal). Phase 1
// walks the canonical rows of the asset by id, carrying the last walked id in
// copy_key with copy_aux 1. A kept track keeps its canonical id; a sidecar
// reference resolves through the canonical subtitle_sidecars row by path.
func copyBasicFamily8(ctx context.Context, turn *commandTurn, c basicCopyTurn) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	switch {
	case c.entry.family != 8 || c.entry.key2 != "" || c.entry.key3 != "":
		return ErrInvalid
	case c.entry.phase == 0:
		if c.entry.id < 0 || c.entry.aux != 0 || c.entry.key != "" {
			return ErrInvalid
		}
	case c.entry.phase == 1:
		if c.entry.id != 0 || c.entry.aux > 1 || (c.entry.key == "") != (c.entry.aux == 0) {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	if err = checkBasicCopyEntry(ctx, turn, c); err != nil {
		return err
	}
	var next basicCopyCursor
	if c.entry.phase == 0 {
		next, err = installBasicCopyFamily8(ctx, turn, c)
	} else {
		next, err = cleanBasicCopyFamily8(ctx, turn, c)
	}
	if err != nil {
		return err
	}
	return advanceBasicCopyCursor(ctx, turn, c, 2, next)
}

func installBasicCopyFamily8(ctx context.Context, turn *commandTurn, c basicCopyTurn) (basicCopyCursor, error) {
	s := c.source
	last, processed := c.entry.id, 0
	// copy_id 0 means "nothing installed yet" and staged ordinals start at 0,
	// so the opening read is inclusive; every later read is a strict keyset.
	inclusive := c.entry.id == 0
	factChecked := false
	for n := 0; n < 8; n++ {
		step := basicCopy8StagedAfter
		if inclusive {
			step, inclusive = basicCopy8StagedFirst, false
		}
		row, err := basicStageRead(ctx, turn, step, s.changeID, last, s.assetToken)
		if err != nil {
			return basicCopyCursor{}, err
		}
		var ordinal int64
		var staged basicCopy8Track
		var asset string
		err = row.Scan(&ordinal, &staged.id, &asset, &staged.origin, &staged.locator, &staged.streamIndex,
			&staged.sidecar, &staged.format, &staged.language, &staged.title,
			&staged.isDefault, &staged.isForced, &staged.reason, &staged.hearingImpaired)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return basicCopyCursor{}, basicStageError(err)
		}
		if !factChecked {
			// A staged track requires the asset's canonical subtitle fact.
			row, err = basicStageRead(ctx, turn, basicCopy8Facts, s.assetToken)
			if err != nil {
				return basicCopyCursor{}, err
			}
			var fact string
			if err = row.Scan(&fact); err != nil {
				return basicCopyCursor{}, basicStageError(err)
			}
			if fact != s.assetToken {
				return basicCopyCursor{}, ErrConflict
			}
			factChecked = true
		}
		if ordinal < last || asset != s.assetToken || staged.id == "" || staged.sidecar.Valid && staged.sidecar.String == "" {
			return basicCopyCursor{}, ErrConflict
		}
		if staged.sidecar.Valid {
			row, err = basicStageRead(ctx, turn, basicCopy8SidecarPath, s.changeID, staged.sidecar.String)
			if err != nil {
				return basicCopyCursor{}, err
			}
			var path string
			if err = row.Scan(&path); err != nil {
				return basicCopyCursor{}, basicStageError(err)
			}
			row, err = basicStageRead(ctx, turn, basicCopy8Sidecar, path)
			if err != nil {
				return basicCopyCursor{}, err
			}
			var canonicalSidecar string
			if err = row.Scan(&canonicalSidecar); err != nil {
				return basicCopyCursor{}, basicStageError(err)
			}
			staged.sidecar = sql.NullString{String: canonicalSidecar, Valid: true}
		}
		var canonicalID, gotAsset string
		var got basicCopy8Track
		row, err = basicStageRead(ctx, turn, basicCopy8Canonical, s.assetToken, staged.origin, staged.locator)
		if err != nil {
			return basicCopyCursor{}, err
		}
		scanErr := row.Scan(&canonicalID, &got.streamIndex, &got.sidecar, &got.format, &got.language, &got.title,
			&got.isDefault, &got.isForced, &got.reason, &got.hearingImpaired)
		switch {
		case errors.Is(scanErr, sql.ErrNoRows):
			row, err = basicStageRead(ctx, turn, basicCopy8Insert, staged.id, s.assetToken, staged.origin, staged.locator,
				staged.streamIndex, nullableSidecar(staged.sidecar), staged.format, staged.language, staged.title,
				staged.isDefault, staged.isForced, staged.reason, staged.hearingImpaired)
			if err != nil {
				return basicCopyCursor{}, basicCopyInsertError(err)
			}
			if err = row.Scan(&got.id, &gotAsset, &got.origin, &got.locator, &got.streamIndex, &got.sidecar, &got.format, &got.language,
				&got.title, &got.isDefault, &got.isForced, &got.reason, &got.hearingImpaired); err != nil {
				return basicCopyCursor{}, basicCopyInsertError(err)
			}
			if got.id != staged.id {
				return basicCopyCursor{}, ErrConflict
			}
		case scanErr != nil:
			return basicCopyCursor{}, basicStageError(scanErr)
		default:
			row, err = basicStageRead(ctx, turn, basicCopy8Update, staged.streamIndex, nullableSidecar(staged.sidecar),
				staged.format, staged.language, staged.title, staged.isDefault, staged.isForced, staged.reason,
				staged.hearingImpaired, canonicalID, s.assetToken, staged.origin, staged.locator)
			if err != nil {
				return basicCopyCursor{}, err
			}
			if err = row.Scan(&got.id, &gotAsset, &got.origin, &got.locator, &got.streamIndex, &got.sidecar, &got.format, &got.language,
				&got.title, &got.isDefault, &got.isForced, &got.reason, &got.hearingImpaired); err != nil {
				return basicCopyCursor{}, basicStageError(err)
			}
			if got.id != canonicalID {
				return basicCopyCursor{}, ErrConflict
			}
		}
		if gotAsset != s.assetToken || got.origin != staged.origin || got.locator != staged.locator || got.streamIndex != staged.streamIndex ||
			got.sidecar != staged.sidecar || got.format != staged.format || got.language != staged.language ||
			got.title != staged.title || got.isDefault != staged.isDefault || got.isForced != staged.isForced ||
			got.reason != staged.reason || got.hearingImpaired != staged.hearingImpaired {
			return basicCopyCursor{}, ErrConflict
		}
		last, processed = ordinal, processed+1
	}
	if processed == 8 {
		row, err := basicStageRead(ctx, turn, basicCopy8PeekStaged, s.changeID, last, s.assetToken)
		if err != nil {
			return basicCopyCursor{}, err
		}
		var ordinal int64
		err = row.Scan(&ordinal)
		if err == nil {
			return basicCopyCursor{family: 8, phase: 0, id: last}, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return basicCopyCursor{}, basicStageError(err)
		}
	}
	return basicCopyCursor{family: 8, phase: 1}, nil
}

func cleanBasicCopyFamily8(ctx context.Context, turn *commandTurn, c basicCopyTurn) (basicCopyCursor, error) {
	s := c.source
	last, processed := c.entry.key, 0
	for n := 0; n < 8; n++ {
		row, err := basicStageRead(ctx, turn, basicCopy8Next, s.assetToken, last)
		if err != nil {
			return basicCopyCursor{}, err
		}
		var id, origin, locator string
		err = row.Scan(&id, &origin, &locator)
		if errors.Is(err, sql.ErrNoRows) {
			return basicCopyCursor{family: 9}, nil
		}
		if err != nil {
			return basicCopyCursor{}, basicStageError(err)
		}
		if id <= last {
			return basicCopyCursor{}, ErrConflict
		}
		row, err = basicStageRead(ctx, turn, basicCopy8StagedKey, s.changeID, s.assetToken, origin, locator)
		if err != nil {
			return basicCopyCursor{}, err
		}
		var stagedID string
		scanErr := row.Scan(&stagedID)
		switch {
		case scanErr == nil:
			// The staged set names this identity: the track survives.
		case errors.Is(scanErr, sql.ErrNoRows):
			row, err = basicStageRead(ctx, turn, basicCopy8Delete, id, s.assetToken)
			if err != nil {
				return basicCopyCursor{}, err
			}
			var deleted string
			if err = row.Scan(&deleted); err != nil {
				return basicCopyCursor{}, basicStageError(err)
			}
			if deleted != id {
				return basicCopyCursor{}, ErrConflict
			}
		default:
			return basicCopyCursor{}, basicStageError(scanErr)
		}
		last, processed = id, processed+1
	}
	if processed == 8 {
		row, err := basicStageRead(ctx, turn, basicCopy8PeekNext, s.assetToken, last)
		if err != nil {
			return basicCopyCursor{}, err
		}
		var id string
		err = row.Scan(&id)
		if err == nil {
			return basicCopyCursor{family: 8, phase: 1, aux: 1, key: last}, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return basicCopyCursor{}, basicStageError(err)
		}
	}
	return basicCopyCursor{family: 9}, nil
}

// nullableSidecar keeps a NULL sidecar_id a real SQL NULL.
func nullableSidecar(v sql.NullString) any {
	if !v.Valid {
		return nil
	}
	return v.String
}
