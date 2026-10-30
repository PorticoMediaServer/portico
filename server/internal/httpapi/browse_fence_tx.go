package httpapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// The same domain as browseFence, resolved inside a queue capture transaction.
// restrictions already includes the account/profile intersection from that tx.
func (d Dependencies) browseFenceTx(ctx context.Context, tx *sql.Tx, p identity.Principal, restrictions identity.ContentRestrictions) (string, error) {
	var policy, restriction int64
	if p.Authority != "local" {
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT revision FROM policy WHERE server_id=?),0),COALESCE((SELECT revision FROM restrictions WHERE profile_id=?),0)`, d.Identity.ID(), p.ProfileID).Scan(&policy, &restriction); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d:%d:%s", presentationIdentity(p), p.Epoch, policy, restriction, catalog.RestrictionFence(restrictions))))), nil
}
