package metadata

import (
	"context"
	"database/sql"
)

// RepairRevisionTx returns the canonical metadata revision for t in the
// caller's transaction. It performs no authorization and no IO beyond the
// transaction it is given; callers must already hold the owner/source proof
// for the same target.
func RepairRevisionTx(ctx context.Context, tx *sql.Tx, t RepairTarget) (string, error) {
	if tx == nil {
		return "", ErrRepairInput
	}
	snap, ent, err := readRepairSnapshot(ctx, tx, t)
	if err != nil {
		return "", err
	}
	return repairRevision(ctx, tx, t, snap, ent)
}
