package httpapi

import (
	"errors"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"strconv"
)

func (d Dependencies) episodeRoutes(mux *http.ServeMux) {
	d.showWorkspaceRoutes(mux)
	if d.Metadata != nil {
		d.tvdbRoutes(mux)
		d.screenMetadataRoutes(mux)
		d.musicBrainzRoutes(mux)
	}
	access := func(w http.ResponseWriter, r *http.Request, library string, err error) (identity.Principal, bool) {
		p, authErr := d.principal(r)
		if authErr != nil {
			failure(w, authErr)
			return p, false
		}
		if err != nil {
			failure(w, err)
			return p, false
		}
		if err == nil {
			err = d.allowedLibrary(r.Context(), p, library)
		}
		if err != nil {
			failure(w, err)
			return p, false
		}
		return p, true
	}
	d.httpHandle(mux, "GET /v1/libraries/{id}/shows", func(w http.ResponseWriter, r *http.Request) {
		lib := r.PathValue("id")
		p, ok := access(w, r, lib, nil)
		if !ok {
			return
		}
		viewer, err := d.catalogViewer(r, p, []string{lib}, "")
		if err != nil {
			failure(w, err)
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		tag := d.gridTag(r, p, gridTagBucket)
		if gridNotModified(w, r, tag) {
			return
		}
		rows, next, err := d.Catalog.WithContext(r.Context()).Shows(viewer, lib, r.URL.Query().Get("cursor"), limit)
		if err != nil {
			failure(w, err)
			return
		}
		setGridTag(w, tag)
		write(w, 200, map[string]any{"shows": rows, "nextCursor": next})
	})
	d.httpHandle(mux, "GET /v1/shows/{id}/seasons", func(w http.ResponseWriter, r *http.Request) {
		lib, err := d.Catalog.WithContext(r.Context()).LibraryForShow(r.PathValue("id"))
		p, ok := access(w, r, lib, err)
		if !ok {
			return
		}
		viewer, err := d.catalogViewer(r, p, []string{lib}, "")
		if err != nil {
			failure(w, err)
			return
		}
		rows, err := d.Catalog.WithContext(r.Context()).Seasons(viewer, r.PathValue("id"))
		if err != nil {
			failure(w, err)
			return
		}
		write(w, 200, map[string]any{"seasons": rows})
	})
	d.httpHandle(mux, "GET /v1/seasons/{id}/episodes", func(w http.ResponseWriter, r *http.Request) {
		lib, err := d.Catalog.WithContext(r.Context()).LibraryForSeason(r.PathValue("id"))
		p, ok := access(w, r, lib, err)
		if !ok {
			return
		}
		viewer, err := d.catalogViewer(r, p, []string{lib}, "")
		if err != nil {
			failure(w, err)
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		rows, next, err := d.Catalog.WithContext(r.Context()).Episodes(viewer, "", r.PathValue("id"), r.URL.Query().Get("cursor"), limit, r.URL.Query().Get("playState"))
		if err != nil {
			failure(w, err)
			return
		}
		write(w, 200, map[string]any{"episodes": rows, "nextCursor": next})
	})
	d.httpHandle(mux, "GET /v1/shows/{id}/episodes", func(w http.ResponseWriter, r *http.Request) {
		lib, err := d.Catalog.WithContext(r.Context()).LibraryForShow(r.PathValue("id"))
		p, ok := access(w, r, lib, err)
		if !ok {
			return
		}
		viewer, err := d.catalogViewer(r, p, []string{lib}, "")
		if err != nil {
			failure(w, err)
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		rows, next, err := d.Catalog.WithContext(r.Context()).Episodes(viewer, r.PathValue("id"), "", r.URL.Query().Get("cursor"), limit, r.URL.Query().Get("playState"))
		if err != nil {
			failure(w, err)
			return
		}
		write(w, 200, map[string]any{"episodes": rows, "nextCursor": next, "group": "unassigned_absolute"})
	})
	d.httpHandle(mux, "GET /v1/shows/{id}/recommendations", func(w http.ResponseWriter, r *http.Request) {
		if e := homeAllowedQuery(r, "limit"); e != nil {
			homeFailure(w, e)
			return
		}
		limit, e := homeQueryLimit(r)
		if e != nil {
			homeFailure(w, e)
			return
		}
		// The show's rows carry the fence its row route continues under.
		p, viewer, restrictions, fence, e := d.showRecommendationViewer(r)
		if e != nil {
			homeFailure(w, e)
			return
		}
		var rows []catalog.HomeRow
		var revision catalog.ContentRevision
		e = d.compose(r, restrictions, func(cat *catalog.Service) error {
			var composeErr error
			rows, revision, composeErr = cat.ShowRecommendationRows(viewer, r.PathValue("id"), limit)
			return composeErr
		})
		if e != nil {
			homeFailure(w, e)
			return
		}
		if _, after, e := d.detailLibraryFenceFor(r, p, viewer.Libraries[0]); e != nil || after != fence {
			homeFailure(w, errors.Join(e, catalog.ErrStaleContinuation))
			return
		}
		write(w, 200, map[string]any{"showId": r.PathValue("id"), "rows": rows, "revision": revision})
	})
	d.httpHandle(mux, "GET /v1/libraries/{id}/episode-issues", func(w http.ResponseWriter, r *http.Request) {
		if _, err := d.owner(r); err != nil {
			failure(w, err)
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		rows, next, err := d.Catalog.WithContext(r.Context()).EpisodeIssues(r.PathValue("id"), r.URL.Query().Get("cursor"), limit)
		if err != nil {
			failure(w, err)
			return
		}
		write(w, 200, map[string]any{"issues": rows, "nextCursor": next})
	})
	d.httpHandle(mux, "POST /v1/libraries/{id}/episode-assignments", func(w http.ResponseWriter, r *http.Request) {
		if _, err := d.owner(r); err != nil {
			failure(w, err)
			return
		}
		var body catalog.EpisodeAssignment
		if err := decode(w, r, &body); err != nil {
			failure(w, err)
			return
		}
		if err := d.Catalog.AssignEpisodes(r.Context(), r.PathValue("id"), body); err != nil {
			failure(w, err)
			return
		}
		write(w, 200, map[string]string{"status": "assigned"})
	})
}
