package httpapi

import (
	"database/sql"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
)

func (d Dependencies) tvdbRoutes(mux *http.ServeMux) {
	rate := &personalLimiter{}
	d.httpHandle(mux, "GET /v1/shows/{id}/metadata/tvdb", func(w http.ResponseWriter, r *http.Request) {
		if _, e := d.owner(r); e != nil {
			failure(w, e)
			return
		}
		out, e := d.Metadata.TVDBState(r.PathValue("id"))
		if e != nil {
			failure(w, e)
			return
		}
		out.LibraryID, e = d.Catalog.WithContext(r.Context()).LibraryForShow(out.ShowID)
		if e != nil {
			failure(w, e)
			return
		}
		if _, e = d.owner(r); e != nil {
			failure(w, e)
			return
		}
		_, _, fence, e := d.homeScope(r)
		if e != nil {
			failure(w, e)
			return
		}
		out.ServerID = d.Identity.ID()
		out.ViewerFence = fence
		write(w, 200, out)
	})
	d.httpHandle(mux, "PUT /v1/shows/{id}/metadata/tvdb", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.owner(r)
		if e != nil {
			failure(w, e)
			return
		}
		if !rate.allow(viewerScope(p)) {
			w.Header().Set("Retry-After", "60")
			write(w, 429, map[string]any{"error": map[string]any{"code": "rate_limited", "message": "Too many metadata selections. Try again shortly.", "retryable": true}})
			return
		}
		var body metadata.TVDBSelection
		if e = decodeLegacyRevision(w, r, &body); e != nil {
			failure(w, e)
			return
		}
		body.Actor = metadata.MBActor{Authority: p.Authority, AccountID: p.AccountID, ProfileID: p.ProfileID}
		e = d.Metadata.SelectTVDB(r.PathValue("id"), body, func(tx *sql.Tx) error { return d.resourceAuthorize(tx, p, "") })
		if e != nil {
			failure(w, e)
			return
		}
		write(w, 200, map[string]any{"showId": r.PathValue("id"), "status": "pending_episodes"})
	})
	d.httpHandle(mux, "POST /v1/shows/{id}/metadata/tvdb/retry", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.owner(r)
		if e != nil {
			failure(w, e)
			return
		}
		if !rate.allow(viewerScope(p)) {
			w.Header().Set("Retry-After", "60")
			write(w, 429, map[string]any{"error": map[string]any{"code": "rate_limited", "message": "Too many metadata selections. Try again shortly.", "retryable": true}})
			return
		}
		var body struct {
			ExpectedRevision int64 `json:"expectedRevision"`
		}
		if e = decodeLegacyRevision(w, r, &body); e != nil {
			failure(w, e)
			return
		}
		e = d.Metadata.RetryTVDB(r.PathValue("id"), body.ExpectedRevision, func(tx *sql.Tx) error { return d.resourceAuthorize(tx, p, "") })
		if e != nil {
			failure(w, e)
			return
		}
		write(w, 202, map[string]string{"showId": r.PathValue("id"), "status": "queued"})
	})
}
