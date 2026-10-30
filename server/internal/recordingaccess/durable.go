package recordingaccess

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
)

// Durable checks the account/profile that owns a recording, not the expired
// interactive token used to create it. It reads only current local authority.
func (p Policy) Durable(ctx context.Context, tx *sql.Tx, owner livechannels.Owner, source, channel string) error {
	if tx == nil || !owner.Valid() {
		return livechannels.ErrDenied
	}
	if err := GrantedTx(ctx, tx, owner); err != nil {
		return err
	}
	localOwner, err := p.MemberTx(ctx, tx, owner)
	if err != nil {
		return err
	}
	var access string
	e := tx.QueryRowContext(ctx, `SELECT COALESCE(x.viewer_access,'owner-only') FROM live_source_identities s LEFT JOIN live_source_settings x ON x.source_id=s.id WHERE s.id=?`, source).Scan(&access)
	if errors.Is(e, sql.ErrNoRows) {
		return livechannels.ErrDenied
	}
	if e != nil {
		return e
	}
	if !localOwner && access != "server-members" {
		return livechannels.ErrDenied
	}
	return ctx.Err()
}

// MemberTx verifies current account/profile membership without granting recording permission.
func (p Policy) MemberTx(ctx context.Context, tx *sql.Tx, owner livechannels.Owner) (bool, error) {
	if tx == nil || !owner.Valid() {
		return false, livechannels.ErrDenied
	}
	who := identity.Principal{Viewer: identity.Viewer{Authority: owner.Authority, AccountID: owner.AccountID, ProfileID: owner.ProfileID}}
	localOwner := false
	if owner.Authority == "local" {
		var e error
		localOwner, e = localAccess(ctx, tx, p.Schema, who, "")
		if errors.Is(e, identity.ErrUnauthorized) {
			return false, livechannels.ErrDenied
		}
		if e != nil {
			return false, e
		}
		who.Role = "member"
		if localOwner {
			who.Role = "owner"
		}
	}
	if p.Cached != nil {
		if e := p.Cached.AllowedTxContext(ctx, who, "", tx); e != nil {
			if errors.Is(e, identity.ErrUnauthorized) {
				return false, livechannels.ErrDenied
			}
			return false, e
		}
	} else if owner.Authority != "local" {
		return false, livechannels.ErrDenied
	}
	return localOwner, nil
}

// RetainedCapture is continuation of already-started library media, never an
// admission grant. The DVR owner must first prove its exact active claim and
// recording/finalizing state; this policy only checks the exceptional creator.
func (p Policy) RetainedCapture(ctx context.Context, tx *sql.Tx, owner livechannels.Owner, source, channel string) error {
	if tx == nil || !owner.Valid() || owner.Authority != "local" {
		return livechannels.ErrDenied
	}
	var primary string
	var retired, wasPrimary bool
	err := tx.QueryRowContext(ctx, `SELECT a.profile_id,p.deleted,p.is_primary FROM direct_profiles p JOIN accounts a ON a.id=p.account_id WHERE p.id=? AND p.account_id=?`, owner.ProfileID, owner.AccountID).Scan(&primary, &retired, &wasPrimary)
	if errors.Is(err, sql.ErrNoRows) {
		return livechannels.ErrDenied
	}
	if err != nil {
		return err
	}
	if !retired || wasPrimary {
		return livechannels.ErrDenied
	}
	// Recheck account/member and cached authority through its current live main
	// profile. This does not inherit the main profile's recording grant or widen
	// the old child's source classification.
	main := livechannels.Owner{Authority: "local", AccountID: owner.AccountID, ProfileID: primary}
	if _, err = p.MemberTx(ctx, tx, main); err != nil {
		return err
	}
	var access string
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(x.viewer_access,'owner-only') FROM live_source_identities s LEFT JOIN live_source_settings x ON x.source_id=s.id WHERE s.id=?`, source).Scan(&access)
	if errors.Is(err, sql.ErrNoRows) {
		return livechannels.ErrDenied
	}
	if err != nil {
		return err
	}
	// A secondary profile could only have started on a server-members source.
	// A subsequent owner-only change therefore revokes this continuation too.
	if access != "server-members" {
		return livechannels.ErrDenied
	}
	return ctx.Err()
}
