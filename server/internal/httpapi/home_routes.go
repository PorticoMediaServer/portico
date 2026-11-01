package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"net/http"
	"strconv"
)

// Home is composed by the server: rows, their order, their policy and their
// paging shape all arrive from here so no client infers membership or ranking.
// One request renders the surface; each row then pages on its own endpoint.

func homeFailure(w http.ResponseWriter, e error) {
	if errors.Is(e, catalog.ErrHomeRowUnknown) {
		write(w, 404, map[string]any{"error": map[string]any{"code": "not_found", "message": publicErrorMessage("not_found"), "retryable": false}})
		return
	}
	if errors.Is(e, catalog.ErrHomeLayoutRow) {
		write(w, 400, map[string]any{"error": map[string]any{"code": "invalid_home_layout", "message": publicErrorMessage("invalid_home_layout"), "retryable": false}})
		return
	}
	failure(w, e)
}

// homeRequest resolves the viewer's visible libraries, their saved layout and
// the owner's community-activity policy in one place.
func (d Dependencies) homeRequest(r *http.Request) (identity.Principal, catalog.HomeRequest, error) {
	p, e := d.principal(r)
	if e != nil {
		return p, catalog.HomeRequest{}, e
	}
	restrictions, _, e := d.viewerRestrictions(r, p)
	if e != nil {
		return p, catalog.HomeRequest{}, e
	}
	request, e := d.homeRequestFor(r, p, restrictions)
	return p, request, e
}

// homeRequestFor is homeRequest with the viewer and their restriction already
// resolved, so it can run inside the caller's read snapshot.
func (d Dependencies) homeRequestFor(r *http.Request, p identity.Principal, restrictions identity.ContentRestrictions) (catalog.HomeRequest, error) {
	libraries, fence, privacy, e := d.homeScopeFences(r, p)
	if e != nil {
		return catalog.HomeRequest{}, e
	}
	viewer := catalog.Viewer{Profile: identity.PersonalKey(p.Viewer), Fence: fence, Libraries: libraries, Restrictions: restrictions}
	request := catalog.HomeRequest{Viewer: viewer, ServerID: d.Identity.ID(), Profile: identity.PersonalKey(p.Viewer), ViewerFence: fence, PrivacyFence: privacy, Libraries: libraries, Restrictions: restrictions}
	if identity.CanUsePersonalState(p) {
		request.ReadingPersonalAuthorize = func(tx *sql.Tx, library string) error {
			live, err := d.Identity.PrincipalByDigestTx(r.Context(), tx, p.Hash)
			if err != nil {
				return err
			}
			if live.Viewer != p.Viewer || live.Epoch != p.Epoch || live.Grant != p.Grant || !identity.CanUsePersonalState(live) {
				return identity.ErrForbidden
			}
			return d.allowedLibraryTx(r.Context(), live, library, tx)
		}
	}
	if d.LiveChannels != nil {
		if err := d.featureAllowed(r.Context(), p, "live_tv"); err == nil {
			request.LiveAvailable = true
			request.OnNow = func(page catalog.HomeRowPage) (catalog.HomeRow, error) { return d.homeOnNow(r, p, page) }
		}
	}
	if d.Console == nil {
		return request, nil
	}
	auth := d.consoleAuthority(p, false)
	layout, e := d.Console.HomeLayout(r.Context(), p, auth)
	if e != nil {
		return request, e
	}
	request.RowOrder, request.HiddenRowIDs, request.LayoutRevision = layout.RowOrder, layout.HiddenRowIDs, layout.Revision
	// Home needs stored policy, not tool validation or applied-runtime status.
	// Join composition's snapshot; direct layout callers get a bounded reader.
	var settings operations.SettingsDocument
	e = dbwork.WithReadSnapshot(r.Context(), d.Console.DB, func(ctx context.Context) error {
		var err error
		settings, err = d.Console.SettingsTx(ctx, dbwork.Snapshot(ctx), auth)
		return err
	})
	if e != nil {
		return request, e
	}
	request.CommunityActivity = settings.Effective.CommunityActivityEnabled
	return request, nil
}

