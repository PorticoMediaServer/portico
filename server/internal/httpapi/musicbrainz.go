package httpapi

import (
	"database/sql"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
)

func (d Dependencies) musicBrainzRoutes(mux *http.ServeMux) {
	rate := &personalLimiter{}
	d.httpHandle(mux, "POST /v1/items/{id}/metadata/local-audio/policy", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.owner(r)
		if e != nil {
			failure(w, e)
			return
		}
		if !rate.allow(viewerScope(p)) {
			w.Header().Set("Retry-After", "60")
			write(w, 429, map[string]any{"error": map[string]any{"code": "rate_limited", "message": "Too many metadata changes.", "retryable": true}})
			return
		}
		var body metadata.LocalAudioPolicyChange
		if e = decodeLegacyRevision(w, r, &body); e == nil {
			e = d.Metadata.UpdateLocalBookPolicy(r.PathValue("id"), body, func(tx *sql.Tx) error { return d.resourceAuthorize(tx, p, "") })
		}
		if e != nil {
			failure(w, e)
			return
		}
		write(w, 200, map[string]string{"entityId": r.PathValue("id"), "status": "saved"})
	})
	for _, route := range []struct{ path, kind string }{{"/v1/albums/{id}/metadata/musicbrainz", "album"}, {"/v1/items/{id}/metadata/musicbrainz", "song"}} {
		d.httpHandle(mux, "GET "+route.path, func(w http.ResponseWriter, r *http.Request) {
			if _, e := d.owner(r); e != nil {
				failure(w, e)
				return
			}
			out, e := d.Metadata.MusicBrainzState(route.kind, r.PathValue("id"))
			if e != nil {
				failure(w, e)
				return
			}
			if route.kind == "album" {
				e = dbwork.QueryRow(r.Context(), d.DB, `SELECT cl.library_id FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.public_id=pid_blob(?) AND e.kind=6`, out.EntityID).Scan(&out.LibraryID)
			} else {
				out.LibraryID, e = d.Catalog.WithContext(r.Context()).LibraryForItem(out.EntityID)
			}
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
		for _, operation := range []struct{ method, suffix string }{{"PUT", ""}, {"POST", "/retry"}, {"POST", "/search"}, {"POST", "/policy"}} {
			d.httpHandle(mux, operation.method+" "+route.path+operation.suffix, func(w http.ResponseWriter, r *http.Request) {
				p, e := d.owner(r)
				if e != nil {
					failure(w, e)
					return
				}
				if !rate.allow(viewerScope(p)) {
					w.Header().Set("Retry-After", "60")
					write(w, 429, map[string]any{"error": map[string]any{"code": "rate_limited", "message": "Too many metadata selections.", "retryable": true}})
					return
				}
				authorize := func(tx *sql.Tx) error { return d.resourceAuthorize(tx, p, "") }
				if operation.suffix == "/policy" {
					var body metadata.MusicPolicyChange
					if e = decodeLegacyRevision(w, r, &body); e == nil {
						e = d.Metadata.UpdateMusicPolicy(route.kind, r.PathValue("id"), body, authorize)
					}
				} else if operation.method == "PUT" {
					var body metadata.MBSelection
					if e = decodeLegacyRevision(w, r, &body); e == nil {
						body.Actor = metadata.MBActor{Authority: p.Authority, AccountID: p.AccountID, ProfileID: p.ProfileID}
						e = d.Metadata.SelectMusicBrainz(route.kind, r.PathValue("id"), body, authorize)
					}
				} else {
					var body struct {
						ExpectedRevision int64 `json:"expectedRevision"`
					}
					if e = decodeLegacyRevision(w, r, &body); e == nil {
						if operation.suffix == "/search" {
							e = d.Metadata.SearchMusicBrainzAlternatives(route.kind, r.PathValue("id"), body.ExpectedRevision, authorize)
						} else {
							e = d.Metadata.RetryMusicBrainz(route.kind, r.PathValue("id"), body.ExpectedRevision, authorize)
						}
					}
				}
				if e != nil {
					failure(w, e)
					return
				}
				write(w, 202, map[string]string{"entityId": r.PathValue("id"), "status": "pending"})
			})
		}
	}
}
