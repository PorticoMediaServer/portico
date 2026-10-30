// Package recordingaccess composes private recording ownership with the current
// server-local policy projection. It never contacts Hosted Services.
package recordingaccess

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

type CachedPolicy interface {
	AllowedTxContext(context.Context, identity.Principal, string, *sql.Tx) error
}

type Policy struct {
	Cached CachedPolicy
	// Schema caches the one-off `sqlite_master` probe for the direct identity
	// tables. Nil keeps the original behaviour of asking on every call.
	Schema *Schema
}

func (p Policy) AllowedTxContext(ctx context.Context, who identity.Principal, library string, tx *sql.Tx) error {
	if tx == nil || who.Authority != "local" {
		return identity.ErrUnauthorized
	}
	target := library
	if library != "" {
		var authority, account, profile string
		e := tx.QueryRowContext(ctx, `SELECT authority,account_id,profile_id FROM dvr_private_libraries WHERE library_id=?`, library).Scan(&authority, &account, &profile)
		if e == nil {
			if authority != who.Authority || account != who.AccountID || profile != who.ProfileID {
				return identity.ErrNotVisible // another profile's recordings
			}
			// P8: the profile's Recordings switch withholds its own recordings
			// library entirely, so browse, search, home and guides never see it.
			restrictions, e := identity.RestrictionsForViewerTx(ctx, tx, who.Viewer)
			if e != nil {
				return e
			}
			if !restrictions.AllowDVR {
				return identity.ErrNotVisible
			}
			// The artifact is owned by this profile, not by the original shared library.
			// Current server membership/restriction revocation must still be valid.
			target = ""
		} else if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
	}
	if who.Authority == "local" {
		if _, e := localAccess(ctx, tx, p.Schema, who, target); e != nil {
			return e
		}
	}
	if p.Cached != nil {
		return p.Cached.AllowedTxContext(ctx, who, target, tx)
	}
	if who.Authority != "local" {
		return identity.ErrUnauthorized
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return identity.DirectAllowed(tx, who, target)
}

func (p Policy) Allowed(ctx context.Context, db *sql.DB, who identity.Principal, library string) error {
	if db == nil {
		return identity.ErrUnauthorized
	}
	gated, e := dbwork.BeginSnapshot(ctx, db)
	if e != nil {
		return e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if e = p.AllowedTxContext(ctx, who, library, tx); e != nil {
		return e
	}
	return gated.Commit()
}

// AllowedSet answers the same question as Allowed for several libraries at once,
// inside one read transaction.
//
// The decision for a library depends only on the principal and that library, so
// asking it N times is correct — it was the *transaction* per question that was
// wrong. A listing of 100 items spans a handful of distinct libraries and was
// opening a hundred read transactions to filter them; this opens one, and the
// caller filters in Go against the answer.
//
// A library the caller may not see maps to false. Any other failure is returned
// as an error, so a database problem is never quietly read as a denial.
func (p Policy) AllowedSet(ctx context.Context, db *sql.DB, who identity.Principal, libraries []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(libraries) == 0 {
		return out, nil
	}
	if db == nil {
		return nil, identity.ErrUnauthorized
	}
	gated, e := dbwork.BeginSnapshot(ctx, db)
	if e != nil {
		return nil, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	for _, library := range libraries {
		if _, decided := out[library]; decided {
			continue
		}
		switch err := p.AllowedTxContext(ctx, who, library, tx); {
		case err == nil:
			out[library] = true
		case errors.Is(err, identity.ErrUnauthorized):
			out[library] = false
		default:
			return nil, err
		}
	}
	if e = gated.Commit(); e != nil {
		return nil, e
	}
	return out, nil
}