// homeLimitQuery is the published contract homeQueryLimit keeps.
var homeLimitQuery = apikit.QueryInteger{Name: "limit", Minimum: 1, Default: catalog.HomeRowDefaultLimit, Maximum: catalog.HomeRowMaxLimit, Clamped: true, EmptyIsDefault: true}

// communityActivity is the owner's stored community-activity policy, which
// Viewers also watched follows as Home's Popular on this server does. Only the
// stored policy is read: the full Settings projection would run unrelated
// tool validation.
func (d Dependencies) communityActivity(r *http.Request, p identity.Principal) (bool, error) {
	var settings operations.SettingsDocument
	err := dbwork.WithReadSnapshot(r.Context(), d.Console.DB, func(ctx context.Context) error {
		var err error
		settings, err = d.Console.SettingsTx(ctx, dbwork.Snapshot(ctx), d.consoleAuthority(p, false))
		return err
	})
	return settings.Effective.CommunityActivityEnabled, err
}

func homeQueryLimit(r *http.Request) (int, error) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return 0, nil
	}
	limit, e := strconv.Atoi(raw)
	if e != nil || limit < 1 {
		return 0, catalog.ErrCursor
	}
	return min(limit, catalog.HomeRowMaxLimit), nil
}

func homeAllowedQuery(r *http.Request, allowed ...string) error {
	for key, values := range r.URL.Query() {
		ok := false
		for _, name := range allowed {
			ok = ok || key == name
		}
		if !ok || len(values) != 1 || len(values[0]) > 4096 {
			return catalog.ErrCursor
		}
	}
	return nil
}

func (d Dependencies) homeRoutes(mux *http.ServeMux) {

	// CON-23: every row this viewer can arrange, empty or not, with the saved layout.
	d.httpHandle(mux, "GET /v1/home/layout", func(w http.ResponseWriter, r *http.Request) {
		if e := homeAllowedQuery(r); e != nil {
			homeFailure(w, e)
			return
		}
		_, request, e := d.homeRequest(r)
		if e != nil {
			homeFailure(w, e)
			return
		}
		var out catalog.HomeLayoutView
		e = d.compose(r, request.Restrictions, func(cat *catalog.Service) error {
			var composeErr error
			out, composeErr = cat.HomeLayoutRows(request)
			return composeErr
		})
		if e != nil {
			homeFailure(w, e)
			return
		}
		write(w, 200, out)
	})
	d.httpHandle(mux, "PUT /v1/home/layout", func(w http.ResponseWriter, r *http.Request) {
		d.applyHomeLayout(w, r, false)
	})
	d.httpHandle(mux, "POST /v1/home/layout/reset", func(w http.ResponseWriter, r *http.Request) {
		d.applyHomeLayout(w, r, true)
	})
	d.httpHandle(mux, "GET /v1/suggestions", func(w http.ResponseWriter, r *http.Request) {
		if e := homeAllowedQuery(r, "limit"); e != nil {
			homeFailure(w, e)
			return
		}
		limit, e := homeQueryLimit(r)
		if e != nil {
			homeFailure(w, e)
			return
		}
		p, request, e := d.homeRequest(r)
		if e != nil {
			homeFailure(w, e)
			return
		}
		request.Limit = limit
		var out catalog.Suggestions
		e = d.compose(r, request.Restrictions, func(cat *catalog.Service) error {
			var composeErr error
			out, composeErr = cat.Suggestions(request)
			return composeErr
		})
		if e != nil {
			homeFailure(w, e)
			return
		}
		after, e := d.homeFenceAfter(r, p)
		if e != nil {
			homeFailure(w, e)
			return
		}
		if after != request.ViewerFence {
			homeFailure(w, catalog.ErrStaleContinuation)
			return
		}
		write(w, 200, out)
	})
	d.httpHandle(mux, "GET /v1/items/{id}/recommendations", func(w http.ResponseWriter, r *http.Request) {
		if e := homeAllowedQuery(r, "limit"); e != nil {
			homeFailure(w, e)
			return
		}
		limit, e := homeQueryLimit(r)
		if e != nil {
			homeFailure(w, e)
			return
		}
		p, library, fence, e := d.detailAccess(r)
		if e != nil {
			homeFailure(w, e)
			return
		}
		restrictions, _, e := d.viewerRestrictions(r, p)
		if e != nil {
			homeFailure(w, e)
			return
		}
		var rows []catalog.HomeRow
		var revision catalog.ContentRevision
		viewer, e := d.catalogViewer(r, p, []string{library}, fence)
		if e != nil {
			homeFailure(w, e)
			return
		}
		community, e := d.communityActivity(r, p)
		if e != nil {
			homeFailure(w, e)
			return
		}
		e = d.compose(r, restrictions, func(cat *catalog.Service) error {
			var composeErr error
			rows, revision, composeErr = cat.ItemRecommendationRows(viewer, r.PathValue("id"), limit, community)
			return composeErr
		})
		if e != nil {
			homeFailure(w, e)
			return
		}
		_, after, e := d.detailFenceFor(r, p)
		if e != nil {
			homeFailure(w, e)
			return
		}
		if after != fence {
			homeFailure(w, catalog.ErrStaleContinuation)
			return
		}
		write(w, 200, map[string]any{"itemId": r.PathValue("id"), "rows": rows, "revision": revision})
	})
}

