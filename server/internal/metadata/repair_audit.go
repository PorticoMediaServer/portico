package metadata

import (
	"context"
	"database/sql"
)

// OwnerTextRevision is installed on the existing catalog text editor at startup.
// Both snapshots and the legacy projection commit in the caller's transaction.
func (s *Service) OwnerTextRevision(ctx context.Context, tx *sql.Tx, item, actor string) (func() error, error) {
	target := RepairTarget{Kind: "item", ID: item}
	before, ent, err := readRepairSnapshot(ctx, tx, target)
	if err != nil {
		return nil, err
	}
	base, err := repairRevision(ctx, tx, target, before, ent)
	if err != nil {
		return nil, err
	}
	return func() error { return recordRepair(ctx, tx, target, before, base, "edit_text", actor) }, nil
}
