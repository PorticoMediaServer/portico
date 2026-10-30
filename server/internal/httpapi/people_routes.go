package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
	"strconv"
)

// peopleRoutes carries the person pages, bulk personal state and the library
// collection membership batch. Each of these is one request per screen action:
// no client-side fan-out, no per-row round trip.
func (d Dependencies) peopleRoutes(mux *http.ServeMux) {
	personalRate := &personalLimiter{}
	d.httpHandle(mux, "GET /v1/people", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		for key, values := range q {
			if len(values) != 1 || (key != "q" && key != "limit") {
				failure(w, catalog.ErrPersonQuery)
				return
			}
		}
		limit := 25
		if q.Has("limit") {
			var e error
			limit, e = strconv.Atoi(q.Get("limit"))
			if e != nil {
				failure(w, catalog.ErrPersonQuery)
				return
			}
		}
		p, libraries, fence, e := d.homeScope(r)
		if e != nil {
			failure(w, e)
			return
		}
		viewer, e := d.catalogViewer(r, p, libraries, fence)
		if e != nil {
			failure(w, e)
			return
		}
		out, e := d.Catalog.WithContext(r.Context()).People(viewer, d.Identity.ID(), q.Get("q"), limit)
		if e != nil {
			failure(w, e)
			return
		}
		d.savedResponse(w, r, fence, out)
	})
	d.projectedHandle(mux, "GET /v1/people/{id}", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		for key, values := range q {
			if len(values) != 1 || (key != "cursor" && key != "limit" && key != "role" && key != "department" && key != "kind") {
				failure(w, catalog.ErrPersonQuery)
				return
			}
		}
		limit := 40
		if q.Has("limit") {
			var e error
			limit, e = strconv.Atoi(q.Get("limit"))
			if e != nil {
				failure(w, catalog.ErrPersonQuery)
				return
			}
		}
		p, libraries, fence, e := d.homeScope(r)
		if e != nil {
			failure(w, e)
			return
		}
		viewer, e := d.catalogViewer(r, p, libraries, fence)
		if e != nil {
			failure(w, e)
			return
		}
		filter := q.Get("role")
		if q.Has("department") {
			if filter != "" || q.Has("kind") {
				failure(w, catalog.ErrPersonQuery)
				return
			}
			filter = "department:" + q.Get("department")
		}
		if q.Has("kind") {
			if filter != "" {
				failure(w, catalog.ErrPersonQuery)
				return
			}
			filter = "kind:" + q.Get("kind")
		}
		out, e := d.Catalog.WithContext(r.Context()).Person(viewer, d.Identity.ID(), r.PathValue("id"), filter, q.Get("cursor"), limit)
		if e != nil {
			failure(w, e)
			return
		}
		d.savedResponse(w, r, fence, out)
	})
	d.httpHandle(mux, "GET /v1/people/{id}/portrait", func(w http.ResponseWriter, r *http.Request) {
		p, err := d.principal(r)
		if err != nil {
			failure(w, err)
			return
		}
		if d.Metadata == nil {
			policyError(w, "metadata_unavailable")
			return
		}
		_, libraries, fence, err := d.homeScope(r)
		if err != nil {
			failure(w, err)
			return
		}
		viewer, err := d.catalogViewer(r, p, libraries, fence)
		if err != nil {
			failure(w, err)
			return
		}
		item, subject, err := d.Catalog.WithContext(r.Context()).PersonPortrait(viewer, r.PathValue("id"))
		if err != nil {
			failure(w, err)
			return
		}
		// Re-check the owning item, so a library revoked between the page read
		// and the image read cannot still serve its artwork.
		if err = d.itemAccess(r.Context(), p, item); err != nil {
			failure(w, err)
			return
		}
		f, mime, err := d.Metadata.EntityArtworkVariant(r.Context(), metadata.RepairTarget{Kind: "item", ID: item}, "portrait", subject, artworkSize(r), r.URL.Query().Get("v"))
		if err != nil {
			if artworkVersionFailure(w, err) {
				return
			}
			if errors.Is(err, metadata.ErrArtworkPending) {
				w.Header().Set("Retry-After", "5")
				w.Header().Set("Cache-Control", "no-store")
				policyError(w, "artwork_pending")
				return
			}
			failure(w, err)
			return
		}
		defer f.Close()
		serveArtwork(w, r, f, mime)
	})
	d.projectedHandle(mux, "PUT /v1/items/personal-state:batch", func(w http.ResponseWriter, r *http.Request) {
		p, libraries, fence, e := d.homeScope(r)
		if e != nil {
			failure(w, e)
			return
		}
		if !personalRate.allow(viewerScope(p)) {
			w.Header().Set("Retry-After", "60")
			write(w, 429, map[string]any{"error": map[string]any{"code": "rate_limited", "message": "Too many personal-state requests. Try again shortly.", "retryable": true}})
			return
		}
		var body catalog.PersonalBatchMutation
		if e = decode(w, r, &body); e != nil {
			failure(w, e)
			return
		}
		allowed := map[string]bool{}
		for _, id := range libraries {
			allowed[id] = true
		}
		out, e := d.Catalog.WithContext(r.Context()).SetPersonalBatch(p.Authority+":"+p.AccountID, identity.PersonalKey(p.Viewer), body, func(tx *sql.Tx, item string) error {
			ctx, err := dbwork.BindSnapshot(r.Context(), tx)
			if err != nil {
				return err
			}
			request := r.WithContext(ctx)
			var library, kind string
			var playable bool
			if err := tx.QueryRow(`SELECT cl.library_id,k.name,k.playable FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id JOIN catalog_kinds k ON k.id=e.kind WHERE e.public_id=pid_blob(?)`, item).Scan(&library, &kind, &playable); err != nil {
				return err
			}
			// SEC-02: a title the profile may not see (another library, or over
			// its rating ceiling or with a blocked label) is refused exactly as an
			// absent one, and nothing is written for it. The library check alone
			// let a restricted profile write personal state for hidden titles.
			if !allowed[library] {
				return sql.ErrNoRows
			}
			// A show, album or book (only ever "not interested", which the
			// catalogue enforces per field) is checked as the container it is.
			if !playable {
				viewer, err := d.catalogViewer(request, p, libraries, fence)
				if err != nil {
					return err
				}
				if err = d.Catalog.WithContext(ctx).VisibleEntity(ctx, viewer, kind, item); err != nil {
					return err
				}
				return d.resourceAuthorize(tx, p, library)
			}
			if err := d.restrictedItem(request, p, item); err != nil {
				if errors.Is(err, identity.ErrContentRestricted) || errors.Is(err, identity.ErrNotVisible) || errors.Is(err, sql.ErrNoRows) {
					return sql.ErrNoRows
				}
				return err
			}
			return d.resourceAuthorize(tx, p, library)
		})
		if e != nil {
			failure(w, e)
			return
		}
		out.ServerID = d.Identity.ID()
		out.ViewerFence = fence
		d.savedResponse(w, r, fence, out)
	})
	d.httpHandle(mux, "POST /v1/collections/{id}/items:batch", func(w http.ResponseWriter, r *http.Request) {
		if _, e := d.owner(r); e != nil {
			failure(w, e)
			return
		}
		var body struct {
			AddItemIDs    []string `json:"addItemIds"`
			RemoveItemIDs []string `json:"removeItemIds"`
		}
		if e := decode(w, r, &body); e != nil {
			failure(w, e)
			return
		}
		out, e := d.Catalog.SetCollectionItems(r.PathValue("id"), body.AddItemIDs, body.RemoveItemIDs)
		if e != nil {
			failure(w, e)
			return
		}
		write(w, 200, out)
	})
	// The owner's manual order of a library collection, moved the way a
	// playlist's is and fenced by the collection's revision.
	d.httpHandle(mux, "PUT /v1/collections/{id}/order", func(w http.ResponseWriter, r *http.Request) {
		if _, e := d.owner(r); e != nil {
			failure(w, e)
			return
		}
		var body collectionReorder
		if e := decode(w, r, &body); e != nil {
			failure(w, e)
			return
		}
		out, e := d.Catalog.WithContext(r.Context()).ReorderCollection(r.PathValue("id"), body.ExpectedRevision, body.ItemIDs, body.AfterItemID)
		if e != nil {
			failure(w, e)
			return
		}
		write(w, 200, out)
	})
}

// collectionReorder is PUT /v1/collections/{id}/order: the titles to move (at
// most 200) and, optionally, the title they go after ("" the front).
type collectionReorder struct {
	ExpectedRevision int64    `json:"expectedRevision"`
	ItemIDs          []string `json:"itemIds"`
	AfterItemID      *string  `json:"afterItemId,omitempty"`
}
