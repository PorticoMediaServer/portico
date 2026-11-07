package writer

import (
	"context"
	"errors"
)

// Pinning changes copy_family from 0 to 1. The initial Seal operations require
// cursor 0, so every complete input family must be sealed before that change;
// otherwise a valid producer could strand its remaining unsealed families.
func checkBasicPinManifest(ctx context.Context, turn *commandTurn, source basicStreamFactStage) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	rows, err := basicRows(ctx, turn, basicAcceptFamilies, source.changeID, int64(10))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	n := int64(0)
	for rows.Next() {
		n++
		var family, witness, expected, staged, sealed int64
		if err = rows.Scan(&family, &witness, &expected, &staged, &sealed); err != nil {
			return err
		}
		limit := int64(1)
		if family >= 2 {
			limit = basicMediaFamilyLimit(family)
		}
		if n > 9 || family != n || witness != 0 || expected < 0 || expected > limit || staged != expected || sealed != 1 {
			return ErrConflict
		}
		if (family == 1 || family == 4 || family == 6 || family == 9) && expected != 1 {
			return ErrConflict
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if n != 9 {
		return ErrConflict
	}
	return nil
}
