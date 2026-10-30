package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
)

func registerInventoryRead(registry *apikit.Registry, d Dependencies) {
	v1Route(registry, apikit.Metadata{Audience: apikit.Clients, Admission: laneAdminHeavy, ID: "get_library_inventory", Method: "GET", Path: "/v1/admin/libraries/{id}/inventory", Summary: "Read one page of library files, optionally locating one file by its asset id", Access: apikit.Admin, AdminScope: "libraries", Requires: apikit.ScopeLibrary, Lane: apikit.Default, Cost: apikit.PageSized, Status: 200, Query: []string{"sourceId", "assetId", "state", "cursor", "limit"}, QueryIntegers: []apikit.QueryInteger{{Name: "limit", Minimum: 0, Default: 40, Maximum: 40, EmptyIsDefault: true, Description: "Zero selects the default page size."}}, Errors: []string{"unauthorized", "not_permitted", "invalid_request", "not_found", "revision_mismatch", "internal"}}, func(ctx context.Context, r *http.Request, _ noBody) (catalog.InventoryPageResult, error) {
		clone := r.Clone(ctx)
		u := *r.URL
		clone.URL = &u
		clone.URL.RawQuery = ""
		q, err := d.adminRequest(clone, "resource")
		if err != nil {
			return catalog.InventoryPageResult{}, err
		}
		q.AssetID, q.Cursor = r.URL.Query().Get("assetId"), r.URL.Query().Get("cursor")
		if raw := r.URL.Query().Get("limit"); raw != "" {
			q.Limit, err = strconv.Atoi(raw)
			if err != nil {
				return catalog.InventoryPageResult{}, &apierror.Error{Code: "invalid_request"}
			}
		}
		out, err := d.Catalog.Inventory(ctx, q, r.URL.Query().Get("sourceId"), r.URL.Query().Get("state"))
		if errors.Is(err, catalog.ErrAdminQuery) {
			return out, &apierror.Error{Code: "invalid_request"}
		}
		if err != nil {
			return out, bulkError(err)
		}
		_, err = d.libraryAdministrationRead(r, r.PathValue("id"))
		return out, err
	})
}
