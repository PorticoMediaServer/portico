package recordingaccess

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
)

// Grant is one profile's answer to Who can record. It is the profile's own
// Recordings switch (profile_restrictions.allow_dvr), so Who can record and the
// profile's settings never disagree; Revision is that restriction document's.
type Grant struct {
	Owner    livechannels.Owner `json:"owner"`
	Enabled  bool               `json:"enabled"`
	Revision int64              `json:"revision"`
}

// GrantedTx is the one recording permission: the profile's Recordings switch.
// There is no account-wide inheritance, and account role grants nothing beyond
// the server owner's main profile recording by default.
func GrantedTx(ctx context.Context, tx *sql.Tx, o livechannels.Owner) error {
	if !o.Valid() || o.Authority != "local" {
		return livechannels.ErrDenied
	}
	var deleted int
	err := tx.QueryRowContext(ctx, `SELECT deleted FROM direct_profiles WHERE account_id=? AND id=?`, o.AccountID, o.ProfileID).Scan(&deleted)
	if errors.Is(err, sql.ErrNoRows) || err == nil && deleted != 0 {
		return livechannels.ErrDenied
	}
	if err != nil {
		return err
	}
	restrictions, err := identity.RestrictionsTx(ctx, tx, o.ProfileID)
	if err != nil {
		return err
	}
	if !restrictions.AllowDVR {
		return livechannels.ErrDenied
	}
	return nil
}

// ReadGrantsTx lists the profiles that may record. Profiles that may not are
// every other current profile; the owner's recording-owners page carries each
// one's state and revision.
func ReadGrantsTx(ctx context.Context, tx *sql.Tx) ([]Grant, error) {
	out := []Grant{}
	rows, err := tx.QueryContext(ctx, `SELECT p.account_id,p.id,COALESCE(r.revision,1) FROM direct_profiles p JOIN direct_memberships m ON m.account_id=p.account_id LEFT JOIN profile_restrictions r ON r.profile_id=p.id
 WHERE p.deleted=0 AND m.disabled=0 AND `+identity.RecordingDefaultSQL+`=1 ORDER BY p.account_id,p.id`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		g := Grant{Owner: livechannels.Owner{Authority: "local"}, Enabled: true}
		if err = rows.Scan(&g.Owner.AccountID, &g.Owner.ProfileID, &g.Revision); err != nil {
			return out, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// SaveGrantTx switches one profile's Recordings at its restriction revision.
// The caller has proved server-owner authority and, to allow, current membership.
func SaveGrantTx(ctx context.Context, tx *sql.Tx, g Grant) (Grant, error) {
	if !g.Owner.Valid() || g.Owner.Authority != "local" || g.Revision < 1 {
		return g, livechannels.ErrInvalid
	}
	saved, err := identity.SetRecordingAllowedTx(ctx, tx, g.Owner.AccountID, g.Owner.ProfileID, g.Enabled, g.Revision)
	switch {
	case errors.Is(err, identity.ErrRestrictionChanged):
		return g, livechannels.ErrConflict
	case errors.Is(err, identity.ErrNotVisible):
		return g, livechannels.ErrInvalid
	case err != nil:
		return g, err
	}
	return Grant{Owner: g.Owner, Enabled: saved.AllowDVR, Revision: saved.Revision}, nil
}
