package httpapi

import (
	"context"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"strconv"
	"time"
)

func (d Dependencies) playbackChapterRoutes(mux *http.ServeMux) {
	d.httpHandle(mux, "GET /v1/playback/sessions/{id}/chapters", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.principal(r)
		if e != nil {
			failure(w, e)
			return
		}
		session := r.PathValue("id")
		// A v1 client names its v1 session; the chapters are read against the
		// media session presenting it (playback_v1_presentation.go).
		presentation, v1, e := d.resolveV1Presentation(r.Context(), p, session)
		if e != nil {
			failure(w, e)
			return
		}
		bound := p
		if v1 {
			session, bound = presentation.media, presentation.as(p)
		}
		var item string
		e = d.DB.QueryRowContext(r.Context(), `SELECT pid(e.public_id) FROM playback_sessions ps JOIN catalog_entities e ON e.id=ps.item_id WHERE ps.id=? AND ps.session_hash=? AND ps.account_id=? AND ps.profile_id=?`, session, bound.Hash, bound.AccountID, bound.ProfileID).Scan(&item)
		if e != nil {
			failure(w, e)
			return
		}
		scoped := r.Clone(r.Context())
		scoped.SetPathValue("id", item)
		p, library, fence, e := d.detailAccess(scoped)
		if e != nil {
			failure(w, e)
			return
		}
		q := r.URL.Query()
		for key, values := range q {
			if (key != "limit" && key != "cursor" && key != "revision") || len(values) != 1 || len(values[0]) > 4096 {
				failure(w, playback.ErrChapterCursor)
				return
			}
		}
		limit := 100
		if q.Has("limit") {
			limit, e = strconv.Atoi(q.Get("limit"))
			if e != nil {
				failure(w, playback.ErrChapterCursor)
				return
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if v1 {
			p = presentation.as(p)
		}
		out, e := d.Playback.Chapters(ctx, p, playback.OffersScope{ServerID: d.Identity.ID(), LibraryID: library, ItemID: item, ViewerFence: fence}, session, q.Get("revision"), q.Get("cursor"), limit)
		if e != nil {
			failure(w, e)
			return
		}
		_, afterLibrary, after, e := d.detailAccess(scoped)
		if e != nil {
			failure(w, e)
			return
		}
		if after != fence || afterLibrary != library {
			failure(w, playback.ErrStaleChapter)
			return
		}
		if v1 {
			out.Scope.SessionID, out.Scope.Generation = presentation.session, presentation.generation
		}
		w.Header().Set("Cache-Control", "no-store")
		write(w, 200, out)
	})
}
