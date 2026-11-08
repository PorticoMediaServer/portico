package writer

import (
	"context"
	"database/sql"
	"errors"
)

// One canonical asset_subtitle_facts row. It is one row per asset, so the whole
// family walk fits ONE turn and has no visible checkpoint.
type basicCopy6Fact struct {
	revision, size, modifiedNS, originUS, timingKnown int64
	status, fingerprint                               string
}

// copyBasicFamily6 mirrors the family-1 pattern for the scalar subtitle fact:
// the staged row replaces the canonical row under the legacy revision guard, an
// obsolete canonical row is deleted only when no asset_subtitles child survives,
// and one cursor CAS moves the change to family 7.
func copyBasicFamily6(ctx context.Context, turn *commandTurn, c basicCopyTurn) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if c.entry != (basicCopyCursor{family: 6}) {
		return ErrInvalid
	}
	if err = checkBasicCopyEntry(ctx, turn, c); err != nil {
		return err
	}
	s := c.source
	var staged basicCopy6Fact
	var ordinal int64
	row, err := basicStageRead(ctx, turn, basicCopy6Staged, s.changeID, s.assetToken)
	if err != nil {
		return err
	}
	err = row.Scan(&ordinal, &staged.revision, &staged.size, &staged.modifiedNS, &staged.originUS, &staged.timingKnown, &staged.status, &staged.fingerprint)
	hasStaged := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return basicStageError(err)
	}
	var canonical basicCopy6Fact
	row, err = basicStageRead(ctx, turn, basicCopy6Canonical, s.assetToken)
	if err != nil {
		return err
	}
	err = row.Scan(&canonical.revision, &canonical.size, &canonical.modifiedNS, &canonical.originUS, &canonical.timingKnown, &canonical.status, &canonical.fingerprint)
	hasCanonical := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return basicStageError(err)
	}
	if hasCanonical && !hasStaged {
		// A surviving asset_subtitles row forbids the delete; never a cascade.
		row, err = basicStageRead(ctx, turn, basicCopy6Children, s.assetToken)
		if err != nil {
			return err
		}
		var child string
		if err = row.Scan(&child); err == nil {
			return ErrConflict
		} else if !errors.Is(err, sql.ErrNoRows) {
			return basicStageError(err)
		}
		row, err = basicStageRead(ctx, turn, basicCopy6Delete, s.assetToken, canonical.revision)
		if err != nil {
			return err
		}
		var deleted string
		if err = row.Scan(&deleted); err != nil {
			return basicStageError(err)
		}
		if deleted != s.assetToken {
			return ErrConflict
		}
	}
	if hasStaged {
		if ordinal < 0 || (hasCanonical && staged.revision < canonical.revision) {
			return ErrConflict // a staged revision older than canonical is an invariant failure
		}
		if hasCanonical {
			row, err = basicStageRead(ctx, turn, basicCopy6Update, staged.revision, staged.size, staged.modifiedNS, staged.originUS, staged.timingKnown, staged.status, staged.fingerprint, s.assetToken, canonical.revision)
		} else {
			row, err = basicStageRead(ctx, turn, basicCopy6Insert, s.assetToken, staged.revision, staged.size, staged.modifiedNS, staged.originUS, staged.timingKnown, staged.status, staged.fingerprint)
		}
		if err != nil {
			return err
		}
		var got basicCopy6Fact
		var token string
		if err = row.Scan(&token, &got.revision, &got.size, &got.modifiedNS, &got.originUS, &got.timingKnown, &got.status, &got.fingerprint); err != nil {
			return basicStageError(err)
		}
		if token != s.assetToken || got != staged {
			return ErrConflict
		}
	}
	return advanceBasicCopyCursor(ctx, turn, c, 2, basicCopyCursor{family: 7})
}
