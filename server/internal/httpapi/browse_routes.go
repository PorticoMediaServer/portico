package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
	"reflect"
	"strconv"
)

// Browse engine routes. Everything a client can express in the library — the
// vocabulary, the expression query, facet counts, pin order and pinned libraries
// — is published here, so a third-party player can drive the same surfaces the
// first-party clients do.

// browseFailure answers a validation error with the offending path so a client
// can attach the message to the control that produced it.
func browseFailure(w http.ResponseWriter, e error) {
	if isBrowseSelectionPreparationError(e) {
		browseSelectionPreparationFailure(w, e)
		return
	}
	if errors.Is(e, catalog.ErrReadingProjectionChanged) {
		policyError(w, "revision_mismatch")
		return
	}
	var issue *catalog.BrowseValidationError
	if errors.As(e, &issue) {
		write(w, 400, map[string]any{"error": map[string]any{"code": "invalid_query", "message": issue.Message, "field": issue.Path, "retryable": false}})
		return
	}
	if errors.Is(e, catalog.ErrBrowsePivot) {
		write(w, 400, map[string]any{"error": map[string]any{"code": "invalid_pivot", "message": publicErrorMessage("invalid_pivot"), "field": "pivot", "retryable": false}})
		return
	}
	if errors.Is(e, catalog.ErrPinOrderConflict) || errors.Is(e, catalog.ErrLibraryNavigationConflict) {
		write(w, 409, map[string]any{"error": map[string]any{"code": "pin_order_conflict", "message": publicErrorMessage("pin_order_conflict"), "retryable": true}})
		return
	}
	failure(w, e)
}

