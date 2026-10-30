package localonly

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

// WithOnlineAdmission carries one already captured epoch and the caller's
// item/provider fences through every provider subrequest. Each check opens a
// fresh read from the pool: a caller's carried snapshot may predate revocation.
func WithOnlineAdmission(ctx context.Context, db *sql.DB, epoch int64, rechecks ...func(context.Context, *sql.Tx) error) context.Context {
	return metadataprovider.WithNetworkAdmission(ctx, func(ctx context.Context) (func() error, error) {
		check := func() error {
			tx, close, err := dbwork.BeginRead(ctx, db)
			if err != nil {
				return err
			}
			defer close()
			if err = CheckOnline(ctx, tx, epoch); err != nil {
				return err
			}
			for _, recheck := range rechecks {
				if err = recheck(ctx, tx); err != nil {
					return err
				}
			}
			return nil
		}
		if err := check(); err != nil {
			return nil, err
		}
		return check, nil
	})
}

// ItemAdmission checks the live library of an existing item. Missing items are
// refused too: an old query must not acquire permission from an absent row.
func ItemAdmission(item string) func(context.Context, *sql.Tx) error {
	return func(ctx context.Context, tx *sql.Tx) error {
		var library string
		if err := tx.QueryRowContext(ctx, `SELECT cl.library_id FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id JOIN libraries l ON l.id=cl.library_id WHERE e.public_id=pid_blob(?) AND e.retired=0 AND cl.retired=0`, item).Scan(&library); err != nil {
			if err == sql.ErrNoRows {
				return Err
			}
			return err
		}
		local, err := Library(ctx, tx, library)
		if err != nil {
			return err
		}
		if local {
			return Err
		}
		return nil
	}
}
