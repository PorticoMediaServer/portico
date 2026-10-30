package httpapi

import (
	"context"
	"database/sql"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// ownerAuthorityTx uses the current token family and the server's installed
// membership policy. A Hosted token's role alone never grants administration.
func (d Dependencies) ownerAuthorityTx(ctx context.Context, tx *sql.Tx, p identity.Principal) error {
	if d.Identity == nil || tx == nil {
		return identity.ErrUnauthorized
	}
	if p.Role != "owner" {
		// A valid session that is not the owner's: refused, still signed in.
		// The live session is checked first so a revoked one still answers 401.
		if _, err := d.Identity.SessionFamilyTx(ctx, tx, p); err != nil {
			return err
		}
		return identity.ErrForbidden
	}
	if _, err := d.Identity.SessionFamilyTx(ctx, tx, p); err != nil {
		return err
	}
	if err := d.Identity.RequireMainProfileTx(ctx, tx, p); err != nil {
		return err
	}
	switch p.Authority {
	case "local":
		return nil // SessionFamilyTx validates the live direct account/profile.
	}
	return identity.ErrForbidden
}

func (d Dependencies) ownerContext(ctx context.Context, r *http.Request) (identity.Principal, error) {
	headers := r.Header.Values("Authorization")
	if d.Identity == nil || d.DB == nil || len(headers) != 1 || !strings.HasPrefix(headers[0], "Bearer ") {
		return identity.Principal{}, identity.ErrUnauthorized
	}
	token := strings.TrimPrefix(headers[0], "Bearer ")
	p, err := d.Identity.AuthenticateContext(ctx, token)
	if err != nil {
		return p, err
	}
	gated, err := dbwork.BeginSnapshot(ctx, d.DB)
	if err != nil {
		return p, err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if err = d.ownerAuthorityTx(ctx, tx, p); err == nil {
		d.admission.rememberCredential(token, p)
	}
	return p, err
}

// Bootstrap/recovery and local Quick Connect must never substitute a Hosted
// account for the independently managed local owner.
func (d Dependencies) localOwner(r *http.Request) (identity.Principal, error) {
	p, err := d.ownerContext(r.Context(), r)
	if err == nil && p.Authority != "local" {
		err = identity.ErrForbidden
	}
	return p, err
}
