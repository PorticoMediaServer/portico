package identity

import (
	"context"
	"database/sql"
)

// AuthorizeRecordingRootOwnerTx rechecks a saved local owner's durable identity
// at startup. It does not mint a session or substitute a Hosted account. The
// captured epoch prevents an earlier credential era from activating a request.
func (s *Service) AuthorizeRecordingRootOwnerTx(ctx context.Context, tx *sql.Tx, authority, account, profile string, epoch int) error {
	if s == nil || tx == nil || authority != "local" || account == "" || profile == "" || epoch < 1 { return ErrForbidden }
	var current bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM accounts a JOIN direct_memberships m ON m.account_id=a.id JOIN direct_profiles p ON p.account_id=a.id AND p.id=a.profile_id WHERE a.id=? AND a.epoch=? AND p.id=? AND p.is_primary=1 AND p.deleted=0 AND m.role='owner' AND m.disabled=0)`, account, epoch, profile).Scan(&current)
	if err != nil { return err }
	if !current { return ErrForbidden }
	return nil
}
