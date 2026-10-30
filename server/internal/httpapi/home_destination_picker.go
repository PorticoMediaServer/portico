package httpapi

import (
	"context"
	"net/http"
	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"time"
)

func registerHomeDestinationPicker(registry *apikit.Registry, d Dependencies) {
	v1Route(registry, apikit.Metadata{Audience: apikit.Clients, Admission: laneBrowsing, ID: "post_home_destination_picker", Method: "POST", Path: "/v1/home/destination-picker", Summary: "Search one authorized playlist or collection destination window", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Cost: apikit.PageSized, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "invalid_cursor", "unauthorized", "not_found", "revision_mismatch", "internal"}},
		func(ctx context.Context, r *http.Request, in catalog.HomeDestinationPickerRequest) (catalog.HomeDestinationPickerPage, error) {
			ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			r = r.WithContext(ctx)
			out := catalog.HomeDestinationPickerPage{}
			if r.URL.RawQuery != "" {
				return out, catalog.ErrCursor
			}
			p, err := d.principal(r)
			if err != nil {
				return out, err
			}
			var fence string
			err = dbwork.WithReadSnapshot(ctx, d.DB, func(snapshot context.Context) error {
				request := r.WithContext(snapshot)
				libraries, bound, err := d.homeScopeFor(request, p)
				if err != nil {
					return err
				}
				fence = bound
				viewer, err := d.catalogViewer(request, p, libraries, bound)
				if err != nil {
					return err
				}
				out, err = d.Catalog.HomeDestinationPickerTx(snapshot, dbwork.Snapshot(snapshot), d.Identity.ID(), viewer, resourceActor(p), in)
				return err
			})
			if err != nil {
				return out, bulkError(err)
			}
			after, err := d.homeFenceAfter(r, p)
			if err != nil {
				return catalog.HomeDestinationPickerPage{}, err
			}
			if after != fence {
				return catalog.HomeDestinationPickerPage{}, bulkError(catalog.ErrStaleContinuation)
			}
			return out, nil
		})
}
