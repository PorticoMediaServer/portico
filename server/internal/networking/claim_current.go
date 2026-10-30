package networking

import (
	"context"
	"database/sql"
	"errors"
)

// InstalledIntent captures the exact current installed operation for a typed
// control-plane consumer. Secret access and later policy publication still
// recheck this capture; it is not a credential or a durable readiness promise.
func (s *SQLiteStore) InstalledIntent(ctx context.Context) (Intent, error) {
	gated, e := s.tx(ctx)
	if e != nil {
		return Intent{}, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	identity, e := readIdentity(ctx, tx)
	if e != nil {
		return Intent{}, e
	}
	if !identity.installed.Valid || !identity.active.Valid || identity.installed.String != identity.active.String {
		return Intent{}, ErrStale
	}
	v, e := loadIntentTx(ctx, tx, identity.installed.String)
	if e != nil {
		return Intent{}, e
	}
	if v.Stage != Installed {
		return Intent{}, ErrStale
	}
	current, e := s.positive(ctx, tx, v.Intent)
	if e != nil {
		return Intent{}, e
	}
	if e = ctx.Err(); e != nil {
		return Intent{}, e
	}
	return current.Intent, nil
}

// SetupClaimInstalledTx is a read-only readiness projection for root's local
// setup transaction. It neither mints Hosted authority nor performs a network
// request. A terminal claim or unacknowledged install cannot finish setup.
func SetupClaimInstalledTx(ctx context.Context, tx *sql.Tx) (bool, error) {
	_, active, installed, e := readCurrentIdentityTx(ctx, tx)
	if e != nil {
		return false, e
	}
	if !active.Valid || !installed.Valid || active.String != installed.String {
		return false, nil
	}
	current, e := loadIntentTx(ctx, tx, installed.String)
	if e != nil {
		return false, e
	}
	if current.Stage != Installed || !current.InstallationAcknowledged {
		return false, nil
	}
	if e = GuardInstalledClaim(ctx, tx, current.Intent); e != nil {
		if errors.Is(e, ErrStale) || errors.Is(e, ErrCancelled) {
			return false, nil
		}
		return false, e
	}
	return true, nil
}
