package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
	"strconv"
)

func resourceActor(p identity.Principal) catalog.ResourceActor {
	return catalog.ResourceActor{Authority: p.Authority, AccountID: p.AccountID, ProfileID: p.ProfileID}
}
func (d Dependencies) resourceAuthorize(tx *sql.Tx, p identity.Principal, library string) error {
	if p.Role == "owner" {
		if err := d.ownerAuthorityTx(context.Background(), tx, p); err != nil {
			return err
		}
	}
	if _, e := d.Identity.SessionFamilyTx(context.Background(), tx, p); e != nil {
		return e
	}
	return d.allowedLibraryLegacyTx(p, library, tx)
}
func (d Dependencies) playlistRoutes(mux *http.ServeMux) {
	rate := &personalLimiter{}
	d.httpHandle(mux, "GET /v1/playlists/{id}", func(w http.ResponseWriter, r *http.Request) {
		p, _, fence, e := d.homeScope(r)
		if e != nil {
			failure(w, e)
			return
		}
		out, e := d.Catalog.WithContext(r.Context()).Playlist(d.Identity.ID(), fence, r.PathValue("id"), resourceActor(p))
		if e != nil {
			failure(w, e)
			return
		}
		_, _, after, e := d.homeScope(r)
		if e != nil {
			failure(w, e)
			return
		}
		if fence != after {
			failure(w, catalog.ErrStaleContinuation)
			return
		}
		write(w, 200, out)
	})
	d.httpHandle(mux, "GET /v1/playlists/{id}/content", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		for k, v := range q {
			if len(v) != 1 || (k != "cursor" && k != "limit" && k != "sort" && k != "direction" && k != "filter") {
				failure(w, catalog.ErrCursor)
				return
			}
		}
		limit := 40
		if q.Has("limit") {
			var e error
			limit, e = strconv.Atoi(q.Get("limit"))
			if e != nil || limit < 1 || limit > 100 {
				failure(w, catalog.ErrCursor)
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
		var out catalog.ContentEnvelope
		e = d.compose(r, viewer.Restrictions, func(cat *catalog.Service) error {
			var readErr error
			out, readErr = cat.PlaylistContent(catalog.ContentRequest{Viewer: viewer, ServerID: d.Identity.ID(), Profile: identity.PersonalKey(p.Viewer), ViewerFence: fence, View: "playlist", EntityID: r.PathValue("id"), Limit: limit, Cursor: q.Get("cursor"), Sort: q.Get("sort"), Direction: q.Get("direction"), Filter: q.Get("filter")}, resourceActor(p), libraries)
			if readErr != nil {
				return readErr
			}
			return d.projectSavedActions(cat.Context(), p, viewer, cat, savedContentEntries(&out))
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
		write(w, 200, out)
	})
	d.httpHandle(mux, "GET /v1/playlists/{id}/share-candidates", func(w http.ResponseWriter, r *http.Request) {
		p, _, fence, e := d.homeScope(r)
		if e != nil {
			failure(w, e)
			return
		}
		q := r.URL.Query()
		for k, v := range q {
			if len(v) != 1 || (k != "cursor" && k != "limit") {
				failure(w, catalog.ErrCursor)
				return
			}
		}
		limit := 40
		if q.Has("limit") {
			limit, e = strconv.Atoi(q.Get("limit"))
			if e != nil || limit < 1 || limit > 100 {
				failure(w, catalog.ErrCursor)
				return
			}
		}
		out, e := d.Catalog.WithContext(r.Context()).PlaylistCandidates(d.Identity.ID(), fence, r.PathValue("id"), resourceActor(p), q.Get("cursor"), limit)
		if e != nil {
			failure(w, e)
			return
		}
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
	})
	for _, route := range []struct{ pattern, action string }{{"POST /v1/playlists", "create"}, {"PATCH /v1/playlists/{id}", "update"}, {"DELETE /v1/playlists/{id}", "delete"}, {"POST /v1/playlists/{id}/entries", "add"}, {"DELETE /v1/playlists/{id}/entries/{entryId}", "remove"}, {"PUT /v1/playlists/{id}/shares", "share"}, {"DELETE /v1/playlists/{id}/shares", "unshare"}} {
		d.httpHandle(mux, route.pattern, func(w http.ResponseWriter, r *http.Request) {
			p, _, _, e := d.homeScope(r)
			if e != nil {
				failure(w, e)
				return
			}
			if !rate.allow(viewerScope(p)) {
				w.Header().Set("Retry-After", "60")
				write(w, 429, map[string]any{"error": map[string]any{"code": "rate_limited", "message": "Too many playlist requests. Try again shortly.", "retryable": true}})
				return
			}
			var m catalog.PlaylistMutation
			decodeMutation := decodeSavedRevision
			if route.action == "create" {
				decodeMutation = decode
			}
			if e = decodeMutation(w, r, &m); e != nil {
				failure(w, e)
				return
			}
			out, e := d.Catalog.WithContext(r.Context()).MutatePlaylist(resourceActor(p), r.PathValue("id"), route.action, r.PathValue("entryId"), m, func(tx *sql.Tx) error {
				if e := d.resourceAuthorize(tx, p, ""); e != nil {
					return e
				}
				// Library access alone cannot admit a restricted title. Re-prove
				// each submitted leaf under the original actor in this accepting
				// transaction, including receipt replay, just as collection adds do.
				itemAuthority := func(item string) error {
					_, err := d.playbackAuthorityTx(r.Context(), tx, p, item)
					if errors.Is(err, sql.ErrNoRows) || errors.Is(err, identity.ErrNotVisible) || errors.Is(err, identity.ErrContentRestricted) || errors.Is(err, identity.ErrForbidden) {
						return sql.ErrNoRows
					}
					return err
				}
				for _, item := range m.ItemIDs {
					if e := itemAuthority(item); e != nil {
						return e
					}
				}
				if route.action == "add" && m.ItemID != "" {
					if e := itemAuthority(m.ItemID); e != nil {
						return e
					}
				}
				if route.action == "share" {
					return d.shareTargetTx(tx, m.Authority, m.AccountID, m.ProfileID)
				}
				return nil
			})
			if e != nil {
				failure(w, e)
				return
			}
			_, _, fence, e := d.homeScope(r)
			if e != nil {
				failure(w, e)
				return
			}
			out.Deleted, e = d.Catalog.WithContext(r.Context()).PlaylistReceiptStatus(out.PlaylistID, resourceActor(p))
			if _, _, protocol := usermutation.RequestFrom(r.Context()); protocol && errors.Is(e, sql.ErrNoRows) {
				out.Deleted = true
				e = nil
			}
			if e != nil {
				failure(w, e)
				return
			}
			out.ServerID = d.Identity.ID()
			out.ViewerFence = fence
			write(w, 200, out)
		})
	}
}