func (d Dependencies) applyHomeLayout(w http.ResponseWriter, r *http.Request, reset bool) {
	if d.Console == nil {
		failure(w, errors.New("home customisation is unavailable"))
		return
	}
	var body struct {
		ExpectedRevision json.RawMessage `json:"expectedRevision"`
		IdempotencyKey   string          `json:"idempotencyKey"`
		RowOrder         []string        `json:"rowOrder"`
		HiddenRowIDs     []string        `json:"hiddenRowIds"`
	}
	if e := decode(w, r, &body); e != nil {
		failure(w, e)
		return
	}
	var suppliedRevision *int64
	if len(body.ExpectedRevision) != 0 {
		if err := json.Unmarshal(body.ExpectedRevision, &suppliedRevision); err != nil {
			failure(w, errStrictJSON)
			return
		}
		if suppliedRevision == nil {
			failure(w, errRevisionRequired)
			return
		}
	} else if !reset {
		// RawMessage is not covered by the generic int64 presence decoder.
		// Keep PUT's existing missing-revision refusal explicitly.
		failure(w, errRevisionRequired)
		return
	}
	p, request, e := d.homeRequest(r)
	if e != nil {
		homeFailure(w, e)
		return
	}
	if !reset {
		if e = d.Catalog.ValidateHomeLayout(request, body.RowOrder, body.HiddenRowIDs); e != nil {
			homeFailure(w, e)
			return
		}
	}
	if body.IdempotencyKey == "" {
		body.IdempotencyKey = identity.Token()
	}
	expectedRevision := int64(0)
	if suppliedRevision != nil {
		expectedRevision = *suppliedRevision
	} else if reset {
		// Older clients sent only the reset key. An explicitly supplied revision
		// remains the editor's original revision, including a genuine zero.
		expectedRevision = request.LayoutRevision
	}
	change := operations.HomeLayoutChange{ExpectedRevision: expectedRevision, IdempotencyKey: body.IdempotencyKey,
		RowOrder: body.RowOrder, HiddenRowIDs: body.HiddenRowIDs, Reset: reset}
	out, e := d.Console.ApplyHomeLayout(r.Context(), p, d.consoleAuthority(p, false), change)
	if e != nil {
		if errors.Is(e, operations.ErrConflict) && !errors.Is(e, operations.ErrIdempotencyKeyReused) {
			write(w, 409, map[string]any{"error": map[string]any{"code": "home_layout_conflict", "message": publicErrorMessage("home_layout_conflict"), "retryable": false}})
			return
		}
		consoleError(w, e)
		return
	}
	write(w, 200, out)
}

