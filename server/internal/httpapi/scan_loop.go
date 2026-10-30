package httpapi

import (
	"context"
	"errors"
	"net/http"
	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/ingestion"
	"strconv"
)

func scanLoopError(err error) error {
	var invalid *administration.ValidationError
	switch {
	case errors.Is(err, administration.ErrConflict):
		return &apierror.Error{Code: "revision_mismatch", Cause: err}
	case errors.Is(err, administration.ErrIdempotencyKeyReused):
		return &apierror.Error{Code: "idempotency_key_reused", Cause: err}
	case errors.As(err, &invalid):
		// A refused value is named, so the owner (or a script) knows which one.
		out := &apierror.Error{Code: "invalid_request", Cause: err}
		for _, name := range invalid.Fields {
			out.Fields = append(out.Fields, apierror.Field{Path: name, Code: "invalid", Message: "Check this value."})
		}
		return out
	case errors.Is(err, administration.ErrInput), errors.Is(err, catalog.ErrAdminQuery):
		return &apierror.Error{Code: "invalid_request", Cause: err}
	case errors.Is(err, administration.ErrDenied):
		return &apierror.Error{Code: "not_permitted", Cause: err}
	case errors.Is(err, administration.ErrNotFound):
		return &apierror.Error{Code: "not_found", Cause: err}
	}
	return err
}
func registerScanLoop(registry *apikit.Registry, d Dependencies) {
	failures := []string{"unauthorized", "not_permitted", "not_found", "invalid_request", "revision_mismatch", "idempotency_key_reused", "internal"}
	v1Route(registry, apikit.Metadata{Audience: apikit.Clients, Admission: laneAdmin, ID: "get_library_settings", Method: "GET", Path: "/v1/admin/libraries/{id}/settings", Summary: "Read a library's scanning and metadata settings", Access: apikit.Admin, AdminScope: "libraries", Requires: apikit.ScopeLibrary, Lane: apikit.Default, Cost: apikit.PageSized, Status: 200, Errors: failures}, func(ctx context.Context, r *http.Request, _ noBody) (administration.LibraryDocument, error) {
		p, err := d.libraryAdministrationRead(r, r.PathValue("id"))
		if err != nil {
			return administration.LibraryDocument{}, err
		}
		if d.Administration == nil {
			return administration.LibraryDocument{}, &apierror.Error{Code: "internal"}
		}
		out, err := d.Administration.LibrarySettingsFor(ctx, d.libraryAdministrationAuthority(p, r.PathValue("id")), r.PathValue("id"), d.librarySonicAuthority(p))
		if err != nil {
			return out, scanLoopError(err)
		}
		_, err = d.libraryAdministrationRead(r, r.PathValue("id"))
		return out, err
	})
	v1Route(registry, apikit.Metadata{Audience: apikit.Clients, Admission: laneAdmin, ID: "put_library_settings", Method: "PUT", Path: "/v1/admin/libraries/{id}/settings", Summary: "Save a library's scanning and metadata settings", Access: apikit.Admin, AdminScope: "libraries", Requires: apikit.ScopeLibrary, Lane: apikit.Default, Cost: apikit.Constant, Status: 200, BodyLimit: 64 << 10, Errors: failures}, func(ctx context.Context, r *http.Request, in administration.Change[administration.LibrarySettings]) (administration.LibraryDocument, error) {
		p, err := d.libraryAdministrationRead(r, r.PathValue("id"))
		if err != nil {
			return administration.LibraryDocument{}, err
		}
		if d.Administration == nil {
			return administration.LibraryDocument{}, &apierror.Error{Code: "internal"}
		}
		out, err := d.Administration.SaveLibrarySettings(ctx, d.libraryAdministrationAuthority(p, r.PathValue("id")), r.PathValue("id"), in, d.librarySonicAuthority(p))
		if err != nil {
			return out, scanLoopError(err)
		}
		_, err = d.libraryAdministrationRead(r, r.PathValue("id"))
		return out, err
	})
	v1Route(registry, apikit.Metadata{Audience: apikit.Clients, Admission: laneBrowsing, ID: "get_scan_issues", Method: "GET", Path: "/v1/admin/libraries/{id}/scans/{job}/issues", Summary: "List files and folders skipped during a scan", Access: apikit.Admin, AdminScope: "libraries", Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Cost: apikit.PageSized, Status: 200, Query: []string{"after", "limit"}, QueryIntegers: emptyLimitQuery(64, 128), Errors: failures}, func(ctx context.Context, r *http.Request, _ noBody) (ingestion.ScanIssues, error) {
		if _, err := d.libraryAdministrationRead(r, r.PathValue("id")); err != nil {
			return ingestion.ScanIssues{}, err
		}
		after, err := catalog.ParseInventorySequence(r.URL.Query().Get("after"))
		if err != nil {
			return ingestion.ScanIssues{}, scanLoopError(err)
		}
		limit := 64
		if raw := r.URL.Query().Get("limit"); raw != "" {
			limit, err = strconv.Atoi(raw)
			if err != nil || limit < 1 || limit > 128 {
				return ingestion.ScanIssues{}, &apierror.Error{Code: "invalid_request"}
			}
		}
		out, err := d.Ingestion.Issues(ctx, r.PathValue("id"), r.PathValue("job"), after, limit)
		if err != nil {
			return out, scanLoopError(err)
		}
		_, err = d.libraryAdministrationRead(r, r.PathValue("id"))
		return out, err
	})
	v1Route(registry, apikit.Metadata{Audience: apikit.Clients, Admission: laneAdmin, ID: "get_inventory_changes", Method: "GET", Path: "/v1/admin/libraries/{id}/sources/{source}/changes", Summary: "Read retained inventory changes and cursor expiry", Access: apikit.Admin, AdminScope: "libraries", Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Cost: apikit.PageSized, Status: 200, Query: []string{"after"}, Errors: failures}, func(ctx context.Context, r *http.Request, _ noBody) (catalog.InventoryChangesPage, error) {
		if _, err := d.libraryAdministrationRead(r, r.PathValue("id")); err != nil {
			return catalog.InventoryChangesPage{}, err
		}
		after, err := catalog.ParseInventorySequence(r.URL.Query().Get("after"))
		if err != nil {
			return catalog.InventoryChangesPage{}, scanLoopError(err)
		}
		out, err := d.Catalog.InventoryChangesPage(ctx, r.PathValue("id"), r.PathValue("source"), after)
		if err != nil {
			return out, scanLoopError(err)
		}
		_, err = d.libraryAdministrationRead(r, r.PathValue("id"))
		return out, err
	})

}
