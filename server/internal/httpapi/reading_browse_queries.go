package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
	"reflect"
	"time"
)

type ReadingBrowsePreparing struct {
	State        string `json:"state"`
	Kind         string `json:"kind"`
	QueryID      string `json:"queryId"`
	RetryAfterMs int    `json:"retryAfterMs"`
}

const readingBrowseRetryMs = 1000
const readingInitialRetryMs = 250

type readingBrowseQueryResponse struct{}

func (readingBrowseQueryResponse) JSONSchemaForStatus(status int) map[string]any {
	if status == 200 {
		shape, _ := apikit.Schema(reflect.TypeFor[catalog.BrowseResult]())
		return withVariantTag(shape, "ready")
	}
	if status == 202 {
		shape, _ := apikit.Schema(reflect.TypeFor[ReadingBrowsePreparing]())
		shape["additionalProperties"] = false
		props := shape["properties"].(map[string]any)
		props["kind"].(map[string]any)["const"] = "reading-browse-preparing"
		props["state"].(map[string]any)["const"] = "preparing"
		props["queryId"].(map[string]any)["minLength"] = 1
		props["queryId"].(map[string]any)["maxLength"] = 256
		props["queryId"].(map[string]any)["pattern"] = "^[A-Za-z0-9_-]+$"
		props["retryAfterMs"].(map[string]any)["minimum"] = 1
		return withVariantTag(shape, "preparing")
	}
	return nil
}
func (r readingBrowseQueryResponse) JSONSchema() map[string]any {
	return map[string]any{"anyOf": []any{r.JSONSchemaForStatus(200), r.JSONSchemaForStatus(202)}}
}

// Parent cohort registers these typed routes and the 202 contract together.
// The existing foreground POST browse handler is deliberately unchanged.
func registerReadingBrowseQueryRoutes(registry *apikit.Registry, d Dependencies) error {
	failures := []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "revision_mismatch", "stale_continuation", "search_busy", "internal"}
	if err := apikit.RegisterRaw(registry, apikit.RawRoute[noBody, readingBrowseQueryResponse]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneBrowsing, ID: "get_library_reading_browse_query", Method: "GET", Path: "/v1/libraries/{id}/reading-browse-queries/{queryId}", Summary: "Read an exact prepared Reading browse page", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Cost: apikit.PageSized, Status: 200, SuccessStatuses: []int{202}, ResponseHeaders: []apikit.ResponseHeader{{Status: 202, Name: "Retry-After", Description: "Minimum polling delay", Required: true}, {Status: 202, Name: "Location", Description: "Library-scoped poll path", Required: true}}, Errors: failures}, Serve: d.readingBrowseQueryHandler}); err != nil {
		return err
	}
	return apikit.RegisterRaw(registry, apikit.RawRoute[noBody, noBody]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneBrowsing, ID: "delete_library_reading_browse_query", Method: "DELETE", Path: "/v1/libraries/{id}/reading-browse-queries/{queryId}", Summary: "Cancel this caller's Reading browse preparation", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Cost: apikit.Constant, Status: 204, NoResponseBody: true, Errors: failures}, Serve: d.readingBrowseQueryHandler})
}
func (d Dependencies) readingCountViewer(ctx context.Context, p identity.Principal, library string) (catalog.Viewer, error) {
	var out catalog.Viewer
	err := dbwork.WithReadSnapshot(ctx, d.DB, func(read context.Context) error {
		var e error
		out, e = d.readingBrowseViewer(read, p, library, catalog.Viewer{})
		if e == nil && (len(out.Libraries) != 1 || out.Libraries[0] != library) {
			e = catalog.ErrBrowsePivot
		}
		return e
	})
	return out, err
}
func (d Dependencies) readingCountCaller(p identity.Principal, v catalog.Viewer, library string) readprep.Caller {
	// Result carries Basis but no namespace: both domain and library MUST be in
	// this exact private identity, in addition to the authenticated session.
	raw, _ := json.Marshal(struct {
		Domain, Library, Server, Hash, Grant, Fence string
		Viewer                                      identity.Viewer
		Epoch                                       int
	}{catalog.ReadingBrowseCountDomain, library, d.Identity.ID(), p.Hash, p.Grant, v.Fence, p.Viewer, p.Epoch})
	return readprep.Caller(raw)
}
func readingBrowsePreparationFailure(w http.ResponseWriter, err error) {
	if errors.Is(err, readprep.ErrStale) || errors.Is(err, catalog.ErrStaleContinuation) || errors.Is(err, catalog.ErrReadingProjectionChanged) {
		policyError(w, "stale_continuation")
		return
	}
	if errors.Is(err, readprep.ErrCapacity) || errors.Is(err, readprep.ErrClosed) {
		w.Header().Set("Retry-After", "1")
		policyError(w, "search_busy")
		return
	}
	browseFailure(w, err)
}
func (d Dependencies) readingCountPersonal(ctx context.Context, p identity.Principal, library string) func(*sql.Tx) error {
	if !identity.CanUsePersonalState(p) {
		return nil
	}
	return func(tx *sql.Tx) error {
		live, err := d.Identity.PrincipalByDigestTx(ctx, tx, p.Hash)
		if err != nil {
			return err
		}
		if live.Viewer != p.Viewer || live.Epoch != p.Epoch || live.Grant != p.Grant || !identity.CanUsePersonalState(live) {
			return identity.ErrForbidden
		}
		return d.allowedLibraryTx(ctx, live, library, tx)
	}
}

