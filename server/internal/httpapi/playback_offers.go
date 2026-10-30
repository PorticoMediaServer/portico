package httpapi

import (
	"context"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"time"
)

func (d Dependencies) playbackOfferRoutes(mux *http.ServeMux) {
	d.httpHandle(mux, "GET /v1/items/{id}/playback-offers", func(w http.ResponseWriter, r *http.Request) {
		p, library, fence, e := d.detailAccess(r)
		if e != nil {
			failure(w, e)
			return
		}
		q := r.URL.Query()
		for key, values := range q {
			if (key != "sessionId" && key != "revision") || len(values) != 1 || len(values[0]) > 128 {
				failure(w, requestinput.New("invalid playback offer query"))
				return
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		// A v1 client names its v1 session; the offers live on the media
		// session presenting it (playback_v1_presentation.go).
		session := q.Get("sessionId")
		presentation, v1, e := d.resolveV1Presentation(ctx, p, session)
		if e != nil {
			failure(w, e)
			return
		}
		reader, offerCtx := p, ctx
		if v1 {
			// Owned by the v1 rule; live while the caller's own login is.
			session, reader, offerCtx = presentation.media, presentation.as(p), playback.WithOfferLogin(ctx, p.Hash)
		}
		out, e := d.Playback.Offers(offerCtx, reader, playback.OffersScope{ServerID: d.Identity.ID(), LibraryID: library, ItemID: r.PathValue("id"), ViewerFence: fence}, session, q.Get("revision"))
		if e != nil {
			failure(w, e)
			return
		}
		if v1 {
			if out.Current != nil {
				out.Current.SessionID, out.Current.Generation = presentation.session, presentation.generation
			}
			if out.SubtitlePlan != nil {
				out.SubtitlePlan.SessionID, out.SubtitlePlan.Generation = presentation.session, presentation.generation
			}
		}
		_, afterLibrary, after, e := d.detailAccess(r)
		if e != nil {
			failure(w, e)
			return
		}
		if after != fence || afterLibrary != library {
			failure(w, playback.ErrStaleOffer)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		write(w, 200, out)
	})
}
