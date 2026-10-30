package httpapi

import (
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"strconv"
)

func (d Dependencies) showWorkspaceRoutes(mux *http.ServeMux) {
	d.httpHandle(mux, "GET /v1/libraries/{id}/show-workspace", func(w http.ResponseWriter, r *http.Request) {
		p, libraries, fence, e := d.homeScope(r)
		if e != nil {
			failure(w, e)
			return
		}
		allowed := false
		for _, id := range libraries {
			if id == r.PathValue("id") {
				allowed = true
			}
		}
		if !allowed {
			failure(w, identity.ErrNotVisible)
			return
		}
		q := r.URL.Query()
		known := map[string]bool{"episodeDirection": true, "playState": true, "showId": true, "seasonId": true, "episodeId": true, "selectedSeasonId": true, "group": true, "cursor": true, "seasonCursor": true, "limit": true, "seasonLimit": true}
		for key, values := range q {
			if !known[key] || len(values) != 1 || len(values[0]) > 4096 {
				failure(w, catalog.ErrShowContext)
				return
			}
		}
		parse := func(key string) (int, error) {
			if q.Get(key) == "" {
				return 0, nil
			}
			return strconv.Atoi(q.Get(key))
		}
		limit, e := parse("limit")
		if e != nil {
			failure(w, catalog.ErrShowContext)
			return
		}
		seasonLimit, e := parse("seasonLimit")
		if e != nil {
			failure(w, catalog.ErrShowContext)
			return
		}
		viewer, e := d.catalogViewer(r, p, libraries, fence)
		if e != nil {
			failure(w, e)
			return
		}
		out, e := d.Catalog.WithContext(r.Context()).ShowWorkspace(catalog.ShowWorkspaceRequest{Viewer: viewer, EpisodeDirection: q.Get("episodeDirection"), PlayState: q.Get("playState"), ServerID: d.Identity.ID(), Library: r.PathValue("id"), Profile: identity.PersonalKey(p.Viewer), ViewerFence: fence, ShowID: q.Get("showId"), SeasonID: q.Get("seasonId"), EpisodeID: q.Get("episodeId"), SelectedSeasonID: q.Get("selectedSeasonId"), Group: q.Get("group"), Cursor: q.Get("cursor"), SeasonCursor: q.Get("seasonCursor"), Limit: limit, SeasonLimit: seasonLimit})
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
}
