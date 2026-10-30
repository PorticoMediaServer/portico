package httpapi

import (
	"context"
	"net/http"
	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/administration"
)

func registerBackgroundPause(registry *apikit.Registry, d Dependencies) error {
	return apikit.Register(registry, apikit.Route[administration.BackgroundPauseChange, administration.MaintenanceDocument]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAdmin, ID: "post_admin_background_pause", Method: "POST", Path: "/v1/admin/maintenance/background-pause", Summary: "Pause background work for an hour or resume it", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Cost: apikit.Constant, BodyLimit: 4096, Status: 200, Errors: []string{"unauthorized", "not_permitted", "invalid_request", "revision_mismatch", "administration_unavailable", "internal"}}, Handler: func(ctx context.Context, r *http.Request, body administration.BackgroundPauseChange) (administration.MaintenanceDocument, error) {
		if _, err := d.ownerContext(ctx, r); err != nil {
			return administration.MaintenanceDocument{}, foundationAuthError(err)
		}
		p, _ := d.foundationCaller(r)
		if d.Administration == nil {
			return administration.MaintenanceDocument{}, &apierror.Error{Code: "internal"}
		}
		out, err := d.Administration.PauseBackground(ctx, d.administrationAuthority(p), body)
		if err != nil {
			return out, foundationAdministrationError(err)
		}
		return out, nil
	}})
}