// browseFence folds the authorization facts into one opaque value; a permission
// change invalidates an open range exactly as a catalog change does.
func (d Dependencies) browseFence(r *http.Request, p identity.Principal) (string, error) {
	var policy, restriction int64
	if p.Authority != "local" {
		if e := dbwork.QueryRow(r.Context(), d.DB, `SELECT COALESCE((SELECT revision FROM policy WHERE server_id=?),0),COALESCE((SELECT revision FROM restrictions WHERE profile_id=?),0)`, d.Identity.ID(), p.ProfileID).Scan(&policy, &restriction); e != nil {
			return "", e
		}
	}
	_, contentFence, e := d.viewerRestrictions(r, p)
	if e != nil {
		return "", e
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d:%d:%s", presentationIdentity(p), p.Epoch, policy, restriction, contentFence)))), nil
}

func (d Dependencies) browseLibrary(w http.ResponseWriter, r *http.Request) (identity.Principal, bool) {
	p, e := d.principal(r)
	if e == nil {
		e = d.allowedLibrary(r.Context(), p, r.PathValue("id"))
	}
	if e != nil {
		failure(w, e)
		return p, false
	}
	return p, true
}

type browseRequestBody struct {
	SelectionBasis string                        `json:"selectionBasis,omitempty"`
	HomeRowDisplay string                        `json:"homeRowDisplay,omitempty"`
	Pivot          string                        `json:"pivot"`
	Query          json.RawMessage               `json:"query"`
	Sort           []catalog.BrowseSortSelection `json:"sort"`
	Limit          int                           `json:"limit"`
	Cursor         string                        `json:"cursor"`
	Range          *catalog.BrowseRange          `json:"range"`
	Seek           *catalog.BrowseSeek           `json:"seek"`
}

func (body *browseRequestBody) UnmarshalJSON(raw []byte) error {
	type wire browseRequestBody
	var value wire
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	if err := catalog.ValidateSelectionBasisJSON(raw); err != nil {
		return err
	}
	*body = browseRequestBody(value)
	return nil
}
func (browseRequestBody) JSONSchema() map[string]any {
	type wire browseRequestBody
	shape, _ := apikit.Schema(reflect.TypeFor[wire]())
	shape["properties"].(map[string]any)["selectionBasis"].(map[string]any)["pattern"] = catalog.SelectionBasisExpression
	return shape
}

func (d Dependencies) browseRoutes(mux *http.ServeMux) {
	d.httpHandle(mux, "GET /v1/libraries/{id}/browse-capabilities", func(w http.ResponseWriter, r *http.Request) {
		p, ok := d.browseLibrary(w, r)
		if !ok {
			return
		}
		for key, values := range r.URL.Query() {
			if key != "pivot" || len(values) != 1 {
				failure(w, catalog.ErrCursor)
				return
			}
		}
		var out catalog.BrowseCapabilities
		var capabilityViewer catalog.Viewer
		e := d.compose(r, identity.ContentRestrictions{}, func(cat *catalog.Service) error {
			viewer, err := d.readingBrowseViewer(cat.Context(), p, r.PathValue("id"), catalog.Viewer{Libraries: []string{r.PathValue("id")}})
			if err != nil {
				return err
			}
			capabilityViewer = viewer
			out, err = cat.BrowseCapabilitiesForViewer(viewer, r.PathValue("id"), r.URL.Query().Get("pivot"))
			return err
		})
		if e != nil {
			browseFailure(w, e)
			return
		}
		if out.Library.Kind == "book" || out.Library.Kind == "comic" {
			if err := d.browseAuthorityAfter(r, p, capabilityViewer); err != nil {
				browseFailure(w, err)
				return
			}
			if err := d.allowedLibrary(r.Context(), p, r.PathValue("id")); err != nil {
				failure(w, err)
				return
			}
		}
		write(w, 200, out)
	})
	if d.registry == nil {
		mux.HandleFunc("POST /v1/libraries/{id}/browse", d.browseHandler)
	}
	if d.registry == nil {
		mux.HandleFunc("GET /v1/libraries/{id}/facets", d.facetsHandler)
		mux.HandleFunc("POST /v1/browse", d.browseAcrossHandler)
		mux.HandleFunc("GET /v1/browse-capabilities", d.browseAcrossCapabilitiesHandler)
		mux.HandleFunc("GET /v1/browse/facets", d.browseAcrossFacetsHandler)
	}
	d.httpHandle(mux, "PUT /v1/saved-pins/order", func(w http.ResponseWriter, r *http.Request) {
		p, _, fence, e := d.homeScope(r)
		if e != nil {
			failure(w, e)
			return
		}
		var body struct {
			ExpectedRevision int64                   `json:"expectedRevision"`
			Order            []catalog.PinOrderEntry `json:"order"`
		}
		if e = decode(w, r, &body); e != nil {
			failure(w, e)
			return
		}
		out, e := d.Catalog.WithContext(r.Context()).SetPinOrder(resourceActor(p), body.ExpectedRevision, body.Order, func(tx *sql.Tx) error {
			return d.resourceAuthorize(tx, p, "")
		})
		if e != nil {
			browseFailure(w, e)
			return
		}
		d.savedResponse(w, r, fence, map[string]any{"serverId": d.Identity.ID(), "viewerFence": fence, "revision": out.Revision, "order": out.Order})
	})
	d.httpHandle(mux, "GET /v1/saved-pins/order", func(w http.ResponseWriter, r *http.Request) {
		p, _, fence, e := d.homeScope(r)
		if e != nil {
			failure(w, e)
			return
		}
		out, e := d.Catalog.WithContext(r.Context()).PinOrder(resourceActor(p))
		if e != nil {
			browseFailure(w, e)
			return
		}
		d.savedResponse(w, r, fence, map[string]any{"serverId": d.Identity.ID(), "viewerFence": fence, "revision": out.Revision, "order": out.Order})
	})
	d.httpHandle(mux, "GET /v1/me/library-navigation", func(w http.ResponseWriter, r *http.Request) {
		p, libraries, fence, e := d.homeScope(r)
		if e != nil {
			failure(w, e)
			return
		}
		visible := map[string]bool{}
		for _, library := range libraries {
			visible[library] = true
		}
		out, e := d.Catalog.WithContext(r.Context()).LibraryNavigation(resourceActor(p), func(id string) bool { return visible[id] })
		if e != nil {
			browseFailure(w, e)
			return
		}
		d.savedResponse(w, r, fence, out)
	})
	d.httpHandle(mux, "PUT /v1/me/library-navigation", func(w http.ResponseWriter, r *http.Request) {
		p, _, fence, e := d.homeScope(r)
		if e != nil {
			failure(w, e)
			return
		}
		var body struct {
			ExpectedRevision int64    `json:"expectedRevision"`
			PinnedLibraryIDs []string `json:"pinnedLibraryIds"`
		}
		if e = decode(w, r, &body); e != nil {
			failure(w, e)
			return
		}
		out, e := d.Catalog.SetLibraryNavigation(resourceActor(p), body.ExpectedRevision, body.PinnedLibraryIDs, func(tx *sql.Tx, library string) error {
			return d.resourceAuthorize(tx, p, library)
		})
		if e != nil {
			browseFailure(w, e)
			return
		}
		d.savedResponse(w, r, fence, out)
	})
}

func (d Dependencies) browseHandler(w http.ResponseWriter, r *http.Request) {
	p, ok := d.browseLibrary(w, r)
	if !ok {
		return
	}
	if d.PrepareReadingBrowse {
		var kind string
		if err := d.DB.QueryRowContext(r.Context(), `SELECT kind FROM libraries WHERE id=?`, r.PathValue("id")).Scan(&kind); err != nil {
			browseFailure(w, err)
			return
		}
		if kind == "book" || kind == "comic" {
			d.readingBrowsePreparedHandler(w, r)
			return
		}
	}
	var body browseRequestBody
	if e := decode(w, r, &body); e != nil {
		failure(w, e)
		return
	}
	if e := catalog.ValidateHomeRowDisplayQueryLimit(body.HomeRowDisplay, body.Limit); e != nil {
		browseFailure(w, e)
		return
	}
	query, e := catalog.ParseBrowseQuery(body.Query, "query")
	if e != nil {
		browseFailure(w, e)
		return
	}
	if e = catalog.ValidateHomeRowDisplay(query, body.HomeRowDisplay); e != nil {
		browseFailure(w, e)
		return
	}
	fence, e := d.browseFence(r, p)
	if e != nil {
		failure(w, e)
		return
	}
	restrictions, _, e := d.viewerRestrictions(r, p)
	if e != nil {
		failure(w, e)
		return
	}
	// One read snapshot for the whole page: the count, the letter index, the
	// anchor rank and the rows are then four views of one state rather than
	// four consecutive states, which is what the second revision read inside
	// browseSelect existed to detect.
	var out catalog.BrowseResult
	var selectionViewer catalog.Viewer
	e = d.compose(r, restrictions, func(cat *catalog.Service) error {
		viewer := catalog.Viewer{Profile: identity.PersonalKey(p.Viewer), Fence: fence, Libraries: []string{r.PathValue("id")}, Restrictions: restrictions}
		viewer, composeErr := d.readingBrowseViewer(cat.Context(), p, r.PathValue("id"), viewer)
		if composeErr != nil {
			return composeErr
		}
		selectionViewer = viewer
		rows, composeErr := d.resolveHomeRows(r, p, viewer, query, cat)
		if composeErr != nil {
			return composeErr
		}
		limit := min(body.Limit, catalog.BrowseMaximumLimit)
		if body.SelectionBasis != "" {
			limit = min(body.Limit, catalog.BrowseSelectionConversionLimit)
		}
		browseRequest := catalog.BrowseRequest{SelectionBasis: body.SelectionBasis, ServerID: d.Identity.ID(), Library: r.PathValue("id"), Profile: viewer.Profile, ViewerFence: viewer.Fence, HomeRowDisplay: body.HomeRowDisplay, Pivot: body.Pivot, Query: query, Sort: body.Sort, Limit: limit, Cursor: body.Cursor, Range: body.Range, Seek: body.Seek, Restrictions: viewer.EffectiveRestrictions(), RowSources: rows}
		if identity.CanUsePersonalState(p) {
			browseRequest.ReadingPersonalAuthorize = func(tx *sql.Tx) error {
				live, err := d.Identity.PrincipalByDigestTx(cat.Context(), tx, p.Hash)
				if err != nil {
					return err
				}
				if live.Viewer != p.Viewer || live.Epoch != p.Epoch || live.Grant != p.Grant || !identity.CanUsePersonalState(live) {
					return identity.ErrForbidden
				}
				return d.allowedLibraryTx(cat.Context(), live, r.PathValue("id"), tx)
			}
		}
		out, composeErr = cat.BrowseEntities(viewer, browseRequest)
		if composeErr != nil {
			return composeErr
		}
		if composeErr = d.projectBrowseDisplay(r, p, viewer, cat, body.HomeRowDisplay, query, &out); composeErr != nil {
			return composeErr
		}
		return d.attachWholeSelection(r, p, viewer, cat, browseRequest, body, &out)
	})
	defer out.Release()
	if e != nil {
		browseFailure(w, e)
		return
	}
	if out.WholeSelectionPreparing != nil {
		var access catalog.SelectionPreparationAccess
		access, e = d.browseSelectionAccess(p, r.PathValue("id"), selectionViewer)
		if e == nil {
			e = d.browseSelectionExposure(r, p, selectionViewer, out.WholeSelectionPreparing.CapturedBasis(), access)
		}
		if e != nil {
			if store := d.Catalog.ReadPreparations(); store != nil && access.Caller != "" {
				_ = store.Cancel(context.WithoutCancel(r.Context()), access.Caller, readprep.Handle(out.WholeSelectionPreparing.QueryID))
			}
			browseSelectionPreparationFailure(w, e)
			return
		}
	} else if e = d.browseAuthorityAfter(r, p, catalog.Viewer{Fence: fence}); e != nil {
		browseFailure(w, e)
		return
	}

	// Authorization is proven again after the read so a revocation that landed
	// mid-request cannot be answered with catalogue facts.
	if e = d.allowedLibrary(r.Context(), p, r.PathValue("id")); e != nil {
		failure(w, e)
		return
	}
	write(w, 200, out)
}

func (d Dependencies) facetsHandler(w http.ResponseWriter, r *http.Request) {
	p, ok := d.browseLibrary(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	for key, values := range q {
		if (key != "field" && key != "q" && key != "limit" && key != "offset" && key != "pivot" && key != "query" && key != "after" && key != "before" && key != "at") || len(values) != 1 {
			failure(w, catalog.ErrCursor)
			return
		}
	}
	limit := 0
	if q.Has("limit") {
		parsed, e := strconv.Atoi(q.Get("limit"))
		if e != nil || parsed < 1 {
			failure(w, catalog.ErrCursor)
			return
		}
		limit = min(parsed, 200)
	}
	offset := 0
	if q.Has("offset") {
		parsed, err := strconv.Atoi(q.Get("offset"))
		if err != nil || parsed < 0 {
			failure(w, catalog.ErrCursor)
			return
		}
		offset = parsed
	}
	restrictions, _, e := d.viewerRestrictions(r, p)
	restrictionFence, fenceErr := d.browseFence(r, p)
	if e == nil {
		e = fenceErr
	}
	if e != nil {
		failure(w, e)
		return
	}
	query, e := catalog.ParseBrowseQuery(json.RawMessage(q.Get("query")), "query")
	if e != nil {
		browseFailure(w, e)
		return
	}
	request := catalog.FacetRequest{Pivot: q.Get("pivot"), Query: query, Library: r.PathValue("id"), Profile: identity.PersonalKey(p.Viewer), Field: q.Get("field"), Q: q.Get("q"), Limit: limit, Offset: offset, Restrictions: restrictions,
		After: q.Get("after"), Before: q.Get("before"), At: q.Get("at"), LimitPresent: q.Has("limit"), OffsetPresent: q.Has("offset")}
	simple, e := d.Catalog.WithContext(r.Context()).IsSimpleFacetRequest(request)
	if e != nil {
		browseFailure(w, e)
		return
	}
	anchors := 0
	for _, name := range []string{"after", "before", "at"} {
		if q.Has(name) {
			anchors++
			if !validFacetQueryID(q.Get(name)) {
				browseFailure(w, catalog.ErrCursor)
				return
			}
		}
	}
	if anchors > 1 || simple && q.Has("offset") || !simple && anchors != 0 {
		browseFailure(w, catalog.ErrCursor)
		return
	}
	// Reading preparation owns its own short snapshots and must never inherit
	// the foreground composition transaction across worker/poll turns.
	if d.PrepareReadingFacets {
		var kind string
		if err := d.DB.QueryRowContext(r.Context(), `SELECT kind FROM libraries WHERE id=?`, r.PathValue("id")).Scan(&kind); err != nil {
			browseFailure(w, err)
			return
		}
		if kind == "book" || kind == "comic" {
			d.prepareReadingFacets(w, r, catalog.FacetRequest{Pivot: q.Get("pivot"), Query: query, Field: q.Get("field"), Q: q.Get("q"), Limit: limit, Offset: offset})
			return
		}
	}
	if simple {
		viewer := catalog.Viewer{Profile: request.Profile, Fence: restrictionFence, Libraries: []string{request.Library}, Restrictions: restrictions}
		d.simpleFacetsHandler(w, r, p, viewer, request)
		return
	}
	var out catalog.FacetPage
	e = d.compose(r, restrictions, func(cat *catalog.Service) error {
		var composeErr error
		viewer := catalog.Viewer{Profile: identity.PersonalKey(p.Viewer), Fence: restrictionFence, Libraries: []string{r.PathValue("id")}, Restrictions: restrictions}
		viewer, composeErr = d.readingBrowseViewer(cat.Context(), p, r.PathValue("id"), viewer)
		if composeErr != nil {
			return composeErr
		}
		rows, err := d.resolveHomeRows(r, p, viewer, query, cat)
		if err != nil {
			return err
		}
		out, composeErr = cat.Facets(viewer, catalog.FacetRequest{Pivot: q.Get("pivot"), Query: query, RowSources: rows, Library: r.PathValue("id"), Profile: viewer.Profile, Field: q.Get("field"), Q: q.Get("q"), Limit: limit, Offset: offset, Restrictions: viewer.EffectiveRestrictions()})
		return composeErr
	})
	if e != nil {
		browseFailure(w, e)
		return
	}
	if e = d.allowedLibrary(r.Context(), p, r.PathValue("id")); e != nil {
		failure(w, e)
		return
	}
	if e = d.browseAuthorityAfter(r, p, catalog.Viewer{Fence: restrictionFence}); e != nil {
		browseFailure(w, e)
		return
	}
	write(w, 200, out)
}

// projectBrowseDisplay keeps canonical membership for every browse operation,
// while the explicitly requested cards and their command offers name actual
// targets. No stale member is skipped or substituted.
// attachWholeSelection adds the exact whole-view aggregate to the first page of
// a view whose job replay is provably identical. Every other page, and every
// view the job compiler cannot reproduce, carries no field at all.
func (d Dependencies) attachWholeSelection(r *http.Request, p identity.Principal, viewer catalog.Viewer, cat *catalog.Service, request catalog.BrowseRequest, body browseRequestBody, out *catalog.BrowseResult) error {
	if d.PrepareBrowseSelections {
		return d.attachWholeSelectionPreparation(r, p, viewer, cat, request, body, out)
	}
	if out.Bound == nil || out.PageInfo.Start != 0 || body.Cursor != "" || body.Range != nil || body.Seek != nil || body.HomeRowDisplay != "" {
		return nil
	}
	authority, err := d.homeActionAuthority(cat.Context(), p)
	if err != nil {
		return err
	}
	whole, err := cat.BrowseSelectionAggregate(request, viewer, *out.Bound, authority)
	if err != nil {
		return err
	}
	// An omitted aggregate has no library: the view is not replayable.
	if whole.Selector.Query.LibraryID == "" {
		return nil
	}
	out.WholeSelection = &whole
	return nil
}

func (d Dependencies) projectBrowseDisplay(r *http.Request, p identity.Principal, viewer catalog.Viewer, cat *catalog.Service, rowID string, query *catalog.BrowseNode, out *catalog.BrowseResult) error {
	if rowID == "" {
		// A library page's containers carry their aggregate selection offers;
		// the handler's post-read family fence covers them (MOB-030).
		entries := make([]*catalog.ContentEntry, len(out.Entries))
		for i := range out.Entries {
			entries[i] = &out.Entries[i]
		}
		return d.projectContainerActions(cat.Context(), p, viewer, cat, r.PathValue("id"), entries)
	}
	if err := catalog.ValidateHomeRowDisplay(query, rowID); err != nil {
		return err
	}
	rr := r.WithContext(cat.Context())
	request, err := d.homeRequestFor(rr, p, viewer.Restrictions)
	if err != nil {
		return err
	}
	ids := make([]string, len(out.Entries))
	for i, entry := range out.Entries {
		ids[i] = entry.ID
	}
	projected, err := cat.ProjectHomeRowDisplayTargets(request, rowID, ids)
	if err != nil {
		return err
	}
	if len(projected) != len(ids) {
		return catalog.ErrStaleContinuation
	}
	out.DisplayTargets = make([]catalog.HomeDisplayTarget, len(projected))
	for i, target := range projected {
		if target.MembershipID != ids[i] {
			return catalog.ErrStaleContinuation
		}
		out.Entries[i] = target.Entry
		out.DisplayTargets[i] = catalog.HomeDisplayTarget{MembershipID: target.MembershipID, EntryID: target.Entry.ID}
	}
	return d.projectHomeActions(cat.Context(), p, request.Viewer, cat, []catalog.HomeRow{{Entries: out.Entries}}, nil)
}

func registerBrowseCountRoutes(registry *apikit.Registry, d Dependencies) error {
	failures := []string{"invalid_request", "invalid_query", "unauthorized", "not_permitted", "not_found", "stale_continuation", "internal"}
	if err := apikit.RegisterRaw(registry, apikit.RawRoute[catalog.BrowseCountRequest, catalog.BrowseCountBatch]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneBrowsing, ID: "browse_library_counts", Method: "POST", Path: "/v1/libraries/{id}/browse/counts", Summary: "Read bounded counts for filter relax suggestions", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Cost: apikit.PageSized, Status: 200, BodyLimit: 65536, Errors: append(append([]string{}, failures...), "revision_mismatch")}, Serve: d.browseCountsHandler}); err != nil {
		return err
	}
	return apikit.RegisterRaw(registry, apikit.RawRoute[catalog.BrowseCountRequest, catalog.BrowseCountBatch]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneBrowsing, ID: "browse_across_counts", Method: "POST", Path: "/v1/browse/counts", Summary: "Read bounded group counts for filter relax suggestions", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Cost: apikit.PageSized, Status: 200, BodyLimit: 65536, Errors: failures}, Serve: d.browseCountsHandler})
}
func (d Dependencies) browseCountsHandler(w http.ResponseWriter, r *http.Request) {
	p, viewer, err := d.acrossViewer(r)
	if err != nil {
		failure(w, err)
		return
	}
	library := r.PathValue("id")
	if library != "" {
		if err = d.allowedLibrary(r.Context(), p, library); err != nil {
			failure(w, err)
			return
		}
	}
	var body catalog.BrowseCountRequest
	if err = decode(w, r, &body); err != nil {
		failure(w, err)
		return
	}
	if len(body.Queries) < 1 || len(body.Queries) > catalog.BrowseCountMaximum {
		browseFailure(w, &catalog.BrowseValidationError{Path: "queries", Message: "send between 1 and 8 count queries"})
		return
	}
	var out catalog.BrowseCountBatch
	err = d.composeScope(r, viewer.Restrictions, func(ctx context.Context, cat *catalog.Service) error {
		if library != "" {
			var e error
			viewer, e = d.readingBrowseViewer(ctx, p, library, viewer)
			if e != nil {
				return e
			}
		}
		sources := map[string]catalog.HomeRowMembership{}
		for _, q := range body.Queries {
			rows, e := d.resolveHomeRows(r.WithContext(ctx), p, viewer, q.Query, cat)
			if e != nil {
				return e
			}
			for id, row := range rows {
				sources[id] = row
			}
		}
		var e error
		out, e = cat.BrowseCounts(viewer, library, body, sources)
		return e
	})
	if err != nil {
		browseFailure(w, err)
		return
	}
	if err = d.browseAuthorityAfter(r, p, viewer); err != nil {
		browseFailure(w, err)
		return
	}
	write(w, 200, out)
}

// Read the current family and policy again after leaving the snapshot.
func (d Dependencies) browseAuthorityAfter(r *http.Request, p identity.Principal, before catalog.Viewer) error {
	if _, err := d.homeActionFenceAfter(r, p); err != nil {
		return err
	}
	_, after, err := d.acrossViewer(r)
	if err != nil {
		return err
	}
	// A library page's fence has no cross-library suffix; it still rechecks both
	// its content and session facts, and the explicit library check follows it.
	fence, err := d.browseFence(r, p)
	if err != nil {
		return err
	}
	if before.Fence != after.Fence && before.Fence != fence {
		return catalog.ErrStaleContinuation
	}
	return nil
}
