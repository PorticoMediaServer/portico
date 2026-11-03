package recordingaccess

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// These are the installed P10 tables, not a second identity store. This adapter
// also supports the pre-P10 single-profile baseline. A partially installed P10
// schema must never fall back to the more permissive legacy owner assumption.
const directSchemaQuery = `SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN('direct_profiles','direct_memberships')`
const directRecordingPrincipalQuery = `SELECT a.epoch,m.role,m.disabled,m.allowed_libraries,p.is_primary,p.allowed_libraries,p.deleted
 FROM accounts a JOIN direct_memberships m ON m.account_id=a.id
 JOIN direct_profiles p ON p.account_id=a.id WHERE a.id=? AND p.id=?`

var errDirectPolicyUnavailable = errors.New("direct recording policy is unavailable")

func localAccess(ctx context.Context, tx *sql.Tx, schema *Schema, who identity.Principal, library string) (bool, error) {
	if tx == nil || who.Authority != "local" {
		return false, identity.ErrUnauthorized
	}
	tables, e := schema.directTables(ctx, tx)
	if e != nil {
		return false, e
	}
	if tables == 0 {
		var epoch int
		e = tx.QueryRowContext(ctx, `SELECT epoch FROM accounts WHERE id=? AND profile_id=?`, who.AccountID, who.ProfileID).Scan(&epoch)
		if errors.Is(e, sql.ErrNoRows) {
			return false, identity.ErrUnauthorized
		}
		if e != nil {
			return false, e
		}
		if who.Epoch > 0 && epoch != who.Epoch {
			return false, identity.ErrUnauthorized
		}
		return true, ctx.Err()
	}
	if tables != 2 {
		return false, errDirectPolicyUnavailable
	}
	row, e := directPrincipalRow(ctx, tx, who)
	if errors.Is(e, sql.ErrNoRows) {
		return false, identity.ErrUnauthorized
	}
	if e != nil {
		return false, e
	}
	if row.disabled || row.deleted || who.Epoch > 0 && row.epoch != who.Epoch || row.role != "owner" && row.role != "admin" && row.role != "member" {
		return false, identity.ErrUnauthorized
	}
	if !directLibraryAllowed(row.role, row.raw, row.restriction, library) {
		// A library the profile is not given is hidden, not a failed sign-in.
		return false, identity.ErrNotVisible
	}
	// A child of the owner's account is still not an interactive server owner.
	return row.role == "owner" && row.primary, ctx.Err()
}

type directPrincipal struct {
	epoch                      int
	role, raw                  string
	disabled, primary, deleted bool
	restriction                sql.NullString
}

// directPrincipalRow reads who's membership row. It does not depend on the
// library asked about, so inside a request's read snapshot it is read once
// (dbwork.RememberForRequest).
func directPrincipalRow(ctx context.Context, tx *sql.Tx, who identity.Principal) (directPrincipal, error) {
	return dbwork.RememberForRequest(ctx, tx, "recordingaccess.principal\x00"+who.AccountID+"\x00"+who.ProfileID, nil, func() (directPrincipal, error) {
		var row directPrincipal
		err := tx.QueryRowContext(ctx, directRecordingPrincipalQuery, who.AccountID, who.ProfileID).Scan(&row.epoch, &row.role, &row.disabled, &row.raw, &row.primary, &row.restriction, &row.deleted)
		return row, err
	})
}
