package networking

import (
	"context"
	"database/sql"
	"errors"
)

// GuardLocalOwner rechecks current owner membership or pending setup authority within the
// same SQLite writer transaction as claim progression. It is intentionally not
// the installed-credential guard: a password rotation invalidates pending
// consent but must not revoke the server's already installed Hosted credential.
// Root authenticates the initiating owner session before capturing LocalOwner;
// Store additionally matches operation revision, active key and reset generation.
func GuardLocalOwner(ctx context.Context, tx *sql.Tx, owner LocalOwner) error {
	if tx == nil || !validOwner(owner) {
		return ErrInvalid
	}
	if e := ctx.Err(); e != nil {
		return e
	}

	if owner.SetupOperationID != "" {
		var valid bool
		if e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM onboarding_state_v1 WHERE singleton=1 AND operation_id=? AND revision=? AND auth_mode='hosted' AND ready=0) AND NOT EXISTS(SELECT 1 FROM accounts)`, owner.SetupOperationID, owner.Epoch).Scan(&valid); e != nil {
			return e
		}
		if !valid {
			return ErrStale
		}
		return nil
	}
	var profile string
	var epoch int64
	e := tx.QueryRowContext(ctx, `SELECT a.profile_id,a.epoch FROM accounts a JOIN direct_memberships m ON m.account_id=a.id WHERE a.id=? AND m.role='owner' AND m.disabled=0`, owner.AccountID).Scan(&profile, &epoch)
	if errors.Is(e, sql.ErrNoRows) {
		return ErrStale
	}
	if e != nil {
		return e
	}
	if profile != owner.ProfileID || epoch != owner.Epoch {
		return ErrStale
	}
	return ctx.Err()
}
