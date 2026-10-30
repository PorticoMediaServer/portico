package httpapi

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func (d Dependencies) librarySonicAuthority(expected identity.Principal) administration.LibrarySonicAuthority {
	return func(ctx context.Context, tx *sql.Tx) (bool, error) {
		current, err := d.Identity.ReauthorizeTx(ctx, tx, expected)
		if err != nil {
			return false, err
		}
		if err = d.ownerAuthorityTx(ctx, tx, current); errors.Is(err, identity.ErrForbidden) {
			return false, nil
		}
		return err == nil, err
	}
}