// Explicit future foreground hook; no route switches to it in this slice.
func (d Dependencies) readingBrowsePreparedHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	p, ok := d.browseLibrary(w, r)
	if !ok {
		return
	}
	var body browseRequestBody
	if err := decode(w, r, &body); err != nil {
		failure(w, err)
		return
	}
	// Reading has no whole-view query selector. Never downgrade a supplied
	// held proof into an unrelated prepared page after a library-kind change.
	if body.SelectionBasis != "" {
		readingBrowsePreparationFailure(w, catalog.ErrStaleContinuation)
		return
	}
	query, err := catalog.ParseBrowseQuery(body.Query, "query")
	if err != nil {
		browseFailure(w, err)
		return
	}
	library := r.PathValue("id")
	viewer, err := d.readingCountViewer(r.Context(), p, library)
	if err != nil {
		browseFailure(w, err)
		return
	}
	request := catalog.BrowseRequest{ServerID: d.Identity.ID(), Library: library, HomeRowDisplay: body.HomeRowDisplay, Pivot: body.Pivot, Query: query, Sort: body.Sort, Limit: body.Limit, Cursor: body.Cursor, Range: body.Range, Seek: body.Seek}
	if body.Cursor != "" {
		var page catalog.BrowseResult
		var carried bool
		err = d.compose(r, viewer.EffectiveRestrictions(), func(cat *catalog.Service) error {
			live, e := d.readingBrowseViewer(cat.Context(), p, library, viewer)
			if e != nil {
				return e
			}
			viewer = live
			request.ReadingPersonalAuthorize = d.readingCountPersonal(cat.Context(), p, library)
			page, carried, e = cat.BrowseReadingCarriedTotal(viewer, request)
			return e
		})
		if err != nil {
			readingBrowsePreparationFailure(w, err)
			return
		}
		if carried {
			if err = d.browseAuthorityAfter(r, p, viewer); err != nil {
				browseFailure(w, err)
				return
			}
			write(w, 200, variantBody{"ready", page})
			return
		}
	}
	caller := d.readingCountCaller(p, viewer, library)
	authorize := func(ctx context.Context) (catalog.Viewer, error) { return d.readingCountViewer(ctx, p, library) }
	handle, err := d.Catalog.WithContext(r.Context()).SubmitReadingBrowseCount(caller, viewer, request, p.ExpiresAt)
	if err != nil {
		readingBrowsePreparationFailure(w, err)
		return
	}
	// This initial private handle is not yet known to the client.
	defer func() {
		if r.Context().Err() != nil {
			_ = d.Catalog.ReadPreparations().Cancel(context.WithoutCancel(r.Context()), caller, handle)
		}
	}()
	after, err := authorize(r.Context())
	if err != nil || d.readingCountCaller(p, after, library) != caller {
		d.Catalog.ReadPreparations().Cancel(r.Context(), caller, handle)
		if err == nil {
			err = readprep.ErrStale
		}
		readingBrowsePreparationFailure(w, err)
		return
	}
	r.SetPathValue("queryId", string(handle))
	d.readingBrowseQueryWithGrace(w, r, readprep.InitialReadyGrace)
}

func (d Dependencies) readingBrowseQueryHandler(w http.ResponseWriter, r *http.Request) {
	d.readingBrowseQueryWithGrace(w, r, 0)
}

