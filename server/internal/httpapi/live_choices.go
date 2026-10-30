package httpapi

import (
	"context"
	"net/http"
	"strconv"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/administration"
)

func registerLiveChoices(registry *apikit.Registry, d Dependencies) {
	for _, kind := range []string{"groups", "channels"} {
		v1Route(registry, apikit.Metadata{Audience: apikit.Clients, Admission: laneAdmin, ID: "list_live_source_" + kind, Method: "GET", Path: "/v1/admin/live/sources/{id}/" + kind, Summary: "List source choices, including groups and channels excluded from the line-up", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Cost: apikit.PageSized, Status: 200, Query: []string{"cursor", "limit", "q"}, QueryIntegers: []apikit.QueryInteger{{Name: "limit", Minimum: 0, Default: 50, Maximum: 200, EmptyIsDefault: true, Description: "Zero selects the service default of 40."}}, Errors: []string{"unauthorized", "not_permitted", "not_found", "invalid_request", "invalid_cursor", "revision_mismatch", "administration_unavailable", "internal"}}, func(ctx context.Context, r *http.Request, _ noBody) (administration.LiveSourceChoicePage, error) {
			p, e := d.owner(r)
			if e != nil {
				return administration.LiveSourceChoicePage{}, e
			}
			if d.Administration == nil {
				return administration.LiveSourceChoicePage{}, &apierror.Error{Code: "administration_unavailable"}
			}
			limit := 50
			if value := r.URL.Query().Get("limit"); value != "" {
				var err error
				limit, err = strconv.Atoi(value)
				if err != nil {
					return administration.LiveSourceChoicePage{}, &apierror.Error{Code: "invalid_request"}
				}
			}
			out, e := d.Administration.SourceChoices(ctx, d.administrationAuthority(p), r.PathValue("id"), kind, r.URL.Query().Get("cursor"), r.URL.Query().Get("q"), limit)
			return out, versionDeletionError(e)
		})
	}
}
