package httpapi

import (
	"context"
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/contentaccess"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func (d Dependencies) missingLibraryAuthority(p identity.Principal, library string) catalog.MissingAuthority {
	return func(ctx context.Context, tx *sql.Tx) (catalog.MissingScope, error) {
		if err := d.libraryAdministrationAuthorize(ctx, tx, p, library); err != nil {
			return catalog.MissingScope{}, err
		}
		if p.Role == identity.TierOwner {
			return catalog.MissingScope{VisibilitySQL: "1"}, nil
		}
		libraries, err := d.libraryAdministrationScope(p)(ctx, tx)
		if err != nil {
			return catalog.MissingScope{}, err
		}
		predicate, args, err := contentaccess.VisibleItemsSQL(ctx, tx, p, "missing_candidate.id", func(id string) error {
			return d.allowedLibraryTx(ctx, p, id, tx)
		})
		return catalog.MissingScope{Libraries: libraries, VisibilitySQL: predicate, VisibilityArgs: args}, err
	}
}