func (d Dependencies) readingBrowseQueryWithGrace(w http.ResponseWriter, r *http.Request, grace time.Duration) {
	w.Header().Set("Cache-Control", "private, no-store")
	p, ok := d.browseLibrary(w, r)
	if !ok {
		return
	}
	if len(r.URL.Query()) != 0 {
		failure(w, catalog.ErrCursor)
		return
	}
	library, id := r.PathValue("id"), r.PathValue("queryId")
	if id == "" || len(id) > 256 {
		readingBrowsePreparationFailure(w, readprep.ErrStale)
		return
	}
	viewer, err := d.readingCountViewer(r.Context(), p, library)
	if err != nil {
		browseFailure(w, err)
		return
	}
	caller := d.readingCountCaller(p, viewer, library)
	store := d.Catalog.ReadPreparations()
	if store == nil {
		readingBrowsePreparationFailure(w, readprep.ErrStale)
		return
	}
	if r.Method == http.MethodDelete {
		if err = store.Cancel(r.Context(), caller, readprep.Handle(id)); err != nil {
			readingBrowsePreparationFailure(w, err)
			return
		}
		if _, err = d.readingCountViewer(r.Context(), p, library); err != nil {
			browseFailure(w, err)
			return
		}
		write(w, 204, nil)
		return
	}
	result, err := store.PollWhenReady(r.Context(), caller, readprep.Handle(id), grace)
	defer result.Release()
	if grace > 0 && result.State == readprep.Ready {
		// Direct 200 exposes no preparation handle for client cleanup.
		defer store.Cancel(context.WithoutCancel(r.Context()), caller, readprep.Handle(id))
	}
	if err != nil {
		readingBrowsePreparationFailure(w, err)
		return
	}
	check := func() (catalog.Viewer, error) {
		v, e := d.readingCountViewer(r.Context(), p, library)
		if e != nil {
			return v, e
		}
		basis, e := d.Catalog.WithContext(r.Context()).ReadingBrowseCountBasis(v, library)
		if e == nil && (basis != result.Basis || d.readingCountCaller(p, v, library) != caller) {
			e = readprep.ErrStale
		}
		return v, e
	}
	viewer, err = check()
	if err != nil {
		store.Cancel(r.Context(), caller, readprep.Handle(id))
		readingBrowsePreparationFailure(w, err)
		return
	}
	if result.State == readprep.Preparing {
		// Path escaping expands each source byte at most threefold. Reserve
		// envelope/header construction and serialization before allocating them.
		locationBytes := int64(len("/v1/libraries/") + 3*len(library) + len("/reading-browse-queries/") + 3*len(id))
		if err = result.ResizeDelivery(4096 + 4*locationBytes); err != nil {
			readingBrowsePreparationFailure(w, err)
			return
		}
		if _, err = check(); err != nil {
			readingBrowsePreparationFailure(w, err)
			return
		}
		retryMs, retrySeconds := readingBrowseRetryMs, "1"
		if grace > 0 {
			retryMs, retrySeconds = readingInitialRetryMs, "0"
		}
		w.Header().Set("Retry-After", retrySeconds)
		w.Header().Set("Location", "/v1/libraries/"+url.PathEscape(library)+"/reading-browse-queries/"+url.PathEscape(id))
		write(w, 202, variantBody{"preparing", ReadingBrowsePreparing{Kind: "reading-browse-preparing", State: "preparing", QueryID: id, RetryAfterMs: retryMs}})
		return
	}
	// Charge typed seed decoding and its backing strings before hydration.
	seedBytes := int64(len(result.Payload))*8 + 32768
	if err = result.ResizeDelivery(seedBytes); err != nil {
		readingBrowsePreparationFailure(w, err)
		return
	}
	var payload catalog.ReadingBrowseCountPayload
	if err = json.Unmarshal(result.Payload, &payload); err != nil || payload.Version != 1 || payload.Request.Library != library || payload.Count.Witness != result.Basis {
		readingBrowsePreparationFailure(w, readprep.ErrStale)
		return
	}
	payload.Count.ExpiresAt = result.OriginalExpiry.Unix()

	var page catalog.BrowseResult
	err = d.compose(r, viewer.EffectiveRestrictions(), func(cat *catalog.Service) error {
		live, e := d.readingBrowseViewer(cat.Context(), p, library, viewer)
		if e != nil {
			return e
		}
		viewer = live
		personal := d.readingCountPersonal(cat.Context(), p, library)
		reserve := func(peak int64, _ bool) error {
			return result.ResizeDelivery(seedBytes + peak)
		}
		page, e = cat.BrowsePreparedReadingCountPayload(viewer, payload, result.OriginalExpiry, d.Identity.ID(), personal, reserve)
		return e
	})
	if err != nil {
		readingBrowsePreparationFailure(w, err)
		return
	}
	if _, err = check(); err != nil {
		readingBrowsePreparationFailure(w, err)
		return
	}
	// Source-length reservations include JSON escaping and remain live through
	// serialization; even Preparing/error paths own and release the Poll lease.
	write(w, 200, variantBody{"ready", page})
}
