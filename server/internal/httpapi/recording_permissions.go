package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/recordingaccess"
	"time"
)

func (d Dependencies) recordingPermissionRoutes(mux *http.ServeMux) {
	d.httpHandle(mux, "GET /v1/admin/dvr/recording-permissions", d.administrationHandler(30*time.Second, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		if r.URL.RawQuery != "" {
			return nil, administration.ErrInput
		}
		g, e := dbwork.BeginSnapshot(ctx, d.DB)
		if e != nil {
			return nil, e
		}
		defer g.Rollback()
		if e = d.administrationAuthority(p)(ctx, g.Tx()); e != nil {
			return nil, e
		}
		return recordingaccess.ReadGrantsTx(ctx, g.Tx())
	}))
	d.httpHandle(mux, "PUT /v1/admin/dvr/recording-permissions", d.administrationHandler(30*time.Second, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		var body recordingaccess.Grant
		if e := decode(w, r, &body); e != nil {
			return nil, administration.ErrInput
		}
		var out recordingaccess.Grant
		e := dbwork.WithWriteTxContext(ctx, d.DB, dbwork.ClassSecurityFence, func(ctx context.Context, tx *sql.Tx) error {
			if e := d.administrationAuthority(p)(ctx, tx); e != nil {
				return e
			}
			// Revocation remains possible after a member has left. Granting requires a
			// currently valid target; neither admin authority nor the DTO manufactures it.
			if body.Enabled {
				if _, e := (recordingaccess.Policy{Cached: d.Hosted}).MemberTx(ctx, tx, body.Owner); e != nil {
					return administration.ErrDenied
				}
			}
			var err error
			out, err = recordingaccess.SaveGrantTx(ctx, tx, body)
			return err
		})
		if errors.Is(e, livechannels.ErrConflict) {
			e = administration.ErrConflict
		}
		if errors.Is(e, livechannels.ErrInvalid) {
			e = administration.ErrInput
		}
		return out, e
	}))
}
