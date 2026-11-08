package writer

import (
	"context"
	"database/sql"
	"errors"
)

// Family 4 (asset_chapter_facts): one fact row per asset, so the whole family
// fits ONE turn with no checkpoint: entry {family:4}, exit {family:5}.
//
//  1. Cleanup: a canonical fact the staged set does not name is removed after a
//     child veto (a surviving asset_chapters row refuses the delete; never a
//     cascade).
//  2. Install: the staged fact is inserted, or replaced by canonical-revision
//     CAS under the legacy guard that a staged revision may not move backwards.
//  3. One cursor CAS rewrites the cursor and bumps job_revision.
type basicCopy4Fact struct {
	revision, size, modifiedNS int64
	status, fingerprint        string
}

func copyBasicFamily4(ctx context.Context, turn *commandTurn, c basicCopyTurn) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if c.entry != (basicCopyCursor{family: 4}) {
		return ErrInvalid
	}
	if err = checkBasicCopyEntry(ctx, turn, c); err != nil {
		return err
	}
	s := c.source
	var staged basicCopy4Fact
	var ordinal int64
	row, err := basicStageRead(ctx, turn, basicCopy4Staged, s.changeID, s.assetToken)
	if err != nil {
		return err
	}
	err = row.Scan(&ordinal, &staged.revision, &staged.size, &staged.modifiedNS, &staged.status, &staged.fingerprint)
	hasStaged := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return basicStageError(err)
	}
	if hasStaged && (ordinal != 0 || staged.revision <= 0 || staged.size < 0 || staged.status == "" || staged.fingerprint == "") {
		return ErrConflict
	}
	var canonical basicCopy4Fact
	row, err = basicStageRead(ctx, turn, basicCopy4Canonical, s.assetToken)
	if err != nil {
		return err
	}
	err = row.Scan(&canonical.revision, &canonical.size, &canonical.modifiedNS, &canonical.status, &canonical.fingerprint)
	hasCanonical := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return basicStageError(err)
	}
	if hasCanonical && !hasStaged {
		row, err = basicStageRead(ctx, turn, basicCopy4Child, s.assetToken)
		if err != nil {
			return err
		}
		var child int64
		if err = row.Scan(&child); err == nil {
			return ErrConflict // a surviving canonical chapter vetoes the delete
		} else if !errors.Is(err, sql.ErrNoRows) {
			return basicStageError(err)
		}
		row, err = basicStageRead(ctx, turn, basicCopy4Delete, s.assetToken, canonical.revision)
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
		if staged.revision < canonical.revision {
			return ErrConflict // a staged revision older than canonical is an invariant failure
		}
		if hasCanonical {
			row, err = basicStageRead(ctx, turn, basicCopy4Update, staged.revision, staged.size, staged.modifiedNS, staged.status,
				staged.fingerprint, s.assetToken, canonical.revision)
		} else {
			row, err = basicStageRead(ctx, turn, basicCopy4Insert, s.assetToken, staged.revision, staged.size, staged.modifiedNS,
				staged.status, staged.fingerprint)
		}
		if err != nil {
			return err
		}
		var got basicCopy4Fact
		var token string
		if err = row.Scan(&token, &got.revision, &got.size, &got.modifiedNS, &got.status, &got.fingerprint); err != nil {
			return basicStageError(err)
		}
		if token != s.assetToken || got != staged {
			return ErrConflict
		}
	}
	return advanceBasicCopyCursor(ctx, turn, c, 2, basicCopyCursor{family: 5})
}
