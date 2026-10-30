package httpapi

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"strconv"
	"time"
)

// presentationIdentity binds documents to the server and viewer, independently
// of access-token rotation. Authorization still uses the current token hash.
func presentationIdentity(p identity.Principal) string {
	return p.ServerID + ":" + identity.PersonalKey(p.Viewer)
}

func viewerScope(p identity.Principal) string {
	return p.Authority + ":" + p.AccountID + ":" + p.ProfileID
}
func (d Dependencies) discoveryRoutes(mux *http.ServeMux) {
	d.homeRoutes(mux)
	d.detailRoutes(mux)
	d.playlistRoutes(mux)
	d.savedResourceRoutes(mux)
	libraryAccess := func(w http.ResponseWriter, r *http.Request, library string) (identity.Principal, bool) {
		p, err := d.principal(r)
		if err == nil {
			err = d.allowedLibrary(r.Context(), p, library)
		}
		if err != nil {
			failure(w, err)
			return p, false
		}
		return p, true
	}
	// query carries the profile's content restrictions (SEC-02: this legacy
	// browse and collection-items path used to omit them), and folds their
	// fence into the cursor scope so a cursor does not outlive a change.
	query := func(r *http.Request, p identity.Principal, library, collection string) (catalog.BrowseQuery, error) {
		restrictions, fence, err := d.viewerRestrictions(r, p)
		if err != nil {
			return catalog.BrowseQuery{}, err
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		return catalog.BrowseQuery{Library: library, Viewer: viewerScope(p) + ":" + fence, Profile: identity.PersonalKey(p.Viewer), Sort: r.URL.Query().Get("sort"), Direction: r.URL.Query().Get("direction"), Category: r.URL.Query().Get("category"), Collection: collection, Search: r.URL.Query().Get("q"), Cursor: r.URL.Query().Get("cursor"), Limit: limit, Restrictions: restrictions}, nil
	}
	d.projectedHandle(mux, "GET /v1/libraries/{id}/content", func(w http.ResponseWriter, r *http.Request) {
		p, ok := libraryAccess(w, r, r.PathValue("id"))
		if !ok {
			return
		}
		tag := d.gridTag(r, p, gridTagBucket, d.containerOffersValidator(r, p, r.PathValue("id")))
		if gridNotModified(w, r, tag) {
			return
		}
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		start := 0
		if q.Has("start") {
			var err error
			if start, err = strconv.Atoi(q.Get("start")); err != nil || start < 0 || strconv.Itoa(start) != q.Get("start") {
				failure(w, catalog.ErrContentStart)
				return
			}
		}
		var policy, restriction int64
		if p.Authority != "local" {
			if err := dbwork.QueryRow(r.Context(), d.DB, `SELECT COALESCE((SELECT revision FROM policy WHERE server_id=?),0),COALESCE((SELECT revision FROM restrictions WHERE profile_id=?),0)`, d.Identity.ID(), p.ProfileID).Scan(&policy, &restriction); err != nil {
				failure(w, err)
				return
			}
		}
		fence := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d:%d", presentationIdentity(p), p.Epoch, policy, restriction))))
		restrictions, _, err := d.viewerRestrictions(r, p)
		if err != nil {
			failure(w, err)
			return
		}
		viewer := catalog.Viewer{Profile: identity.PersonalKey(p.Viewer), Fence: fence, Libraries: []string{r.PathValue("id")}, Restrictions: restrictions}
		// The page's displayed containers carry their aggregate selection
		// offers from the same snapshot (MOB-030), fenced after the read.
		var result catalog.ContentEnvelope
		before, err := d.containerOffersFenceBefore(r, p)
		if err != nil {
			failure(w, err)
			return
		}
		err = d.composeScope(r, restrictions, func(ctx context.Context, cat *catalog.Service) error {
			var e error
			result, e = cat.Content(catalog.ContentRequest{Viewer: viewer, Restrictions: restrictions, ServerID: d.Identity.ID(), Library: r.PathValue("id"), Profile: identity.PersonalKey(p.Viewer), ViewerFence: fence, View: q.Get("view"), EntityID: q.Get("entityId"), Sort: q.Get("sort"), Direction: q.Get("direction"), Category: q.Get("category"), Q: q.Get("q"), Cursor: q.Get("cursor"), Limit: limit, Start: start})
			if e != nil {
				return e
			}
			return d.projectContainerActions(ctx, p, viewer, cat, r.PathValue("id"), contentEnvelopeEntries(&result))
		})
		if err != nil {
			failure(w, err)
			return
		}
		if err = d.containerOffersFenceAfter(r, p, before); err != nil {
			failure(w, err)
			return
		}
		if err = d.allowedLibrary(r.Context(), p, r.PathValue("id")); err != nil {
			failure(w, err)
			return
		}
		setGridTag(w, tag)
		write(w, 200, result)
	})
	d.projectedHandle(mux, "GET /v1/libraries/{id}/browse", func(w http.ResponseWriter, r *http.Request) {
		p, ok := libraryAccess(w, r, r.PathValue("id"))
		if !ok {
			return
		}
		q, err := query(r, p, r.PathValue("id"), "")
		if err != nil {
			failure(w, err)
			return
		}
		tag := d.gridTag(r, p, gridTagBucket)
		if gridNotModified(w, r, tag) {
			return
		}
		rows, next, err := d.Catalog.WithContext(r.Context()).Browse(q)
		if err != nil {
			failure(w, err)
			return
		}
		setGridTag(w, tag)
		write(w, 200, map[string]any{"items": rows, "nextCursor": next})
	})
	d.projectedHandle(mux, "GET /v1/libraries/{id}/discover", func(w http.ResponseWriter, r *http.Request) {
		p, ok := libraryAccess(w, r, r.PathValue("id"))
		if !ok {
			return
		}
		restrictions, _, e := d.viewerRestrictions(r, p)
		if e != nil {
			failure(w, e)
			return
		}
		tag := d.gridTag(r, p, time.Minute)
		if gridNotModified(w, r, tag) {
			return
		}
		var result catalog.Discovery
		e = d.compose(r, restrictions, func(cat *catalog.Service) error {
			var composeErr error
			result, composeErr = cat.DiscoverWithRestrictions(r.PathValue("id"), identity.PersonalKey(p.Viewer), restrictions, time.Time{})
			return composeErr
		})
		if e != nil {
			failure(w, e)
			return
		}
		setGridTag(w, tag)
		write(w, 200, result)
	})
	d.httpHandle(mux, "GET /v1/libraries/{id}/categories", func(w http.ResponseWriter, r *http.Request) {
		p, ok := libraryAccess(w, r, r.PathValue("id"))
		if !ok {
			return
		}
		viewer, err := d.catalogViewer(r, p, []string{r.PathValue("id")}, "")
		if err != nil {
			failure(w, err)
			return
		}
		tag := d.gridTag(r, p, gridTagBucket)
		if gridNotModified(w, r, tag) {
			return
		}
		rows, err := d.Catalog.WithContext(r.Context()).Categories(viewer, r.PathValue("id"))
		if err != nil {
			failure(w, err)
			return
		}
		setGridTag(w, tag)
		write(w, 200, map[string]any{"categories": rows})
	})
	d.httpHandle(mux, "GET /v1/libraries/{id}/collections", func(w http.ResponseWriter, r *http.Request) {
		p, ok := libraryAccess(w, r, r.PathValue("id"))
		if !ok {
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		viewer, err := d.catalogViewer(r, p, []string{r.PathValue("id")}, viewerScope(p))
		if err != nil {
			failure(w, err)
			return
		}
		tag := d.gridTag(r, p, gridTagBucket)
		if gridNotModified(w, r, tag) {
			return
		}
		rows, next, err := d.Catalog.WithContext(r.Context()).Collections(r.PathValue("id"), viewer, r.URL.Query().Get("cursor"), limit)
		if err != nil {
			failure(w, err)
			return
		}
		setGridTag(w, tag)
		write(w, 200, map[string]any{"collections": rows, "nextCursor": next})
	})
	d.httpHandle(mux, "POST /v1/libraries/{id}/collections", func(w http.ResponseWriter, r *http.Request) {
		if _, err := d.owner(r); err != nil {
			failure(w, err)
			return
		}
		var body struct {
			Name string `json:"name"`
		}
		if err := decode(w, r, &body); err != nil {
			failure(w, err)
			return
		}
		row, err := d.Catalog.WithContext(r.Context()).CreateCollection(r.PathValue("id"), body.Name)
		if err != nil {
			failure(w, err)
			return
		}
		write(w, 201, row)
	})
	collectionAccess := func(w http.ResponseWriter, r *http.Request) (identity.Principal, catalog.Collection, bool) {
		p, err := d.principal(r)
		if err != nil {
			failure(w, err)
			return p, catalog.Collection{}, false
		}
		// Readiness before existence: while a restricted profile's classes
		// build, every collection id answers "building", hidden or absent
		// alike (SEC-02).
		if err = d.collectionClassesReady(r, p); err != nil {
			failure(w, err)
			return p, catalog.Collection{}, false
		}
		collection, err := d.Catalog.WithContext(r.Context()).Collection(r.PathValue("id"))
		if err == nil {
			err = d.allowedLibrary(r.Context(), p, collection.LibraryID)
		}
		if err == nil {
			// A collection whose members are all hidden from this profile does
			// not exist for it, and its count is of visible members only.
			var viewer catalog.Viewer
			if viewer, err = d.catalogViewer(r, p, []string{collection.LibraryID}, viewerScope(p)); err == nil {
				collection, err = d.Catalog.WithContext(r.Context()).VisibleCollection(r.Context(), viewer, collection.ID)
			}
		}
		if err != nil {
			failure(w, err)
			return p, collection, false
		}
		return p, collection, true
	}
	d.httpHandle(mux, "GET /v1/collections/{id}", func(w http.ResponseWriter, r *http.Request) {
		_, c, ok := collectionAccess(w, r)
		if ok {
			write(w, 200, c)
		}
	})
	d.projectedHandle(mux, "GET /v1/collections/{id}/items", func(w http.ResponseWriter, r *http.Request) {
		p, c, ok := collectionAccess(w, r)
		if !ok {
			return
		}
		q, err := query(r, p, c.LibraryID, c.ID)
		if err != nil {
			failure(w, err)
			return
		}
		tag := d.gridTag(r, p, gridTagBucket)
		if gridNotModified(w, r, tag) {
			return
		}
		rows, next, err := d.Catalog.WithContext(r.Context()).Browse(q)
		if err != nil {
			failure(w, err)
			return
		}
		setGridTag(w, tag)
		write(w, 200, map[string]any{"items": rows, "nextCursor": next})
	})
	d.httpHandle(mux, "PATCH /v1/collections/{id}", func(w http.ResponseWriter, r *http.Request) {
		if _, err := d.owner(r); err != nil {
			failure(w, err)
			return
		}
		var body struct {
			Name string `json:"name"`
		}
		if err := decode(w, r, &body); err != nil {
			failure(w, err)
			return
		}
		row, err := d.Catalog.WithContext(r.Context()).RenameCollection(r.PathValue("id"), body.Name)
		if err != nil {
			failure(w, err)
			return
		}
		write(w, 200, row)
	})
	d.httpHandle(mux, "DELETE /v1/collections/{id}", func(w http.ResponseWriter, r *http.Request) {
		if _, err := d.owner(r); err != nil {
			failure(w, err)
			return
		}
		if err := d.Catalog.WithContext(r.Context()).DeleteCollection(r.PathValue("id")); err != nil {
			failure(w, err)
			return
		}
		w.WriteHeader(204)
	})
	for _, method := range []string{"PUT", "DELETE"} {
		d.projectedHandle(mux, method+" /v1/collections/{id}/items/{itemId}", func(w http.ResponseWriter, r *http.Request) {
			if _, err := d.owner(r); err != nil {
				failure(w, err)
				return
			}
			if err := d.Catalog.WithContext(r.Context()).SetCollectionItem(r.PathValue("id"), r.PathValue("itemId"), r.Method == "PUT"); err != nil {
				failure(w, err)
				return
			}
			w.WriteHeader(204)
		})
	}
}

// collectionClassesReady is catalog.CollectionClassesReady over every library
// the principal may open. Unrestricted profiles return at once.
func (d Dependencies) collectionClassesReady(r *http.Request, p identity.Principal) error {
	viewer, err := d.catalogViewer(r, p, nil, viewerScope(p))
	if err != nil || !viewer.EffectiveRestrictions().Active() {
		return err
	}
	libraries, err := d.visibleLibraries(r, p)
	if err != nil {
		return err
	}
	for _, library := range libraries {
		if library.Kind == "movie" { // collections live in film libraries
			viewer.Libraries = append(viewer.Libraries, library.ID)
		}
	}
	return d.Catalog.WithContext(r.Context()).CollectionClassesReady(viewer)
}

// contentEnvelopeEntries are a content page's displayed entries: its entity
// and every section's entries.
func contentEnvelopeEntries(out *catalog.ContentEnvelope) []*catalog.ContentEntry {
	entries := []*catalog.ContentEntry{}
	if out.Entity != nil {
		entries = append(entries, out.Entity)
	}
	for i := range out.Sections {
		for j := range out.Sections[i].Entries {
			entries = append(entries, &out.Sections[i].Entries[j])
		}
	}
	return entries
}
