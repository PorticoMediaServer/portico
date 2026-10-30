package preparedmedia

import (
	"context"
	"database/sql"
)

// CancelForRetirementTx is the narrow prepared-media hook used by the
// catalogue's SeparateFolder retirement units. It performs only the existing
// cancelTx transition: a queued job becomes cancelled, a running job becomes
// cancelling. It selects no job, deletes no version and touches no session pin.
// The caller registers the existing post-commit worker Interrupt itself.
func CancelForRetirementTx(ctx context.Context, tx *sql.Tx, id string) error {
	return cancelTx(ctx, tx, id)
}
