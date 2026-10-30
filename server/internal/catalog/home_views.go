package catalog

import (
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"strings"
)

// Saved views on Home. A viewer adds one by placing "view:<id>" in the Home
// layout's order (Customize Home › Add a saved view); the row is the view's
// browse (its library, pivot, filter and sort) under the view's name, its
// first page on Home, the whole list on the view's own page. A view the
// viewer can no longer read, or that no longer validates, is left out; its id
// stays harmlessly in the layout until the layout is next saved.

const homeViewPrefix = "view:"

// homeViewSpecs are the Home rows for the saved views the layout names.
func (s *Service) homeViewSpecs(r HomeRequest) []homeRowSpec {
	var out []homeRowSpec
	actor, ok := homeViewActor(r.Profile)
	if !ok {
		return nil
	}
	for i, id := range r.RowOrder {
		if !strings.HasPrefix(id, homeViewPrefix) {
			continue
		}
		view, err := s.SavedResource(r.ServerID, r.ViewerFence, strings.TrimPrefix(id, homeViewPrefix), actor)
		if err != nil || view.Kind != "view" || view.Definition == nil || view.Status != "ready" || !containsString(r.Libraries, view.Definition.LibraryID) {
			continue
		}
		lib, err := s.library(view.Definition.LibraryID)
		if err != nil {
			continue
		}
		// Relative saved rows must be read again within their freshness window.
		ttl := 60
		if browseRelative(view.Definition.Query) {
			ttl = 30
		}
		out = append(out, homeRowSpec{ID: id, Title: view.Name, Kind: "saved_view", ArtworkShape: homeArtwork(lib.Kind), LibraryID: view.Definition.LibraryID,
			SeeAll: &HomeSeeAll{Type: "saved_view", ResourceID: view.ID}, Priority: 90 + i, CacheTTL: ttl, Hideable: true, Reorderable: true, Cursors: true, Privacy: "personal", PolicyState: "available", Direction: "asc"})
	}
	return out
}

// homeViewSource is the saved browse as a server-side relation, including its
// exact filter and order. There is no first-60 member materialization.
func (s *Service) homeViewSource(r HomeRequest, spec homeRowSpec) (homeSource, error) {
	actor, ok := homeViewActor(r.Profile)
	if !ok {
		return homeSource{}, ErrHomeRowUnknown
	}
	view, err := s.SavedResource(r.ServerID, r.ViewerFence, strings.TrimPrefix(spec.ID, homeViewPrefix), actor)
	if err != nil || view.Definition == nil || view.Status != "ready" {
		return homeSource{}, ErrHomeRowUnknown
	}
	d := view.Definition
	s, err = s.withBrowseInstant(d.Query, "")
	if err != nil {
		return homeSource{}, err
	}
	if !r.Viewer.AllowsLibrary(d.LibraryID) {
		return homeSource{}, ErrHomeRowUnknown
	}
	library, err := s.library(d.LibraryID)
	if err != nil {
		return homeSource{}, err
	}
	pivot, ok := pivotForKind(library.Kind, d.Pivot)
	if !ok || !pivot.Browsable {
		return homeSource{}, ErrBrowsePivot
	}
	sorts, err := resolveBrowseSorts(pivot, d.Sort)
	if err != nil {
		return homeSource{}, err
	}
	where, args := browseScopeSQL(d.LibraryID, pivot)
	compiler := &browseCompiler{now: s.recommendationNow(s.browseInstant), profile: r.Profile, restrictions: r.Restrictions, count: s.countRows, ids: s.entityIDs, itemPivot: playablePivot(pivot)}
	for _, member := range entityKindMembers(pivot) {
		kind, err := compactcatalog.ParseKind(member)
		if err != nil {
			return homeSource{}, err
		}
		compiler.kinds = append(compiler.kinds, int(kind))
	}
	readingEligible, err := s.bindReadingBrowse(d.LibraryID, compiler)
	if err != nil {
		return homeSource{}, err
	}
	where += " AND " + readingEligible
	clause, err := compiler.compile(d.Query)
	if err != nil {
		return homeSource{}, err
	}
	where += " AND " + clause
	args = append(args, compiler.args...)
	visible, values := EntityRestrictionSQL("e.entity_id", r.Restrictions)
	where += " AND " + visible
	args = append(args, values...)
	from := `catalog_browse_rows e JOIN catalog_entities ce ON ce.id=e.entity_id`
	order, orderArgs := "", []any{}
	if sorts[0].Field == "groupOrder" {
		group, groupArgs, err := s.groupOrderSource(groupOrderTarget(d.Query), nil)
		if err != nil {
			return homeSource{}, err
		}
		from = `(` + group + `) ord CROSS JOIN catalog_browse_rows e ON e.entity_id=ord.id CROSS JOIN catalog_entities ce ON ce.id=e.entity_id`
		args = append(groupArgs, args...)
		direction := "ASC"
		if sorts[0].Direction == "desc" {
			direction = "DESC"
		}
		order = "ord.pos " + direction + ",e.entity_id " + direction
	} else {
		order, orderArgs, err = s.queueBrowseOrder(r.Viewer, &QueueQuery{LibraryID: d.LibraryID, Query: d.Query}, sorts, where, args, nil)
		if err != nil {
			return homeSource{}, err
		}
	}
	request := BrowseRequest{Library: d.LibraryID, Profile: r.Profile, ViewerFence: r.ViewerFence, Pivot: d.Pivot, Query: d.Query, Restrictions: r.Restrictions}
	revision, err := s.browseRevision(request, pivot, sorts)
	if err != nil {
		return homeSource{}, err
	}
	return homeSource{evaluationAt: s.relativeBrowseTime(d.Query), base: `SELECT pid(ce.public_id) AS id,printf('%020d',ROW_NUMBER() OVER(ORDER BY ` + order + `)) AS ord FROM ` + from + ` WHERE ` + where, args: append(orderArgs, args...), selfRestricted: true, fingerprint: fmt.Sprintf("%d:%s", view.Revision, revision)}, nil
}

func homeViewActor(profile string) (ResourceActor, bool) {
	v, ok := viewerFromPersonalKey(profile)
	if !ok {
		return ResourceActor{}, false
	}
	return ResourceActor{Authority: v.Authority, AccountID: v.AccountID, ProfileID: v.ProfileID}, true
}
