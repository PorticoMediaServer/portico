package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Lists across libraries (POST /v1/browse): a person's titles, a playlist, a
// personal collection, a Home row, My List, Favorites and the history, sorted
// and filtered as one list over every library the viewer may read. The same
// vocabulary as a library's own, published by GET /v1/browse-capabilities,
// with facet values counted over those libraries by GET /v1/browse/facets.

// acrossViewer is the viewer of a list across libraries: every library the
// principal may read, and a fence over both the browse authorization and that
// set, so a change to either invalidates an open list.
func (d Dependencies) acrossViewer(r *http.Request) (identity.Principal, catalog.Viewer, error) {
	p, e := d.principal(r)
	if e != nil {
		return p, catalog.Viewer{}, e
	}
	restrictions, _, e := d.viewerRestrictions(r, p)
	if e != nil {
		return p, catalog.Viewer{}, e
	}
	libraries, scopeFence, e := d.homeScopeFor(r, p)
	if e != nil {
		return p, catalog.Viewer{}, e
	}
	fence, e := d.browseFence(r, p)
	if e != nil {
		return p, catalog.Viewer{}, e
	}
	return p, catalog.Viewer{Profile: identity.PersonalKey(p.Viewer), Fence: fence + ":" + scopeFence, Libraries: libraries, Restrictions: restrictions}, nil
}

// resolveHomeRows reads the Home rows a query names through Home itself, so a
// homeRow condition holds exactly the titles the viewer's row shows.
func (d Dependencies) resolveHomeRows(r *http.Request, p identity.Principal, viewer catalog.Viewer, query *catalog.BrowseNode, cat *catalog.Service) (map[string]catalog.HomeRowMembership, error) {
	r = r.WithContext(cat.Context())
	ids := catalog.BrowseHomeRows(query)
	if len(ids) == 0 {
		return nil, nil
	}
	request, e := d.homeRequestFor(r, p, viewer.Restrictions)
	if e != nil {
		return nil, e
	}
	out := map[string]catalog.HomeRowMembership{}
	for _, id := range ids {
		members, e := cat.ResolveHomeRowMembership(request, id)
		if e != nil {
			return nil, e
		}
		out[id] = members
	}
	return out, nil
}

func (d Dependencies) browseAcrossHandler(w http.ResponseWriter, r *http.Request) {
	p, viewer, e := d.acrossViewer(r)
	if e != nil {
		failure(w, e)
		return
	}
	var body browseRequestBody
	if e = decode(w, r, &body); e != nil {
		failure(w, e)
		return
	}
	if body.SelectionBasis != "" {
		browseFailure(w, catalog.ErrStaleContinuation)
		return
	}
	if body.Seek != nil {
		browseFailure(w, &catalog.BrowseValidationError{Path: "seek", Message: "seek is unavailable for lists across libraries"})
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
	if e = catalog.ValidateHomeRowDisplayQueryLimit(body.HomeRowDisplay, body.Limit); e != nil {
		browseFailure(w, e)
		return
	}
	var out catalog.BrowseResult
	e = d.compose(r, viewer.Restrictions, func(cat *catalog.Service) error {
		rows, err := d.resolveHomeRows(r, p, viewer, query, cat)
		if err != nil {
			return err
		}
		out, err = cat.BrowseAcross(viewer, catalog.AcrossRequest{ServerID: d.Identity.ID(), HomeRowDisplay: body.HomeRowDisplay, Pivot: body.Pivot, Query: query, Sort: body.Sort, Limit: min(body.Limit, catalog.BrowseMaximumLimit), Cursor: body.Cursor, Range: body.Range, RowSources: rows})
		if err != nil {
			return err
		}
		return d.projectBrowseDisplay(r, p, viewer, cat, body.HomeRowDisplay, query, &out)
	})
	if e != nil {
		browseFailure(w, e)
		return
	}
	if e = d.browseAuthorityAfter(r, p, viewer); e != nil {
		browseFailure(w, e)
		return
	}
	write(w, 200, out)
}

func (d Dependencies) browseAcrossCapabilitiesHandler(w http.ResponseWriter, r *http.Request) {
	for key, values := range r.URL.Query() {
		if key != "pivot" || len(values) != 1 {
			failure(w, catalog.ErrCursor)
			return
		}
	}
	_, viewer, e := d.acrossViewer(r)
	if e != nil {
		failure(w, e)
		return
	}
	out, e := d.Catalog.WithContext(r.Context()).BrowseAcrossCapabilities(viewer, r.URL.Query().Get("pivot"))
	if e != nil {
		browseFailure(w, e)
		return
	}
	write(w, 200, out)
}

func (d Dependencies) browseAcrossFacetsHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	for key, values := range q {
		if (key != "field" && key != "q" && key != "limit" && key != "group" && key != "value" && key != "query" && key != "pivot") || len(values) != 1 {
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
	p, viewer, e := d.acrossViewer(r)
	if e != nil {
		failure(w, e)
		return
	}
	// A list's own facets: group names its condition (person, cast, crew,
	// list, savedCollection, homeRow, watchlisted, favorite or lastPlayedAt)
	// and value its subject, as the list's query names them.
	var group *catalog.BrowseNode
	if q.Has("group") {
		group = &catalog.BrowseNode{Field: q.Get("group"), Operator: "equals", Value: q.Get("value")}
		switch q.Get("group") {
		case "watchlisted", "favorite":
			group.Value = true
		case "lastPlayedAt":
			group.Operator, group.Value = "is-present", nil
		}
	}
	query, e := catalog.ParseBrowseQuery(json.RawMessage(q.Get("query")), "query")
	if e != nil {
		browseFailure(w, e)
		return
	}
	query = catalog.BrowseConjunction(group, query)
	var out catalog.FacetPage
	e = d.compose(r, viewer.Restrictions, func(cat *catalog.Service) error {
		var rows map[string]catalog.HomeRowMembership
		if query != nil {
			var err error
			if rows, err = d.resolveHomeRows(r, p, viewer, query, cat); err != nil {
				return err
			}
		}
		var err error
		out, err = cat.FacetsAcross(viewer, catalog.FacetRequest{Pivot: q.Get("pivot"), Query: query, Field: q.Get("field"), Q: q.Get("q"), Limit: limit, RowSources: rows})
		return err
	})
	if e != nil {
		browseFailure(w, e)
		return
	}
	if e = d.browseAuthorityAfter(r, p, viewer); e != nil {
		browseFailure(w, e)
		return
	}
	write(w, 200, out)
}
