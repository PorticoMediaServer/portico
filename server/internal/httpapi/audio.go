package httpapi

import (
	"context"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"strconv"
)

func (d Dependencies) audioRoutes(mux *http.ServeMux) {
	d.listeningRoutes(mux)
	d.sonicAnalysisRoutes(mux)
	d.musicMixesRoutes(mux)
	d.httpHandle(mux, "GET /v1/items/{id}/similar", func(w http.ResponseWriter, r *http.Request) {
		out, e := d.sonicSimilarRequest(r)
		if e != nil {
			failure(w, e)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		write(w, 200, out)
	})

	d.httpHandle(mux, "GET /v1/books/{id}/timeline", func(w http.ResponseWriter, r *http.Request) {
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
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		var position *int64
		if q.Has("positionMs") {
			n, err := strconv.ParseInt(q.Get("positionMs"), 10, 64)
			if err != nil || n < 0 {
				failure(w, errStrictJSON)
				return
			}
			position = &n
		}
		data, e := d.Catalog.WithContext(r.Context()).BookTimeline(catalog.ContentRequest{Viewer: viewer, Profile: identity.PersonalKey(p.Viewer), ViewerFence: fence, Cursor: q.Get("cursor"), Limit: limit}, r.PathValue("id"), position)
		if e == nil {
			e = d.allowedLibrary(r.Context(), p, data.LibraryID)
		}
		if e != nil {
			failure(w, e)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		write(w, 200, map[string]any{"scope": map[string]string{"serverId": d.Identity.ID(), "authority": p.Authority, "accountId": p.AccountID, "profileId": p.ProfileID}, "data": data})
	})

	d.httpHandle(mux, "GET /v1/libraries/{id}/artists", func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		d.serveAudioPage(w, r, "library", "artists", func(cat *catalog.Service, viewer catalog.Viewer, p identity.Principal) (any, string, error) {
			return cat.Artists(viewer, r.PathValue("id"), r.URL.Query().Get("cursor"), limit)
		})
	})
	d.httpHandle(mux, "GET /v1/libraries/{id}/books", func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		d.serveAudioPage(w, r, "library", "books", func(cat *catalog.Service, viewer catalog.Viewer, p identity.Principal) (any, string, error) {
			return cat.Books(viewer, r.PathValue("id"), r.URL.Query().Get("cursor"), limit)
		})
	})
	for _, route := range []struct{ pattern, kind, collection string }{{"GET /v1/artists/{id}/albums", "artist", "albums"}, {"GET /v1/albums/{id}/songs", "album", "songs"}, {"GET /v1/books/{id}/files", "book", "files"}, {"GET /v1/books/{id}/chapters", "book", "chapters"}} {
		d.httpHandle(mux, route.pattern, func(w http.ResponseWriter, r *http.Request) {
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			cursor := r.URL.Query().Get("cursor")
			d.serveAudioPage(w, r, route.kind, route.collection, func(cat *catalog.Service, viewer catalog.Viewer, p identity.Principal) (any, string, error) {
				switch route.collection {
				case "albums":
					// An artist's release rows carry their aggregate selection
					// offers from the same snapshot (MOB-030), fenced after.
					var albums []catalog.Album
					var next string
					before, err := d.containerOffersFenceBefore(r, p)
					if err != nil {
						return nil, "", err
					}
					err = d.composeScope(r, viewer.Restrictions, func(ctx context.Context, c *catalog.Service) error {
						var e error
						if albums, next, e = c.Albums(viewer, r.PathValue("id"), cursor, limit); e != nil {
							return e
						}
						return d.projectAlbumActions(ctx, p, viewer, c, albums)
					})
					if err == nil {
						err = d.containerOffersFenceAfter(r, p, before)
					}
					return albums, next, err
				case "chapters":
					return cat.BookChapters(viewer, r.PathValue("id"), cursor, limit)
				default:
					return cat.AudioItems(viewer, r.PathValue("id"), route.kind, cursor, limit)
				}
			})
		})
	}
}

// serveAudioPage authorizes the library before checking its grid validator.
func (d Dependencies) serveAudioPage(w http.ResponseWriter, r *http.Request, kind, collection string, body func(*catalog.Service, catalog.Viewer, identity.Principal) (any, string, error)) {
	p, err := d.principal(r)
	if err != nil {
		failure(w, err)
		return
	}
	cat := d.Catalog.WithContext(r.Context())
	library := r.PathValue("id")
	if kind != "library" {
		library, err = cat.LibraryForAudioEntity(kind, library)
		if err != nil {
			failure(w, err)
			return
		}
	}
	if err = d.allowedLibrary(r.Context(), p, library); err != nil {
		failure(w, err)
		return
	}
	var extra []string
	if collection == "albums" {
		extra = append(extra, d.containerOffersValidator(r, p, library))
	}
	tag := d.gridTag(r, p, gridTagBucket, extra...)
	if gridNotModified(w, r, tag) {
		return
	}
	viewer, err := d.catalogViewer(r, p, []string{library}, "")
	if err != nil {
		failure(w, err)
		return
	}
	rows, next, err := body(cat, viewer, p)
	if err != nil {
		failure(w, err)
		return
	}
	setGridTag(w, tag)
	write(w, 200, map[string]any{collection: rows, "nextCursor": next})
}

// projectAlbumActions offers an artist page's release rows the same aggregate
// album actions a displayed album entry carries.
func (d Dependencies) projectAlbumActions(ctx context.Context, p identity.Principal, viewer catalog.Viewer, cat *catalog.Service, albums []catalog.Album) error {
	entries := make([]catalog.ContentEntry, len(albums))
	pointers := make([]*catalog.ContentEntry, len(albums))
	for i, album := range albums {
		entries[i] = catalog.ContentEntry{ID: album.ID, LibraryID: album.LibraryID, Kind: "album"}
		pointers[i] = &entries[i]
	}
	if err := d.projectContainerActions(ctx, p, viewer, cat, "", pointers); err != nil {
		return err
	}
	for i := range albums {
		albums[i].HomeActions = entries[i].HomeActions
	}
	return nil
}
