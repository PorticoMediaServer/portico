package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
	"strconv"
)

func savedPageQuery(r *http.Request, extra ...string) (int, error) {
	for k, v := range r.URL.Query() {
		valid := k == "cursor" || k == "limit"
		for _, key := range extra {
			valid = valid || k == key
		}
		if !valid || len(v) != 1 {
			return 0, catalog.ErrCursor
		}
	}
	limit := 40
	if r.URL.Query().Has("limit") {
		var e error
		limit, e = strconv.Atoi(r.URL.Query().Get("limit"))
		if e != nil || limit < 1 || limit > 100 {
			return 0, catalog.ErrCursor
		}
	}
	return limit, nil
}
func (d Dependencies) savedResponse(w http.ResponseWriter, r *http.Request, fence string, out any) {
	_, _, after, e := d.homeScope(r)
	if e != nil {
		failure(w, e)
		return
	}
	if after != fence {
		failure(w, catalog.ErrStaleContinuation)
		return
	}
	write(w, 200, out)
}
func (d Dependencies) shareTargetTx(tx *sql.Tx, authority, account, profile string) error {
	if authority == "local" {
		return identity.DirectShareTarget(tx, account, profile)
	}

	return requestinput.New("invalid share authority")
}
func (d Dependencies) savedResourceRoutes(mux *http.ServeMux) {
	d.httpHandle(mux, "GET /v1/me/saved/summary", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			failure(w, catalog.ErrCursor)
			return
		}
		p, libraries, fence, err := d.homeScope(r)
		if err != nil {
			failure(w, err)
			return
		}
		viewer, err := d.catalogViewer(r, p, libraries, fence)
		if err != nil {
			failure(w, err)
			return
		}
		var admit catalog.SavedGroupAdmission
		if identity.CanUsePersonalState(p) {
			admit = d.savedReadingGroupsAdmission(p, viewer)
		}
		out, err := d.Catalog.WithContext(r.Context()).SavedSummaryWithReadingGroups(d.Identity.ID(), viewer, resourceActor(p), admit)
		if err != nil {
			failure(w, err)
			return
		}
		d.savedResponse(w, r, fence, out)
	})
	for _, method := range []string{"GET", "DELETE"} {
		d.httpHandle(mux, method+" /v1/me/removed-titles/{id}", func(w http.ResponseWriter, r *http.Request) {
			p, libraries, fence, err := d.homeScope(r)
			if err != nil {
				failure(w, err)
				return
			}
			viewer, err := d.catalogViewer(r, p, libraries, fence)
			if err != nil {
				failure(w, err)
				return
			}
			if r.Method == "DELETE" {
				if err = d.Catalog.WithContext(r.Context()).DismissRemovedTitle(viewer, r.PathValue("id"), func(tx *sql.Tx) error { return d.resourceAuthorize(tx, p, "") }); err != nil {
					failure(w, err)
					return
				}
				d.savedResponse(w, r, fence, map[string]bool{"removed": true})
				return
			}
			out, err := d.Catalog.WithContext(r.Context()).RemovedTitle(viewer, r.PathValue("id"))
			if err != nil {
				failure(w, err)
				return
			}
			d.savedResponse(w, r, fence, out)
		})
	}

	rate := &personalLimiter{}
	d.httpHandle(mux, "GET /v1/saved-resources", func(w http.ResponseWriter, r *http.Request) {
		limit, e := savedPageQuery(r, "kind", "pinned", "shape")
		if e != nil {
			failure(w, e)
			return
		}
		q := r.URL.Query()
		// Pinned listings are returned in the viewer's arrangement; both spellings
		// of the flag are accepted so a hand-written client works either way.
		pinned := q.Get("pinned")
		if q.Has("pinned") && pinned != "true" && pinned != "false" && pinned != "1" && pinned != "0" {
			failure(w, catalog.ErrCursor)
			return
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
		_, request, e := d.homeRequest(r)
		if e != nil {
			failure(w, e)
			return
		}
		homeRows := []string{}
		for _, row := range request.RowOrder {
			hidden := false
			for _, id := range request.HiddenRowIDs {
				if id == row {
					hidden = true
					break
				}
			}
			if !hidden {
				homeRows = append(homeRows, row)
			}
		}
		out, e := d.Catalog.WithContext(r.Context()).SavedResourcesForViewer(d.Identity.ID(), viewer, resourceActor(p), q.Get("kind"), q.Get("cursor"), pinned == "true" || pinned == "1", limit, q.Get("shape"), homeRows)
		if e != nil {
			failure(w, e)
			return
		}
		d.savedResponse(w, r, fence, out)
	})
	d.httpHandle(mux, "GET /v1/saved-resources/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			failure(w, catalog.ErrCursor)
			return
		}
		p, _, fence, e := d.homeScope(r)
		if e != nil {
			failure(w, e)
			return
		}
		out, e := d.Catalog.WithContext(r.Context()).SavedResource(d.Identity.ID(), fence, r.PathValue("id"), resourceActor(p))
		if e != nil {
			failure(w, e)
			return
		}
		d.savedResponse(w, r, fence, out)
	})
	d.projectedHandle(mux, "GET /v1/saved-resources/{id}/content", func(w http.ResponseWriter, r *http.Request) {
		limit, e := savedPageQuery(r, "sort", "direction", "filter")
		if e != nil {
			failure(w, e)
			return
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
		var out catalog.SavedResourceContent
		var admit catalog.SavedGroupAdmission
		if identity.CanUsePersonalState(p) {
			admit = d.savedReadingGroupsAdmission(p, viewer)
		}
		e = d.compose(r, viewer.Restrictions, func(cat *catalog.Service) error {
			var readErr error
			out, readErr = cat.SavedResourceContent(viewer, d.Identity.ID(), fence, r.PathValue("id"), resourceActor(p), catalog.ResourceContentQuery{Sort: r.URL.Query().Get("sort"), Direction: r.URL.Query().Get("direction"), Filter: r.URL.Query().Get("filter")}, r.URL.Query().Get("cursor"), limit, admit)
			if readErr != nil {
				return readErr
			}
			entries := make([]*catalog.ContentEntry, 0, len(out.Entries))
			for i := range out.Entries {
				if !out.Entries[i].Hidden {
					entries = append(entries, out.Entries[i].Media)
				}
			}
			if out.Browse != nil {
				for i := range out.Browse.Entries {
					entries = append(entries, &out.Browse.Entries[i])
				}
			}
			return d.projectSavedActions(cat.Context(), p, viewer, cat, entries)
		})
		if e != nil {
			failure(w, e)
			return
		}
		after, e := d.homeActionFenceAfter(r, p)
		if e != nil {
			failure(w, e)
			return
		}
		if after != fence {
			failure(w, catalog.ErrStaleContinuation)
			return
		}
		d.savedResponse(w, r, fence, out)
	})
	d.httpHandle(mux, "GET /v1/saved-resources/{id}/share-candidates", func(w http.ResponseWriter, r *http.Request) {
		limit, e := savedPageQuery(r)
		if e != nil {
			failure(w, e)
			return
		}
		p, _, fence, e := d.homeScope(r)
		if e != nil {
			failure(w, e)
			return
		}
		out, e := d.Catalog.WithContext(r.Context()).ResourceCandidates(d.Identity.ID(), fence, r.PathValue("id"), resourceActor(p), r.URL.Query().Get("cursor"), limit)
		if e != nil {
			failure(w, e)
			return
		}
		d.savedResponse(w, r, fence, out)
	})
	for _, route := range []struct{ pattern, action string }{{"POST /v1/saved-resources", "create"}, {"PATCH /v1/saved-resources/{id}", "update"}, {"DELETE /v1/saved-resources/{id}", "delete"}, {"PUT /v1/saved-resources/{id}/entries", "entries"}, {"PUT /v1/saved-resources/{id}/order", "reorder"}, {"PUT /v1/saved-resources/{id}/shares", "share"}, {"DELETE /v1/saved-resources/{id}/shares", "unshare"}} {
		d.httpHandle(mux, route.pattern, func(w http.ResponseWriter, r *http.Request) {
			p, _, fence, e := d.homeScope(r)
			if e != nil {
				failure(w, e)
				return
			}
			if !rate.allow(viewerScope(p)) {
				w.Header().Set("Retry-After", "60")
				write(w, 429, map[string]any{"error": map[string]any{"code": "rate_limited", "message": "Too many saved resource changes. Try again shortly.", "retryable": true}})
				return
			}
			var m catalog.SavedResourceMutation
			if e = decodeSavedRevision(w, r, &m); e != nil {
				failure(w, e)
				return
			}
			// Membership reports per item. Hidden and absent titles have the
			// same outcome, so a saved list cannot reveal a restricted title.
			rejected := []catalog.EntryOutcome{}
			_, _, protocol := usermutation.RequestFrom(r.Context())
			if !protocol && route.action == "entries" && len(m.AddItemIDs) > 0 {
				keep := []string{}
				for _, id := range m.AddItemIDs {
					if err := d.savedResourceItemAccess(r.Context(), p, id); err != nil {
						if errors.Is(err, sql.ErrNoRows) || errors.Is(err, identity.ErrNotVisible) || errors.Is(err, identity.ErrContentRestricted) || errors.Is(err, identity.ErrForbidden) {
							rejected = append(rejected, catalog.EntryOutcome{ItemID: id, Code: "not_found"})
							continue
						}
						failure(w, err)
						return
					}
					keep = append(keep, id)
				}
				if len(keep) == 0 && len(m.RemoveEntryIDs) == 0 {
					write(w, 200, map[string]any{"serverId": d.Identity.ID(), "viewerFence": fence, "receipt": catalog.SavedResourceReceipt{OperationID: m.OperationID, ResourceID: r.PathValue("id"), Entries: &catalog.EntryOutcomes{Added: []string{}, Removed: []string{}, Unchanged: []string{}, Failed: rejected}}, "receiptLifetimeMs": 2592000000})
					return
				}
				m.AddItemIDs = keep
			}
			out, e := d.Catalog.WithContext(r.Context()).MutateSavedResource(resourceActor(p), r.PathValue("id"), route.action, m, func(tx *sql.Tx) error {
				if e := d.resourceAuthorize(tx, p, ""); e != nil {
					return e
				}
				if m.Definition != nil {
					if e := d.resourceAuthorize(tx, p, m.Definition.LibraryID); e != nil {
						return e
					}
				}
				for _, id := range m.AddItemIDs {
					if protocol {
						break
					}
					var library string
					if e := tx.QueryRow(`SELECT cl.library_id FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.public_id=pid_blob(?)`, id).Scan(&library); e != nil {
						return e
					}
					if e := d.resourceAuthorize(tx, p, library); e != nil {
						return e
					}
					if e := d.savedResourceItemAuthorityTx(r.Context(), tx, p, id); e != nil {
						return e
					}
				}
				if route.action == "share" {
					return d.shareTargetTx(tx, m.Authority, m.AccountID, m.ProfileID)
				}
				return nil
			}, func(tx *sql.Tx, id string) error {
				if !protocol {
					return nil
				}
				err := d.savedResourceItemAuthorityTx(r.Context(), tx, p, id)
				if errors.Is(err, identity.ErrNotVisible) || errors.Is(err, identity.ErrContentRestricted) || errors.Is(err, identity.ErrForbidden) {
					return sql.ErrNoRows
				}
				return err
			})
			if e != nil {
				failure(w, e)
				return
			}

			if len(rejected) > 0 {
				if out.Entries == nil {
					out.Entries = &catalog.EntryOutcomes{Added: []string{}, Removed: []string{}, Unchanged: []string{}, Failed: []catalog.EntryOutcome{}}
				}
				out.Entries.Failed = append(out.Entries.Failed, rejected...)
			}
			dead, revision, e := d.Catalog.WithContext(r.Context()).SavedResourceReceiptStatus(out.ResourceID, resourceActor(p))
			if _, _, protocol := usermutation.RequestFrom(r.Context()); protocol && errors.Is(e, sql.ErrNoRows) {
				dead = true
				revision = out.Revision
				e = nil
			}
			if e != nil {
				failure(w, e)
				return
			}
			d.savedResponse(w, r, fence, map[string]any{"serverId": d.Identity.ID(), "viewerFence": fence, "receipt": out, "current": map[string]any{"resourceId": out.ResourceID, "revision": revision, "deleted": dead}, "receiptLifetimeMs": 2592000000})
		})
	}
	d.httpHandle(mux, "PUT /v1/saved-pins/{kind}/{id}", func(w http.ResponseWriter, r *http.Request) {
		p, _, fence, e := d.homeScope(r)
		if e != nil {
			failure(w, e)
			return
		}
		var m catalog.PinMutation
		if e = decodeSavedRevision(w, r, &m); e != nil {
			failure(w, e)
			return
		}
		out, e := d.Catalog.WithContext(r.Context()).MutateSavedPin(resourceActor(p), r.PathValue("kind"), r.PathValue("id"), m, func(tx *sql.Tx) error { return d.resourceAuthorize(tx, p, "") })
		if e != nil {
			failure(w, e)
			return
		}
		d.savedResponse(w, r, fence, map[string]any{"serverId": d.Identity.ID(), "viewerFence": fence, "operationId": out.OperationID, "resourceId": out.ResourceID, "playlistId": out.PlaylistID, "revision": out.Revision, "pinRevision": out.PinRevision, "pinned": out.Pinned, "deleted": out.Deleted})
	})

	d.httpHandle(mux, "POST /v1/me/recommendations:reset", func(w http.ResponseWriter, r *http.Request) {
		p, _, fence, e := d.homeScope(r)
		if e != nil {
			failure(w, e)
			return
		}
		var m catalog.RecommendationsReset
		if e = decode(w, r, &m); e != nil {
			failure(w, e)
			return
		}
		out, e := d.Catalog.ResetRecommendations(identity.PersonalKey(p.Viewer), m, func(tx *sql.Tx) error { return d.resourceAuthorize(tx, p, "") })
		if e != nil {
			failure(w, e)
			return
		}
		d.savedResponse(w, r, fence, map[string]any{"serverId": d.Identity.ID(), "viewerFence": fence, "operationId": out.OperationID, "clearedNotInterested": out.ClearedNotInterested, "receiptLifetimeMs": 2592000000})
	})
	d.httpHandle(mux, "POST /v1/personal-history/actions", func(w http.ResponseWriter, r *http.Request) {
		p, _, fence, e := d.homeScope(r)
		if e != nil {
			failure(w, e)
			return
		}
		var m catalog.ActivityMutation
		if e = decodeSavedRevision(w, r, &m); e != nil {
			failure(w, e)
			return
		}
		out, e := d.Catalog.MutateActivity(identity.PersonalKey(p.Viewer), m, func(tx *sql.Tx) error { return d.resourceAuthorize(tx, p, "") })
		if e != nil {
			failure(w, e)
			return
		}
		d.savedResponse(w, r, fence, map[string]any{"serverId": d.Identity.ID(), "viewerFence": fence, "receipt": out, "receiptLifetimeMs": 2592000000})
	})
}

func (d Dependencies) servePersonalHistory(w http.ResponseWriter, r *http.Request) {
	limit, e := savedPageQuery(r, "period", "libraryId")
	if e != nil {
		failure(w, e)
		return
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
	out, e := d.Catalog.WithContext(r.Context()).History(d.Identity.ID(), fence, identity.PersonalKey(p.Viewer), r.URL.Query().Get("cursor"), r.URL.Query().Get("period"), r.URL.Query().Get("libraryId"), libraries, limit, viewer)
	if e != nil {
		failure(w, e)
		return
	}
	d.savedResponse(w, r, fence, out)
}
