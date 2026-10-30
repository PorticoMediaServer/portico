package downloads

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// ProfileAllowsDownloads reads the current profile switch in the same
// transaction as preparation, grant and receipt authority.
func ProfileAllowsDownloads(tx *sql.Tx, viewer identity.Viewer) (bool, error) {
	restrictions, err := identity.RestrictionsForViewerTx(context.Background(), tx, viewer)
	if err != nil {
		return false, err
	}
	return restrictions.AllowDownloads, nil
}

// AccountDisabled reports an account the server has switched off. A disabled
// account keeps no offline authorization: its receipts stop revalidating on the
// next contact, which is the only moment a server gets to say so.
func AccountDisabled(tx *sql.Tx, viewer identity.Viewer) (bool, error) {
	if viewer.Authority != "local" {
		return false, identity.ErrUnauthorized
	}
	var disabled bool
	err := tx.QueryRow(`SELECT disabled FROM direct_memberships WHERE account_id=?`, viewer.AccountID).Scan(&disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	return disabled, err
}
