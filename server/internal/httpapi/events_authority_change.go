package httpapi

import (
	"context"
	"net/http"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/eventfeed"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// A role change can end the reader's original capacity admission. Deliver only
// its own revision, then retire that transport; never adopt successor admission
// or flush an ordinary page using the old principal.
func (d Dependencies) retiringAuthorityPage(r *http.Request, original identity.Principal, after string) (eventfeed.Page, bool) {
	var page eventfeed.Page
	ctx := dbwork.WithClass(r.Context(), dbwork.ClassSecurityFence)
	err := dbwork.WithReadSnapshot(ctx, d.DB, func(ctx context.Context) error {
		fresh, err := d.strictPrincipalFor(r.WithContext(ctx))
		if err != nil {
			return err
		}
		if fresh.Hash != original.Hash || fresh.AccountID != original.AccountID || fresh.ProfileID != original.ProfileID || fresh.Authority != original.Authority || fresh.Epoch != original.Epoch || fresh.Grant != original.Grant {
			return identity.ErrUnauthorized
		}
		if fresh.Role == original.Role {
			return identity.ErrUnauthorized
		}
		page, err = d.Events.AuthoritySince(ctx, fresh, after)
		return err
	})
	return page, err == nil && len(page.Events) != 0
}

func (d Dependencies) eventAccountDisabled(ctx context.Context, original identity.Principal) bool {
	if original.Authority != "local" {
		return false
	}
	var disabled bool
	err := dbwork.QueryRow(dbwork.WithClass(ctx, dbwork.ClassSecurityFence), d.DB, `SELECT disabled FROM direct_memberships WHERE account_id=?`, original.AccountID).Scan(&disabled)
	return err == nil && disabled
}