func (d Dependencies) serveHomeDocument(w http.ResponseWriter, r *http.Request) {
	if e := homeAllowedQuery(r, "limit"); e != nil {
		homeFailure(w, e)
		return
	}
	limit, e := homeQueryLimit(r)
	if e != nil {
		homeFailure(w, e)
		return
	}
	// The viewer and their restriction come from the authority caches, so the
	// scope resolution, the validator and the whole composition can then run
	// on one read snapshot rather than on four.
	p, e := d.principal(r)
	if e != nil {
		homeFailure(w, e)
		return
	}
	restrictions, _, e := d.viewerRestrictions(r, p)
	if e != nil {
		homeFailure(w, e)
		return
	}
	var out catalog.HomeDocument
	var request catalog.HomeRequest
	e = d.composeScope(r, restrictions, func(ctx context.Context, cat *catalog.Service) error {
		var scopeErr error
		if request, scopeErr = d.homeRequestFor(r.WithContext(ctx), p, restrictions); scopeErr != nil {
			return scopeErr
		}
		request.Limit = limit
		// Catalogue composition may share a scan's bounded freshness window;
		// command offers and permission facts below are always current.
		var composeErr error
		out, composeErr = cat.HomeManifest(request)
		if composeErr != nil {
			return composeErr
		}
		return d.projectHomeActions(ctx, p, request.Viewer, cat, out.Rows, out.Spotlight)
	})
	if e != nil {
		homeFailure(w, e)
		return
	}
	after, e := d.homeActionFenceAfter(r, p)
	if e != nil {
		homeFailure(w, e)
		return
	}
	if after != request.ViewerFence {
		homeFailure(w, catalog.ErrStaleContinuation)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	write(w, 200, out)
}

func (d Dependencies) serveHomeRow(w http.ResponseWriter, r *http.Request) {
	if e := homeAllowedQuery(r, "limit", "cursor", "start", "revision", "anchorId", "group", "sourceId"); e != nil {
		homeFailure(w, e)
		return
	}
	limit, e := homeQueryLimit(r)
	if e != nil {
		homeFailure(w, e)
		return
	}
	if r.PathValue("id") != "on_now" && (r.URL.Query().Has("group") || r.URL.Query().Has("sourceId")) {
		homeFailure(w, catalog.ErrCursor)
		return
	}
	page := catalog.HomeRowPage{Group: r.URL.Query().Get("group"), SourceID: r.URL.Query().Get("sourceId"), Limit: limit, Cursor: r.URL.Query().Get("cursor"), Revision: r.URL.Query().Get("revision"), AnchorID: r.URL.Query().Get("anchorId")}
	if raw := r.URL.Query().Get("start"); raw != "" {
		start, err := strconv.Atoi(raw)
		if err != nil || start < 0 || start > 100000 {
			homeFailure(w, catalog.ErrCursor)
			return
		}
		page.Start = start
	}
	p, e := d.principal(r)
	if e != nil {
		homeFailure(w, e)
		return
	}
	restrictions, _, e := d.viewerRestrictions(r, p)
	if e != nil {
		homeFailure(w, e)
		return
	}
	var request catalog.HomeRequest
	var out catalog.HomeRow
	// Offers depend on live command services and library settings as well as
	// catalogue revisions. Recompose them rather than returning a cached grant.
	e = d.composeScope(r, restrictions, func(ctx context.Context, cat *catalog.Service) error {
		var scopeErr error
		request, scopeErr = d.homeRequestFor(r.WithContext(ctx), p, restrictions)
		if scopeErr != nil {
			return scopeErr
		}
		// Live TV owns its directory/guide revision and final live reread.
		// Keep that reread off the catalogue WAL snapshot so source-policy
		// changes during guide composition still invalidate the response.
		if request.OnNow != nil {
			request.OnNow = func(page catalog.HomeRowPage) (catalog.HomeRow, error) { return d.homeOnNow(r, p, page) }
		}
		var composeErr error
		out, composeErr = cat.HomeSingleRow(request, r.PathValue("id"), page)
		if composeErr != nil {
			return composeErr
		}
		return d.projectHomeActions(ctx, p, request.Viewer, cat, []catalog.HomeRow{out}, nil)
	})
	if e != nil {
		homeFailure(w, e)
		return
	}
	after, e := d.homeActionFenceAfter(r, p)
	if e != nil {
		homeFailure(w, e)
		return
	}
	if after != request.ViewerFence {
		homeFailure(w, catalog.ErrStaleContinuation)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	write(w, 200, out)
}
