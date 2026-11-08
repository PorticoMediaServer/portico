package writer

import (
	"context"
	"database/sql"
	"errors"
)

// copyBasicFamily7 installs the shared subtitle_sidecars rows. They are keyed by
// path across assets, so this family is upsert-by-path only: a canonical row is
// never deleted and a kept row keeps its canonical id. The install is bounded to
// eight staged rows per turn and carries the last installed input_ordinal.
func copyBasicFamily7(ctx context.Context, turn *commandTurn, c basicCopyTurn) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if c.entry.family != 7 || c.entry.phase != 0 || c.entry.id < 0 || c.entry.aux != 0 ||
		c.entry.key != "" || c.entry.key2 != "" || c.entry.key3 != "" {
		return ErrInvalid
	}
	if err = checkBasicCopyEntry(ctx, turn, c); err != nil {
		return err
	}
	s := c.source
	last, processed := c.entry.id, 0
	// copy_id 0 means "nothing installed yet": the first staged ordinal is 0,
	// so the opening read is inclusive. Every later read is a strict keyset.
	inclusive := c.entry.id == 0
	for n := 0; n < 8; n++ {
		step := basicCopy7StagedAfter
		if inclusive {
			step, inclusive = basicCopy7StagedFirst, false
		}
		row, err := basicStageRead(ctx, turn, step, s.changeID, last)
		if err != nil {
			return err
		}
		var ordinal, size, modifiedNS int64
		var id, path, digest string
		err = row.Scan(&ordinal, &id, &path, &size, &modifiedNS, &digest)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return basicStageError(err)
		}
		if ordinal < last || id == "" || path == "" || digest == "" || size < 0 {
			return ErrConflict
		}
		var canonicalID, canonicalDigest string
		var canonicalSize, canonicalModifiedNS int64
		row, err = basicStageRead(ctx, turn, basicCopy7Canonical, path)
		if err != nil {
			return err
		}
		scanErr := row.Scan(&canonicalID, &canonicalSize, &canonicalModifiedNS, &canonicalDigest)
		var gotID, gotPath, gotDigest string
		var gotSize, gotModifiedNS int64
		switch {
		case errors.Is(scanErr, sql.ErrNoRows):
			row, err = basicStageRead(ctx, turn, basicCopy7Insert, id, path, size, modifiedNS, digest)
			if err != nil {
				return basicCopyInsertError(err)
			}
			if err = row.Scan(&gotID, &gotPath, &gotSize, &gotModifiedNS, &gotDigest); err != nil {
				return basicCopyInsertError(err)
			}
			if gotID != id {
				return ErrConflict
			}
		case scanErr != nil:
			return basicStageError(scanErr)
		default:
			row, err = basicStageRead(ctx, turn, basicCopy7Update, size, modifiedNS, digest, path, canonicalSize, canonicalModifiedNS, canonicalDigest)
			if err != nil {
				return err
			}
			if err = row.Scan(&gotID, &gotPath, &gotSize, &gotModifiedNS, &gotDigest); err != nil {
				return basicStageError(err)
			}
			if gotID != canonicalID {
				return ErrConflict
			}
		}
		if gotPath != path || gotSize != size || gotModifiedNS != modifiedNS || gotDigest != digest {
			return ErrConflict
		}
		last, processed = ordinal, processed+1
	}
	if processed == 8 {
		row, err := basicStageRead(ctx, turn, basicCopy7Peek, s.changeID, last)
		if err != nil {
			return err
		}
		var ordinal int64
		err = row.Scan(&ordinal)
		if err == nil {
			return advanceBasicCopyCursor(ctx, turn, c, 2, basicCopyCursor{family: 7, phase: 0, id: last})
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return basicStageError(err)
		}
	}
	return advanceBasicCopyCursor(ctx, turn, c, 2, basicCopyCursor{family: 8})
}
