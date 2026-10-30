package recordingaccess

import (
	"context"
	"database/sql"
	"errors"

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
	var epoch int
	var role, raw string
	var disabled, primary, deleted bool
	var restriction sql.NullString
	e = tx.QueryRowContext(ctx, directRecordingPrincipalQuery, who.AccountID, who.ProfileID).Scan(&epoch, &role, &disabled, &raw, &primary, &restriction, &deleted)
	if errors.Is(e, sql.ErrNoRows) {
		return false, identity.ErrUnauthorized
	}
	if e != nil {
		return false, e
	}
	if disabled || deleted || who.Epoch > 0 && epoch != who.Epoch || role != "owner" && role != "admin" && role != "member" {
		return false, identity.ErrUnauthorized
	}
	if !directLibraryAllowed(role, raw, restriction, library) {
		// A library the profile is not given is hidden, not a failed sign-in.
		return false, identity.ErrNotVisible
	}
	// A child of the owner's account is still not an interactive server owner.
	return role == "owner" && primary, ctx.Err()
}
