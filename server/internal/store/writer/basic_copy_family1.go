package writer

import (
	"context"
	"database/sql"
	"errors"
)

type basicStreamFactRow struct {
	revision, size, modifiedNS, detailVersion int64
	fingerprint                               string
}

// copyBasicFamily1 is the frozen copy pattern (W-BASIC-P4-DESIGN.md). Family 1
// is one asset_stream_facts row per asset, so its whole walk fits ONE turn and
// it has no visible checkpoint: entry cursor zero, exit cursor family 2 zero.
//
//  1. Cleanup: a canonical row the staged set does not name is removed, after
//     a child veto (no asset_streams row may remain; never a cascade).
//  2. Install: the staged row (read in this turn) is inserted, or replaces the
//     canonical row by revision CAS under the revision guard; the RETURNING row
//     must equal it exactly.
//  3. One cursor CAS rewrites every cursor column and bumps job_revision.
func copyBasicFamily1(ctx context.Context, turn *commandTurn, c basicCopyTurn) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if c.entry != (basicCopyCursor{}) {
		return ErrInvalid
	}
	if err = checkBasicCopyEntry(ctx, turn, c); err != nil {
		return err
	}
	s := c.source
	var staged basicStreamFactRow
	var ordinal int64
	row, err := basicStageRead(ctx, turn, basicCopy1Staged, s.changeID, s.assetToken)
	if err != nil {
		return err
	}
	err = row.Scan(&ordinal, &staged.revision, &staged.size, &staged.modifiedNS, &staged.fingerprint, &staged.detailVersion)
	hasStaged := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return basicStageError(err)
	}
	var canonical basicStreamFactRow
	row, err = basicStageRead(ctx, turn, basicCopy1Canonical, s.assetToken)
	if err != nil {
		return err
	}
	err = row.Scan(&canonical.revision, &canonical.size, &canonical.modifiedNS, &canonical.fingerprint, &canonical.detailVersion)
	hasCanonical := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return basicStageError(err)
	}
	if hasCanonical && !hasStaged {
		row, err = basicStageRead(ctx, turn, basicCopy1Children, s.assetToken)
		if err != nil {
			return err
		}
		var child int64
		if err = row.Scan(&child); err == nil {
			return ErrConflict // a surviving canonical child vetoes the delete
		} else if !errors.Is(err, sql.ErrNoRows) {
			return basicStageError(err)
		}
		row, err = basicStageRead(ctx, turn, basicCopy1Delete, s.assetToken, canonical.revision)
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
		// Installs the staged values read in THIS turn: a fresh INSERT, or a CAS
		// UPDATE keyed on the canonical revision this turn read (guard above).
		if hasCanonical {
			row, err = basicStageRead(ctx, turn, basicCopy1Update, staged.revision, staged.size, staged.modifiedNS, staged.fingerprint, staged.detailVersion, s.assetToken, canonical.revision)
		} else {
			row, err = basicStageRead(ctx, turn, basicCopy1Insert, s.assetToken, staged.revision, staged.size, staged.modifiedNS, staged.fingerprint, staged.detailVersion)
		}
		if err != nil {
			return err
		}
		var got basicStreamFactRow
		var token string
		if err = row.Scan(&token, &got.revision, &got.size, &got.modifiedNS, &got.fingerprint, &got.detailVersion); err != nil {
			return basicStageError(err)
		}
		if token != s.assetToken || got != staged {
			return ErrConflict
		}
	}
	return advanceBasicCopyCursor(ctx, turn, c, 2, basicCopyCursor{family: 2})
}
