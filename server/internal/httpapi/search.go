package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (d Dependencies) searchScope(r *http.Request) (identity.Principal, bool, []string, bool, string, error) {
	p, e := d.principal(r)
	if e != nil {
		return p, false, nil, false, "", e
	}
	all, ids, policy, restriction, e := d.Hosted.SearchAccess(p)
	if e != nil {
		return p, false, nil, false, "", e
	}
	sort.Strings(ids)
	raw, _ := json.Marshal(ids)
	_, contentFence, e := d.viewerRestrictions(r, p)
	if e != nil {
		return p, false, nil, false, "", e
	}
	// Service presence is not permission. Use the same current profile authority
	// as Guide, and fence this decision so revocation during composition also
	// withholds the response, even if a content-policy cache is still warm.
	liveTV := false
	if d.LiveChannels != nil {
		e = d.featureAllowed(r.Context(), p, "live_tv")
		if e != nil && !errors.Is(e, errFeatureRestricted) {
			return p, false, nil, false, "", e
		}
		liveTV = e == nil
	}
	fence := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d:%d:%t:%s:%s:%t", presentationIdentity(p), p.Epoch, policy, restriction, all, raw, contentFence, liveTV))))
	return p, all, ids, liveTV, fence, nil
}

// commaList bounds a comma separated request parameter. An empty element is a
// request error rather than a silently dropped filter.
func commaList(raw string, max int) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > max {
		return nil, catalog.ErrSearchQuery
	}
	out := []string{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || len(part) > 256 {
			return nil, catalog.ErrSearchQuery
		}
		out = append(out, part)
	}
	return out, nil
}

