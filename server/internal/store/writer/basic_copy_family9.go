package writer

import (
	"context"
	"database/sql"
	"errors"
)

// copyBasicFamily9 installs the subtitle inventory evidence. Schema v1 folds the
// one-per-asset evidence into asset_subtitle_facts as
// has_subtitle_inventory_evidence/inventory_revision/inventory_evidence (there is
// no separate canonical table), so this family rewrites those columns of the
// asset's fact row. Install requires the fact row at the SAME revision the staged
// evidence names; a staged set that no longer carries evidence clears it. It is
// the last family: completion moves the change from copy phase 2 to phase 3 with
// a zeroed cursor.
func copyBasicFamily9(ctx context.Context, turn *commandTurn, c basicCopyTurn) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if c.entry != (basicCopyCursor{family: 9}) {
		return ErrInvalid
	}
	if err = checkBasicCopyEntry(ctx, turn, c); err != nil {
		return err
	}
	s := c.source
	var ordinal, revision int64
	var evidence string
	row, err := basicStageRead(ctx, turn, basicCopy9Staged, s.changeID, s.assetToken)
	if err != nil {
		return err
	}
	err = row.Scan(&ordinal, &revision, &evidence)
	hasStaged := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return basicStageError(err)
	}
	var factRevision, hasEvidence int64
	var inventoryRevision sql.NullInt64
	var inventoryEvidence sql.NullString
	row, err = basicStageRead(ctx, turn, basicCopy9Present, s.assetToken)
	if err != nil {
		return err
	}
	err = row.Scan(&factRevision, &hasEvidence, &inventoryRevision, &inventoryEvidence)
	hasFact := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return basicStageError(err)
	}
	if hasFact && (hasEvidence < 0 || hasEvidence > 1) {
		return ErrConflict
	}
	if hasStaged {
		if ordinal < 0 || revision <= 0 || evidence == "" {
			return ErrConflict
		}
		// The evidence revision is the fact revision the staged set names; an
		// absent or newer fact row is an invariant failure, never a silent drop.
		if !hasFact || factRevision != revision {
			return ErrConflict
		}
		switch {
		case hasEvidence == 0:
			if inventoryRevision.Valid || inventoryEvidence.Valid {
				return ErrConflict
			}
			row, err = basicStageRead(ctx, turn, basicCopy9Insert, revision, evidence, s.assetToken)
		case inventoryRevision.Valid && inventoryEvidence.Valid && inventoryRevision.Int64 == revision && inventoryEvidence.String == evidence:
			// Already exact; the staged row equals the canonical evidence.
			return advanceBasicCopyCursor(ctx, turn, c, 3, basicCopyCursor{})
		case inventoryRevision.Valid && inventoryEvidence.Valid:
			row, err = basicStageRead(ctx, turn, basicCopy9Update, revision, evidence, s.assetToken, inventoryRevision.Int64, inventoryEvidence.String)
		default:
			return ErrConflict
		}
		if err != nil {
			return err
		}
		var gotAsset string
		var gotHas int64
		var gotRevision sql.NullInt64
		var gotEvidence sql.NullString
		if err = row.Scan(&gotAsset, &gotHas, &gotRevision, &gotEvidence); err != nil {
			return basicStageError(err)
		}
		if gotAsset != s.assetToken || gotHas != 1 || !gotRevision.Valid || gotRevision.Int64 != revision ||
			!gotEvidence.Valid || gotEvidence.String != evidence {
			return ErrConflict
		}
	} else if hasFact && hasEvidence == 1 {
		if !inventoryRevision.Valid || !inventoryEvidence.Valid {
			return ErrConflict
		}
		row, err = basicStageRead(ctx, turn, basicCopy9Clear, int64(0), nil, nil, s.assetToken, inventoryRevision.Int64, inventoryEvidence.String)
		if err != nil {
			return err
		}
		var gotAsset string
		var gotHas int64
		var gotRevision sql.NullInt64
		var gotEvidence sql.NullString
		if err = row.Scan(&gotAsset, &gotHas, &gotRevision, &gotEvidence); err != nil {
			return basicStageError(err)
		}
		if gotAsset != s.assetToken || gotHas != 0 || gotRevision.Valid || gotEvidence.Valid {
			return ErrConflict
		}
	}
	return advanceBasicCopyCursor(ctx, turn, c, 3, basicCopyCursor{})
}
