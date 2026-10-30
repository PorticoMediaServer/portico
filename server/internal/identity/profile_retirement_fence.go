package identity

import (
	"context"
	"database/sql"
)

// ProfileRetiredTx is an additional denial fence for already-authorized work
// captured before a profile was removed. It grants no authority for an unknown
// viewer; callers retain their ordinary principal, family and policy checks.
// The sanitized deleted row is retained for the owner's history attribution,
// so the denial also survives physical private-child cleanup.
func ProfileRetiredTx(ctx context.Context, tx *sql.Tx, v Viewer) (bool, error) {
	if v.Authority != "local" {
		return false, nil
	}
	var retired bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM direct_profiles WHERE account_id=? AND id=? AND deleted=1)`, v.AccountID, v.ProfileID).Scan(&retired)
	return retired, err
}
