package httpapi

import (
	"context"
	"errors"
	"net/http"
	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
)

func registerRouteFailureMemory(r *apikit.Registry, d Dependencies) {
	v1Route(r, apikit.Metadata{Audience: apikit.Clients, Admission: laneAdmin, ID: "list_playback_failure_memory", Method: "GET", Path: "/v1/admin/playback/route-failures", Summary: "Read active remembered playback failures", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Cost: apikit.PageSized, Status: 200, Errors: []string{"unauthorized", "not_permitted", "internal"}}, func(ctx context.Context, _ *http.Request, _ noBody) (playback.RouteFailurePage, error) {
		if d.Playback == nil {
			return playback.RouteFailurePage{}, errors.New("playback unavailable")
		}
		return d.Playback.RouteFailureMemory(ctx)
	})
	v1Route(r, apikit.Metadata{Audience: apikit.Clients, Admission: laneAdmin, ID: "clear_playback_failure_memory", Method: "DELETE", Path: "/v1/admin/playback/route-failures/{sourceId}", Summary: "Forget remembered failures for a media file", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Cost: apikit.Constant, Status: 204, Errors: []string{"unauthorized", "not_permitted", "invalid_request", "internal"}}, func(ctx context.Context, q *http.Request, _ noBody) (noBody, error) {
		if d.Playback == nil {
			return noBody{}, errors.New("playback unavailable")
		}
		err := d.Playback.ClearRouteFailureMemory(ctx, q.PathValue("sourceId"))
		if errors.Is(err, playback.ErrRouteFailure) {
			return noBody{}, &apierror.Error{Code: "invalid_request", Cause: err}
		}
		return noBody{}, err
	})
}