func (d Dependencies) searchRoutes(mux *http.ServeMux) {
	d.httpHandle(mux, "GET /v1/search/genres", d.searchGenresHandler)
	if d.registry == nil {
		mux.HandleFunc("GET /v1/search", d.searchHandler)
	}
	d.httpHandle(mux, "GET /v1/search/history", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.principal(r)
		if e != nil {
			failure(w, e)
			return
		}
		if len(r.URL.Query()) != 0 {
			failure(w, catalog.ErrSearchQuery)
			return
		}
		out, e := d.Catalog.WithContext(r.Context()).SearchHistory(p.Viewer)
		if e != nil {
			failure(w, e)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		write(w, 200, out)
	})
	d.httpHandle(mux, "DELETE /v1/search/history", func(w http.ResponseWriter, r *http.Request) {
		p, e := d.principal(r)
		if e != nil {
			failure(w, e)
			return
		}
		// No query clears all, exactly as before. One `query` names the retained entry,
		// by the query text the viewer was shown; the catalogue resolves the same
		// canonical identity RecordSearch stored, never a list position.
		values, named := r.URL.Query()["query"]
		var out catalog.SearchHistoryPage
		if named {
			if len(r.URL.Query()) != 1 || len(values) != 1 || strings.TrimSpace(values[0]) == "" || len(values[0]) > 512 {
				failure(w, catalog.ErrSearchQuery)
				return
			}
			out, e = d.Catalog.WithContext(r.Context()).DeleteSearchHistory(p.Viewer, values[0])
		} else {
			if len(r.URL.Query()) != 0 {
				failure(w, catalog.ErrSearchQuery)
				return
			}
			out, e = d.Catalog.WithContext(r.Context()).ClearSearchHistory(p.Viewer)
		}
		if e != nil {
			failure(w, e)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		write(w, 200, out)
	})
}

func (d Dependencies) searchHandler(w http.ResponseWriter, r *http.Request) {
	// One request budget above the per-group budget, so a whole-response
	// timeout only happens when every group is slow at once.
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	q := r.URL.Query()
	allowed := map[string]bool{"q": true, "group": true, "groups": true, "cursor": true, "limit": true, "sort": true, "direction": true, "libraryIds": true, "record": true}
	for key, values := range q {
		if len(values) != 1 || !allowed[key] {
			failure(w, catalog.ErrSearchQuery)
			return
		}
	}
	limit := 40
	var e error
	if q.Has("limit") {
		limit, e = strconv.Atoi(q.Get("limit"))
		if e != nil || limit < 1 {
			failure(w, catalog.ErrSearchQuery)
			return
		}
	}
	limit = min(limit, 40)
	if q.Has("record") && q.Get("record") != "1" && q.Get("record") != "0" {
		failure(w, catalog.ErrSearchQuery)
		return
	}
	groups, e := commaList(q.Get("groups"), 16)
	if e != nil {
		failure(w, e)
		return
	}
	libraryIDs, e := commaList(q.Get("libraryIds"), 64)
	if e != nil {
		failure(w, e)
		return
	}
	p, all, ids, liveTV, fence, e := d.searchScope(r)
	if e != nil {
		failure(w, e)
		return
	}
	offerLibraries, offerFence, e := d.homeScopeFor(r, p)
	if e != nil {
		failure(w, e)
		return
	}
	if all {
		ids = offerLibraries
	}

	restrictions, _, e := d.viewerRestrictions(r, p)
	if e != nil {
		failure(w, e)
		return
	}
	// Search is the most expensive read the server serves, and a client that
	// types fast will fire several in a second. Cap it per viewer as well as
	// globally, and say so plainly rather than letting the queue grow.
	if d.admission != nil {
		done, ok := d.admission.acquireSearch(identity.PersonalKey(p.Viewer))
		if !ok {
			w.Header().Set("Retry-After", "1")
			write(w, 429, map[string]any{"error": map[string]any{"code": "search_busy", "message": "Too many searches are already running. Try again in a moment.", "retryable": true}})
			return
		}
		defer done()
	}
	var out catalog.SearchEnvelope
	e = d.compose(r.WithContext(ctx), restrictions, func(cat *catalog.Service) error {
		var composeErr error
		viewer := catalog.Viewer{Profile: identity.PersonalKey(p.Viewer), Fence: fence, Libraries: ids, Restrictions: restrictions}
		out, composeErr = cat.Search(cat.Context(), catalog.SearchRequest{Viewer: viewer, ServerID: d.Identity.ID(), Profile: identity.PersonalKey(p.Viewer), ViewerFence: fence, Q: q.Get("q"), Group: q.Get("group"), Cursor: q.Get("cursor"), Sort: q.Get("sort"), Direction: q.Get("direction"), Limit: limit, AllLibraries: all, Libraries: ids, LibraryIDs: libraryIDs, Groups: groups, LiveTV: liveTV, Record: q.Get("record") == "1", Restrictions: restrictions})
		if composeErr != nil {
			return composeErr
		}
		rows := make([]catalog.HomeRow, 0, len(out.Groups))
		for _, group := range out.Groups {
			if group.EntityKind != "person" && group.EntityKind != "channel" {
				rows = append(rows, catalog.HomeRow{Entries: group.Items})
			}
		}
		return d.projectHomeActions(cat.Context(), p, viewer, cat, rows, nil)
	})
	if e != nil {
		if errors.Is(e, context.DeadlineExceeded) || errors.Is(e, context.Canceled) {
			write(w, 503, map[string]any{"error": map[string]any{"code": "search_unavailable", "message": "Search timed out or was cancelled. Refine the query and retry.", "retryable": true}})
			return
		}
		failure(w, e)
		return
	}
	_, _, _, _, after, e := d.searchScope(r)
	if e != nil {
		failure(w, e)
		return
	}
	if after != fence {
		failure(w, catalog.ErrStaleContinuation)
		return
	}
	// Command offers also prove the original device family after composition.
	// The Home fence is not compared with Search's independent content fence.
	if afterOffers, offerErr := d.homeActionFenceAfter(r, p); offerErr != nil {
		failure(w, offerErr)
		return
	} else if afterOffers != offerFence {
		failure(w, catalog.ErrStaleContinuation)
		return
	}
	// History records only a committed search (`record=1`, sent when the viewer
	// submits or opens a result), never typeahead pages or continuations.
	out.Query.Recorded = false
	if q.Get("record") == "1" && q.Get("cursor") == "" {
		if e = d.Catalog.WithContext(r.Context()).RecordSearch(p.Viewer, q.Get("q")); e != nil {
			failure(w, e)
			return
		}
		out.Query.Recorded, _ = d.Catalog.WithContext(r.Context()).RemembersSearchHistory(p.Viewer)
	}
	write(w, 200, out)
}
