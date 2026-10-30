package httpapi

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"strconv"
)

func (d Dependencies) listeningRoutes(mux *http.ServeMux) {
	envelope := func(p identity.Principal, data any) any {
		return map[string]any{"scope": map[string]string{"serverId": d.Identity.ID(), "authority": p.Authority, "accountId": p.AccountID, "profileId": p.ProfileID}, "data": data}
	}
	d.httpHandle(mux, "GET /v1/listening/preferences", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.principal(r)
		if e != nil {
			failure(w, e)
			return
		}
		data, e := d.Catalog.WithContext(r.Context()).ListeningPreferences(p.Viewer, d.consoleAuthority(p, false))
		if e != nil {
			failure(w, e)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		write(w, 200, envelope(p, data))
	})
	d.httpHandle(mux, "PUT /v1/listening/preferences", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.principal(r)
		if e != nil {
			failure(w, e)
			return
		}
		var body struct {
			Revision       *int64   `json:"revision"`
			BookRate       *float64 `json:"bookRate"`
			PassoutMinutes *int     `json:"passoutMinutes"`
		}
		if e = strictDecode(w, r, &body); e != nil {
			failure(w, e)
			return
		}
		if body.Revision == nil || body.BookRate == nil || body.PassoutMinutes == nil {
			failure(w, errStrictJSON)
			return
		}
		data := catalog.ListeningPreferences{Revision: *body.Revision, BookRate: *body.BookRate, PassoutMinutes: *body.PassoutMinutes}
		data, e = d.Catalog.WithContext(r.Context()).SaveListeningPreferences(p.Viewer, data, d.consoleAuthority(p, false))
		if e != nil {
			failure(w, e)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		write(w, 200, envelope(p, data))
	})
	d.projectedHandle(mux, "GET /v1/libraries/{id}/listening/selection", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.principal(r)
		if e == nil {
			e = d.allowedLibrary(r.Context(), p, r.PathValue("id"))
		}
		if e != nil {
			failure(w, e)
			return
		}
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		target := catalog.ListeningTarget{LibraryID: r.PathValue("id"), Kind: q.Get("kind"), ID: q.Get("entityId")}
		var policy, restriction int64
		if p.Authority != "local" {
			if e = dbwork.QueryRow(r.Context(), d.DB, `SELECT COALESCE((SELECT revision FROM policy WHERE server_id=?),0),COALESCE((SELECT revision FROM restrictions WHERE profile_id=?),0)`, d.Identity.ID(), p.ProfileID).Scan(&policy, &restriction); e != nil {
				failure(w, e)
				return
			}
		}
		fence := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d:%d", presentationIdentity(p), p.Epoch, policy, restriction))))
		viewer, e := d.catalogViewer(r, p, []string{target.LibraryID}, fence)
		if e != nil {
			failure(w, e)
			return
		}
		var data catalog.ListeningSelection
		e = dbwork.WithReadSnapshot(r.Context(), d.DB, func(ctx context.Context) error {
			resumeRewindMS := 0
			if q.Get("resume") == "true" && target.Kind == "book" {
				preferences, _, _, err := operations.EffectivePreferences(dbwork.Snapshot(ctx), p.Viewer, "")
				if err != nil {
					return err
				}
				resumeRewindMS = preferences.Int("audiobooks.resumeRewindMs")
			}
			var err error
			data, err = d.Catalog.WithContext(ctx).ListeningSelection(catalog.ContentRequest{Viewer: viewer, ServerID: d.Identity.ID(), Library: target.LibraryID, Profile: identity.PersonalKey(p.Viewer), ViewerFence: fence, Cursor: q.Get("cursor"), Limit: limit, ResumeRewindMs: resumeRewindMS}, target, q.Get("mode"), q.Get("seed"), q.Get("resume") == "true", q.Get("startItem"))
			return err
		})
		if e == nil {
			e = d.allowedLibrary(r.Context(), p, target.LibraryID)
		}
		if e != nil {
			failure(w, e)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		write(w, 200, envelope(p, data))
	})
	d.httpHandle(mux, "GET /v1/items/{id}/listening", func(w http.ResponseWriter, r *http.Request) {
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
		data, e := d.Catalog.WithContext(r.Context()).ListeningItem(viewer, r.PathValue("id"))
		if e != nil {
			failure(w, e)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		write(w, 200, envelope(p, data))
	})
}
