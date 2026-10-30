package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func registerHomeDestinationReceipt(registry *apikit.Registry, d Dependencies) {
	v1Route(registry, apikit.Metadata{Audience: apikit.Clients, Admission: laneBrowsing, ID: "post_home_destination_receipt", Method: "POST", Path: "/v1/home/destination-receipt", Summary: "Read distinct durable membership outcomes for one authorized destination job set", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Cost: apikit.PageSized, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "invalid_cursor", "unauthorized", "not_found", "revision_mismatch", "internal"}},
		func(ctx context.Context, r *http.Request, in catalog.HomeDestinationReceiptRequest) (catalog.HomeDestinationReceipt, error) {
			ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			r = r.WithContext(ctx)
			out := catalog.HomeDestinationReceipt{}
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
				out, err = d.Catalog.HomeDestinationReceiptTx(snapshot, dbwork.Snapshot(snapshot), d.Identity.ID(), p, viewer, in, d.bulkAccess)
				return err
			})
			if err != nil {
				return out, bulkError(err)
			}
			after, err := d.homeFenceAfter(r, p)
			if err != nil {
				return catalog.HomeDestinationReceipt{}, err
			}
			if after != fence {
				return catalog.HomeDestinationReceipt{}, bulkError(catalog.ErrStaleContinuation)
			}
			return out, nil
		})
}
