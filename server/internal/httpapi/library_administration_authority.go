package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Library management rechecks both the session tier and live membership in the
// transaction doing the work. It never borrows owner authority for an admin.
func (d Dependencies) libraryAdministrationAuthorize(ctx context.Context, tx *sql.Tx, p identity.Principal, library string) error {
	if _, err := d.tierAuthorityTx(ctx, tx, p, identity.TierAdmin); err != nil {
		return err
	}
	// The owner keeps the pre-existing administrative view, including library
	// configuration belonging to private recordings. Admins use viewer visibility.
	if p.Role == identity.TierOwner {
		return nil
	}
	return d.allowedLibraryTx(ctx, p, library, tx)
}

func (d Dependencies) libraryAdministrationAuthority(p identity.Principal, libraries ...string) administration.Authorize {
	return func(ctx context.Context, tx *sql.Tx) error {
		if len(libraries) == 0 {
			return d.libraryAdministrationAuthorize(ctx, tx, p, "")
		}
		for _, library := range libraries {
			if err := d.libraryAdministrationAuthorize(ctx, tx, p, library); err != nil {
				return err
			}
		}
		return nil
	}
}

// Scope is resolved in the same snapshot as each listing, before its LIMIT and
// cursor are applied. Nil is the owner's existing unrestricted admin view;
// an empty non-nil slice is an admin with no currently visible libraries.
func (d Dependencies) libraryAdministrationScope(p identity.Principal) func(context.Context, *sql.Tx) ([]string, error) {
	return func(ctx context.Context, tx *sql.Tx) ([]string, error) {
		if err := d.libraryAdministrationAuthorize(ctx, tx, p, ""); err != nil {
			return nil, err
		}
		if p.Role == identity.TierOwner {
			return nil, nil
		}
		rows, err := tx.QueryContext(ctx, `SELECT id FROM libraries ORDER BY id`)
		if err != nil {
			return nil, err
		}
		candidates := []string{}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			candidates = append(candidates, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		allowed := []string{}
		for _, id := range candidates {
			err = d.allowedLibraryTx(ctx, p, id, tx)
			if errors.Is(err, identity.ErrNotVisible) {
				continue
			}
			if err != nil {
				return nil, err
			}
			allowed = append(allowed, id)
		}
		return allowed, nil
	}
}

func (d Dependencies) libraryAdministrationRead(r *http.Request, library string) (identity.Principal, error) {
	p, err := d.administrator(r)
	if err != nil {
		return p, err
	}
	err = dbwork.WithReadSnapshot(r.Context(), d.DB, func(ctx context.Context) error {
		return d.libraryAdministrationAuthorize(ctx, dbwork.Snapshot(ctx), p, library)
	})
	return p, err
}

func (d Dependencies) libraryAdministrationJobAuthorize(ctx context.Context, tx *sql.Tx, p identity.Principal, job string) error {
	var library string
	if err := tx.QueryRowContext(ctx, `SELECT library_id FROM jobs WHERE id=?`, job).Scan(&library); err != nil {
		return err
	}
	return d.libraryAdministrationAuthorize(ctx, tx, p, library)
}

func (d Dependencies) libraryAdministrationJobRead(r *http.Request, job string) (identity.Principal, error) {
	p, err := d.administrator(r)
	if err != nil {
		return p, err
	}
	err = dbwork.WithReadSnapshot(r.Context(), d.DB, func(ctx context.Context) error {
		return d.libraryAdministrationJobAuthorize(ctx, dbwork.Snapshot(ctx), p, job)
	})
	return p, err
}
