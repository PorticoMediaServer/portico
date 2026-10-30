package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/sorttext"
)

// Facets count the values a viewer could actually choose. Every count is taken
// through browse_entities, so retired DVR rows and rows outside the library are
// excluded by construction, and the result is cached per catalog revision.

// ReadingFacetSeriesID is the existing library-scoped opaque Reading identity.
// It is an optional navigation fact, never the attribution filter value.
type ReadingFacetSeriesID string

func (ReadingFacetSeriesID) JSONSchema() map[string]any {
	return map[string]any{"type": "string", "pattern": "^[A-Za-z0-9_-]{43}$"}
}

type FacetValue struct {
	SeriesID          ReadingFacetSeriesID `json:"seriesId,omitempty"`
	Value             string               `json:"value"`
	Label             string               `json:"label"`
	Count             int                  `json:"count"`
	CountIsLowerBound bool                 `json:"countIsLowerBound"`
}
type FacetPage struct {
	// Private aggregate payload reuses the bounded revision/single-flight cache;
	// it is never serialized as a facet response.
	durationMs        int64
	durationLower     bool
	Field             string       `json:"field"`
	TotalIsLowerBound bool         `json:"totalIsLowerBound"`
	Revision          string       `json:"revision,omitempty"`
	Total             int          `json:"total"`
	Values            []FacetValue `json:"values"`
}
type FacetRequest struct {
	Library string
	Pivot   string
	Query   *BrowseNode
	Profile string
	Field   string
	Q       string
	Limit   int
	Offset  int
	// Keyset parameters belong only to the SIMPLE branch. Presence is kept
	// separately so offset=0 is rejected there and omitted limits can inherit
	// the original continuation window size.
	After, Before, At           string
	LimitPresent, OffsetPresent bool
	Sort                        string
	// Restrictions must match the browse request whose facets these are, or the
	// counts would advertise titles the page itself refuses to show.
	Restrictions identity.ContentRestrictions
	// Group, for a list across libraries, is the list's group condition (a
	// person, playlist, collection, Home row, My List, Favorites or the
	// history): values are counted over the list's own titles. Rows are the
	// Home rows it names, resolved by the caller.
	Group      *BrowseNode
	Rows       map[string][]int64
	RowSources map[string]HomeRowMembership
}

// facetSource describes how one facet is counted. Join is appended after
// `FROM catalog_browse_rows e` and may reference e.item_id (the INTEGER entity
// id of the member item).
type facetSource struct {
	Join  string
	Value string
	Label string
}

var facetSources = map[string]facetSource{
	"genre":         {Join: `JOIN catalog_term_sources ts ON ts.entity_id IN(e.item_id,(SELECT ep.show_id FROM catalog_episodes ep WHERE ep.entity_id=e.item_id)) JOIN catalog_terms mg ON mg.id=ts.term_id AND mg.vocab=1`, Value: "COALESCE(ts.label_override,mg.label)", Label: "COALESCE(ts.label_override,mg.label)"},
	"decade":        {Value: "CAST(((e.year/10)*10) AS TEXT)", Label: "CAST(((e.year/10)*10) AS TEXT)||'s'", Join: ``},
	"year":          {Value: "CAST(e.year AS TEXT)", Label: "CAST(e.year AS TEXT)"},
	"contentRating": {Join: attributeFacetJoin("contentRating"), Value: "ca.source_value", Label: "ca.source_value"},
	"resolution":    {Join: `JOIN catalog_asset_links ia ON ia.entity_id=e.item_id JOIN catalog_assets ast ON ast.id=ia.asset_id`, Value: `CASE WHEN ast.height>=2000 THEN '4k' WHEN ast.height>=1000 THEN '1080p' WHEN ast.height>=700 THEN '720p' ELSE 'sd' END`, Label: `CASE WHEN ast.height>=2000 THEN '4K' WHEN ast.height>=1000 THEN '1080p' WHEN ast.height>=700 THEN '720p' ELSE 'SD' END`},
	"audioLanguage": {Join: attributeFacetJoin("audioLanguage"), Value: "ca.source_value", Label: "ca.source_value"},
	"studio":        {Join: attributeFacetJoin("studio"), Value: "ca.source_value", Label: "ca.source_value"},
	"network":       {Join: attributeFacetJoin("network"), Value: "ca.source_value", Label: "ca.source_value"},
	"tag":           {Join: attributeFacetJoin("tag"), Value: "ca.source_value", Label: "ca.source_value"},
	"label":         {Join: attributeFacetJoin("label"), Value: "ca.source_value", Label: "ca.source_value"},
	"series":        {Join: attributeFacetJoin("series"), Value: "ca.source_value", Label: "ca.source_value"},
	"library":       {Join: `JOIN catalog_libraries library_key ON library_key.id=e.library_id JOIN libraries library ON library.id=library_key.library_id`, Value: "library.id", Label: "library.name"},
	"actor":         {Join: `JOIN catalog_credits cc ON cc.entity_id IN(e.item_id,e.entity_id) JOIN catalog_credit_labels department ON department.id=cc.department_id AND department.label='Acting'`, Value: "cc.credited_name", Label: "cc.credited_name"},
	"director":      {Join: `JOIN catalog_credits cc ON cc.entity_id IN(e.item_id,e.entity_id) JOIN catalog_credit_labels role ON role.id=cc.role_id JOIN catalog_credit_labels department ON department.id=cc.department_id AND (role.label='Director' OR department.label='Directing')`, Value: "cc.credited_name", Label: "cc.credited_name"},
	"writer":        {Join: `JOIN catalog_credits cc ON cc.entity_id IN(e.item_id,e.entity_id) JOIN catalog_credit_labels role ON role.id=cc.role_id JOIN catalog_credit_labels department ON department.id=cc.department_id AND (role.label IN('Writer','Screenplay') OR department.label='Writing')`, Value: "cc.credited_name", Label: "cc.credited_name"},
	"collection":    {Join: `JOIN catalog_collection_members cim ON cim.item_id=e.item_id JOIN catalog_entities col ON col.id=cim.collection_id`, Value: "pid(col.public_id)", Label: "col.title"},
}

func attributeFacetJoin(field string) string {
	return `JOIN catalog_item_attribute_edges ca ON ca.item_id=e.item_id JOIN catalog_attribute_terms ca_term ON ca_term.id=ca.term_id AND ca_term.field_id=(SELECT id FROM catalog_attribute_fields WHERE field='` + field + `')`
}

// Facets returns the counted values of one field under the viewer's visibility.
func (s *Service) Facets(viewer Viewer, request FacetRequest) (FacetPage, error) {
	out := FacetPage{Field: request.Field, Values: []FacetValue{}}
	if request.Library != "" {
		lib, err := s.library(request.Library)
		if err != nil {
			return out, err
		}
		if isReadingLibrary(lib.Kind) {
			if !viewer.AllowsLibrary(request.Library) {
				return out, sql.ErrNoRows
			}
			if dbwork.Snapshot(s.Context()) == nil {
				err = dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
					var e error
					out, e = s.WithContext(ctx).Facets(viewer, request)
					return e
				})
				return out, err
			}
			r, err := s.readingBrowseScope(request.Library)
			if err != nil {
				return out, err
			}
			return s.readingFacetPage(viewer, request, r)
		}
	}
	if request.Query != nil || request.Pivot != "" || request.Field == "library" || request.Field == "actor" || request.Field == "director" || request.Field == "writer" {
		return s.contextualFacets(viewer, request)
	}
	if !viewer.AllowsLibrary(request.Library) {
		return out, sql.ErrNoRows
	}
	if err := s.prepareViewer(viewer); err != nil {
		return out, err
	}
	request.Profile, request.Restrictions = viewer.Profile, viewer.EffectiveRestrictions()
	source, ok := facetSources[request.Field]
	if !ok {
		return out, browseIssue("field", "field is not a counted facet")
	}
	if request.Limit < 1 || request.Limit > browseFacetLimit {
		request.Limit = browseFacetLimit
	}
	if len(request.Q) > 100 {
		return out, browseIssue("q", "facet filter exceeds 100 characters")
	}
	if _, err := s.library(request.Library); err != nil {
		return out, err
	}
	if err := s.browseReady(request.Library); err != nil {
		return out, err
	}
	// The key already carries the restriction fence, so one viewer's counts can
	// never be served to a viewer under a different restriction. It leaves out
	// q and the limit: a field's whole value list is counted once per catalogue
	// revision, and a typeahead's filter and limit are applied to it here, so
	// each keystroke is not another pass over the library.
	key := request.Library + "\x00" + request.Field + "\x00" + RestrictionFence(request.Restrictions)
	revision, err := s.ContentRevision(request.Library, request.Profile)
	if err != nil {
		return out, err
	}
	all, hit := s.state.facetCache.get(key, revision.Catalog)
	if !hit {
		// Single-flight: two hundred viewers arriving at a cold facet together
		// produce one computation, not two hundred.
		all, err = s.state.facetCache.do(key, func() (FacetPage, error) {
			if cached, hit := s.state.facetCache.get(key, revision.Catalog); hit {
				return cached, nil
			}
			return s.computeFacets(request, source, revision, key)
		})
		if err != nil {
			return out, err
		}
	}
	needle := sorttext.Fold(request.Q)
	filtered := make([]FacetValue, 0, len(all.Values))
	for _, value := range all.Values {
		if needle == "" || strings.Contains(sorttext.Fold(value.Label), needle) {
			filtered = append(filtered, value)
		}
	}
	if strings.HasPrefix(request.Sort, "title:") {
		desc := request.Sort == "title:desc"
		sort.Slice(filtered, func(i, j int) bool {
			a, b := sorttext.Key(filtered[i].Label, "", ""), sorttext.Key(filtered[j].Label, "", "")
			if a == b {
				a, b = filtered[i].Value, filtered[j].Value
			}
			if desc {
				return a > b
			}
			return a < b
		})
	}
	out.Total = len(filtered)
	start := min(max(0, request.Offset), len(filtered))
	out.Values = filtered[start:min(start+request.Limit, len(filtered))]

	return out, nil
}

func (s *Service) computeFacets(request FacetRequest, source facetSource, revision ContentRevision, key string) (FacetPage, error) {
	out := FacetPage{Field: request.Field, Values: []FacetValue{}}
	// The maintained counts answer every viewer whose restriction is decided
	// per item (age, unrated, labels). A member-level restriction, or counts
	// still being built, count live below.
	r := request.Restrictions
	// A show library counts shows (derive_facet_counts.go). The maintained
	// counts are kept under the show's own keys, which cannot say whether a
	// restricted viewer may see any of its episodes, so a restricted viewer's
	// counts in a show library are taken live: a show counts when one of its
	// episodes is visible to them.
	lib, err := s.library(request.Library)
	if err != nil {
		return out, err
	}
	shows := lib.Kind == "tv" || lib.Kind == "anime"
	if len(r.MemberDeniedLabels) == 0 && !(shows && r.Active()) && s.compactProjectionReady(33) == nil {
		if out.Values, err = s.facetsFromCounts(request); err != nil {
			return out, err
		}
		return s.storeFacets(request, revision, key, out)
	}
	query := ""
	args := []any{}
	if request.Field == "decade" && request.Restrictions.Active() {
		classKey, generation, ready, classErr := s.publishedVisibilityClass(request.Library, request.Restrictions)
		if classErr != nil {
			return out, classErr
		}
		if ready {
			// The background class maintains decade totals. A facet-cache miss
			// must not recheck every item's rating and labels in a large library.
			query = `SELECT CAST(decade AS TEXT),CAST(decade AS TEXT)||'s',sum(total) AS facet_count
				FROM compact_visibility_counts WHERE class_id=(SELECT id FROM compact_visibility_classes WHERE class_key=?) AND generation=?
				AND library_id=(SELECT id FROM catalog_libraries WHERE library_id=?)
				AND kind IN (1,4,7,9,11)
				AND decade BETWEEN 1800 AND 2190`
			if shows {
				// The class counts its visible shows (kind 2) as well as their episodes.
				query = strings.Replace(query, "kind IN (1,4,7,9,11)", "kind=2", 1)
			}
			args = []any{classKey, generation, request.Library}
			query += ` GROUP BY decade ORDER BY facet_count DESC,decade COLLATE NOCASE`
		}
	}
	if query == "" && shows {
		// Each visible episode stands for its show: the row a facet reads is the
		// episode (its file and title facts) under the show's id and year.
		restriction, restrictionArgs := ItemRestrictionSQL("er.item_id", request.Restrictions)
		from := `(SELECT er.item_id AS item_id,ep.show_id AS entity_id,sh.year AS year FROM catalog_browse_rows er
		 JOIN catalog_episodes ep ON ep.entity_id=er.entity_id JOIN catalog_browse_rows sh ON sh.entity_id=ep.show_id
		 WHERE er.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND er.item_id IS NOT NULL AND ` + restriction + `) e`
		args = append([]any{request.Library}, restrictionArgs...)
		where := "1"
		if request.Field == "decade" || request.Field == "year" {
			where = `e.year BETWEEN 1800 AND 2199`
		}
		query = `SELECT ` + source.Value + ` AS facet_value,` + source.Label + ` AS facet_label,count(DISTINCT e.entity_id) AS facet_count FROM ` + from + ` ` + source.Join + ` WHERE ` + where + ` GROUP BY facet_value ORDER BY facet_count DESC,facet_label COLLATE NOCASE`
	}
	if query == "" {
		where := `e.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND e.item_id IS NOT NULL`
		args = []any{request.Library}
		if request.Field == "decade" || request.Field == "year" {
			where += ` AND e.year BETWEEN 1800 AND 2199`
		}
		if restriction, restrictionArgs := ItemRestrictionSQL("e.item_id", request.Restrictions); restriction != "1" {
			where += " AND " + restriction
			args = append(args, restrictionArgs...)
		}
		query = `SELECT ` + source.Value + ` AS facet_value,` + source.Label + ` AS facet_label,count(DISTINCT e.entity_id) AS facet_count FROM catalog_browse_rows e ` + source.Join + ` WHERE ` + where + ` GROUP BY facet_value ORDER BY facet_count DESC,facet_label COLLATE NOCASE`
	}
	rows, err := s.read().Query(query, args...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var value FacetValue
		if err = rows.Scan(&value.Value, &value.Label, &value.Count); err != nil {
			rows.Close()
			return out, err
		}
		if value.Value == "" {
			continue
		}
		out.Values = append(out.Values, value)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	return s.storeFacets(request, revision, key, out)
}

// storeFacets keeps a field's counted values for this catalogue revision.
func (s *Service) storeFacets(request FacetRequest, revision ContentRevision, key string, out FacetPage) (FacetPage, error) {
	after, err := s.ContentRevision(request.Library, request.Profile)
	if err != nil {
		return out, err
	}
	if after.Catalog != revision.Catalog {
		return out, ErrStaleContinuation
	}
	s.state.facetCache.put(key, facetCacheEntry{revision: revision.Catalog, publications: dbwork.Publications(), expires: time.Now().Add(time.Minute), page: out})
	return out, nil
}

// facetsFromCounts reads one field's maintained counts (catalog_facet_counts)
// and decides each count against the viewer's restriction by its keys, as the
// browse buckets are decided: the cost is the field's distinct values, never
// the library's items.
func (s *Service) facetsFromCounts(request FacetRequest) ([]FacetValue, error) {
	r := request.Restrictions
	var ages ratingAges
	blocked := map[string]bool{}
	if r.Active() {
		var err error
		if ages, err = s.ratingAges(); err != nil {
			return nil, err
		}
		for _, label := range r.BlockedLabels {
			blocked[strings.ToLower(label)] = true
		}
	}
	rows, err := s.read().Query(`SELECT value,rating_key,label_key,total FROM catalog_facet_counts
	 WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND field=?`, request.Library, request.Field)
	if err != nil {
		return nil, err
	}
	totals := map[string]int{}
	for rows.Next() {
		var value string
		var bucket browseBucket
		if err = rows.Scan(&value, &bucket.ratingKey, &bucket.labelKey, &bucket.total); err != nil {
			rows.Close()
			return nil, err
		}
		if !r.Active() || bucketVisible(bucket, ages, r, blocked) {
			totals[value] += bucket.total
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	labels := map[string]string{}
	if request.Field == "collection" && len(totals) > 0 {
		// Collection counts are kept by entity id; the value a filter takes is
		// the collection's public id and its label is its title.
		ids := make([]string, 0, len(totals))
		for id := range totals {
			ids = append(ids, id)
		}
		raw, _ := json.Marshal(ids)
		titles, err := s.read().Query(`SELECT CAST(e.id AS TEXT),pid(e.public_id),e.title FROM json_each(?) j CROSS JOIN catalog_entities e ON e.id=CAST(j.value AS INTEGER)`, string(raw))
		if err != nil {
			return nil, err
		}
		public := map[string]int{}
		for titles.Next() {
			var id, pid, title string
			if err = titles.Scan(&id, &pid, &title); err != nil {
				titles.Close()
				return nil, err
			}
			public[pid] += totals[id]
			labels[pid] = title
		}
		err = titles.Err()
		titles.Close()
		if err != nil {
			return nil, err
		}
		totals = public
	}
	out := make([]FacetValue, 0, len(totals))
	for value, count := range totals {
		if count == 0 {
			continue
		}
		label := value
		switch request.Field {
		case "decade":
			label = value + "s"
		case "resolution":
			label = map[string]string{"4k": "4K", "sd": "SD"}[value]
			if label == "" {
				label = value
			}
		case "collection":
			label = labels[value]
		}
		out = append(out, FacetValue{Value: value, Label: label, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		if a, b := asciiLower(out[i].Label), asciiLower(out[j].Label); a != b {
			return a < b
		}
		return out[i].Value < out[j].Value
	})
	return out, nil
}

// browseCategoryPredicate translates the legacy `category=<facet>:<value>` chip
// into an expression predicate so both surfaces execute the same engine.
func browseCategoryPredicate(category string) (*BrowseNode, error) {
	field, value, ok := strings.Cut(category, ":")
	if !ok || value == "" {
		return nil, requestinput.New("unknown category")
	}
	switch field {
	case "decade":
		year, err := strconv.Atoi(value)
		if err != nil || year < 1800 || year > 2190 || year%10 != 0 {
			return nil, requestinput.New("invalid decade category")
		}
		return &BrowseNode{Field: "decade", Operator: "equals", Value: float64(year)}, nil
	case "genre", "series", "tag", "label", "studio", "network", "contentRating", "audioLanguage", "collection", "resolution":
		definition, ok := browseFieldByID(field)
		if !ok {
			return nil, requestinput.New("unknown category")
		}
		operator := "equals"
		if definition.Type == valueSet {
			operator = "contains"
		}
		return &BrowseNode{Field: field, Operator: operator, Value: value}, nil
	case "year":
		year, err := strconv.Atoi(value)
		if err != nil || year < 1800 || year > 2199 {
			return nil, requestinput.New("invalid year category")
		}
		return &BrowseNode{Field: "year", Operator: "equals", Value: float64(year)}, nil
	}
	return nil, requestinput.New("unknown category")
}

// withoutFacet removes only the counted field. A branch made empty becomes
// true, including within an alternative, so removing a condition never narrows
// the result. The immutable group source is attached separately by the caller.
func withoutFacet(node *BrowseNode, field string) *BrowseNode {
	if node == nil || node.Field == field {
		return nil
	}
	copy := *node
	if node.Not != nil {
		copy.Not = withoutFacet(node.Not, field)
		if copy.Not == nil {
			return nil
		}
		return &copy
	}
	if node.All != nil || node.Any != nil {
		children := node.All
		any := node.Any != nil
		if any {
			children = node.Any
		}
		kept := []BrowseNode{}
		for i := range children {
			child := withoutFacet(&children[i], field)
			if child == nil {
				if any {
					return nil
				}
				continue
			}
			kept = append(kept, *child)
		}
		if len(kept) == 0 {
			return nil
		}
		if len(kept) == 1 {
			return &kept[0]
		}
		if any {
			copy.Any = kept
		} else {
			copy.All = kept
		}
		return &copy
	}
	return &copy
}

// browseMeasureScope is the same canonical entity selection as browse. It
// keeps the source as SQL through filtering and aggregation, with no ID list
// ceiling. Only the subsequent count/aggregate may be bounded.
func (s *Service) browseMeasureScope(viewer Viewer, request FacetRequest) (string, []any, string, error) {
	var instantErr error
	s, instantErr = s.withBrowseInstant(browseAnd(request.Query, request.Group), "")
	if instantErr != nil {
		return "", nil, "", instantErr
	}
	query := request.Query
	if request.Group != nil {
		query = browseAnd(request.Group, query)
	}
	var pivot browsePivot
	var where string
	var args []any
	var anchor *BrowseNode
	if request.Library != "" {
		if !viewer.AllowsLibrary(request.Library) {
			return "", nil, "", sql.ErrNoRows
		}
		lib, err := s.library(request.Library)
		if err != nil {
			return "", nil, "", err
		}
		if isReadingLibrary(lib.Kind) {
			if _, err := s.readingBrowseScope(request.Library); err != nil {
				return "", nil, "", err
			}
		}
		if err := s.browseReady(request.Library); err != nil {
			if isReadingLibrary(lib.Kind) && (errors.Is(err, ErrVisibilityBuilding) || errors.Is(err, ErrStaleContinuation)) {
				return "", nil, "", ErrReadingProjectionChanged
			}
			return "", nil, "", err
		}
		id := request.Pivot
		if id == "" {
			id = defaultPivot(lib.Kind)
		}
		var ok bool
		pivot, ok = pivotForKind(lib.Kind, id)
		if !ok || isReadingLibrary(lib.Kind) && !pivot.Browsable {
			return "", nil, "", ErrBrowsePivot
		}
		if pivot.Aggregate != "" {
			pivot, _ = pivotForKind(lib.Kind, defaultPivot(lib.Kind))
		}
		where, args = browseScopeSQL(request.Library, pivot)
	} else {
		var ok bool
		pivot, ok = acrossPivot(request.Pivot)
		if !ok {
			return "", nil, "", ErrBrowsePivot
		}
		ks, err := acrossKinds(pivot)
		if err != nil {
			return "", nil, "", err
		}
		where = `e.library_id IN(SELECT id FROM catalog_libraries WHERE library_id IN(SELECT value FROM json_each(?))) AND e.kind IN(` + placeholders(len(ks)) + `)`
		args = append([]any{viewer.librariesJSON()}, ks...)
		if pivot.ID == "video-titles" {
			where += ` AND ` + acrossVideoLibraryPredicate
		}
		anchor = acrossAnchorForPivot(query, pivot)
		if anchor != nil {
			members, ma, _, err := s.acrossMembersForViewer(anchor, viewer, request.Rows, request.RowSources)
			if err != nil {
				return "", nil, "", err
			}
			where += ` AND e.entity_id IN(WITH anchor AS MATERIALIZED (` + members + `) SELECT id FROM anchor UNION SELECT bm.entity_id FROM anchor CROSS JOIN catalog_browse_memberships bm INDEXED BY catalog_browse_membership_item ON bm.item_id=anchor.id UNION SELECT bm.item_id FROM anchor CROSS JOIN catalog_browse_memberships bm ON bm.entity_id=anchor.id)`
			args = append(args, ma...)
		}
	}
	compiler := &browseCompiler{now: s.recommendationNow(s.browseInstant), profile: viewer.Profile, restrictions: viewer.Restrictions, rows: request.Rows, rowSources: request.RowSources, itemPivot: playablePivot(pivot), count: s.countRows, ids: s.entityIDs}
	for _, member := range entityKindMembers(pivot) {
		if kind, err := compactcatalog.ParseKind(member); err == nil {
			compiler.kinds = append(compiler.kinds, int(kind))
		}
	}
	readingEligible, err := s.bindReadingBrowse(request.Library, compiler)
	if err != nil {
		return "", nil, "", err
	}
	where += " AND " + readingEligible
	clause, err := compiler.compile(withoutSelectedGenreAnchor(query, anchor))
	if err != nil {
		return "", nil, "", err
	}
	where += " AND " + clause
	args = append(args, compiler.args...)
	visibility, va := EntityRestrictionSQL("e.entity_id", viewer.Restrictions)
	where += " AND " + visibility
	args = append(args, va...)
	ar := AcrossRequest{Query: query, Rows: request.Rows, RowSources: request.RowSources, profile: viewer.Profile, fence: viewer.Fence + ":" + RestrictionFence(viewer.Restrictions), libraries: viewer.Libraries}
	revision, err := s.acrossRevision(ar, pivot, titleAscending())
	if err != nil {
		return "", nil, "", err
	}
	// The revision also binds the library and query vocabulary when used inside
	// one library, so a cached aggregate cannot cross its pivot or source.
	revision += ":" + request.Library + ":" + queryFingerprint(query)
	witness, err := s.measureWitness(request.Library, viewer.Libraries)
	if err != nil {
		return "", nil, "", err
	}
	revision += ":" + witness
	if compiler.reading != nil {
		revision += ":" + compiler.reading.fence()
	}
	return `SELECT e.entity_id,e.library_id,e.year FROM catalog_browse_rows e WHERE ` + where, args, revision, nil
}

// measureWitness is the state a cached count, facet panel or duration reads
// beyond the order and viewer revisions: each scoped library's raw revision
// (attribute edges, credits and rating ages advance only that) and the rating
// classification revision, which a live age restriction reads.
func (s *Service) measureWitness(library string, libraries []string) (string, error) {
	if library != "" {
		libraries = []string{library}
	}
	var witness string
	if err := s.read().QueryRow(`SELECT COALESCE((SELECT group_concat(state,',') FROM (SELECT library_id||':'||revision AS state FROM library_revisions WHERE library_id IN(SELECT value FROM json_each(?)) ORDER BY library_id)),'')||':c'||(SELECT revision FROM catalog_summary_classification_revision WHERE singleton=1)`, idsJSON(libraries)).Scan(&witness); err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(witness))
	return hex.EncodeToString(digest[:12]), nil
}

func browseAnd(a, b *BrowseNode) *BrowseNode {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	return &BrowseNode{All: []BrowseNode{*a, *b}}
}

func (s *Service) contextualFacets(viewer Viewer, request FacetRequest) (FacetPage, error) {
	out := FacetPage{Field: request.Field, Values: []FacetValue{}}
	if dbwork.Snapshot(s.Context()) == nil {
		err := dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
			var err error
			out, err = s.WithContext(ctx).contextualFacets(viewer, request)
			return err
		})
		return out, err
	}
	if err := s.prepareViewer(viewer); err != nil {
		return out, err
	}
	source, ok := facetSources[request.Field]
	if !ok {
		return out, browseIssue("field", "field is not a counted facet")
	}
	if len(request.Q) > 100 {
		return out, browseIssue("q", "facet filter exceeds 100 characters")
	}
	if request.Limit < 1 || request.Limit > browseFacetLimit {
		request.Limit = browseFacetLimit
	}
	if err := validateMeasureNode(request.Query); err != nil {
		return out, err
	}
	request.Query = withoutFacet(request.Query, request.Field)
	scope, args, revision, err := s.browseMeasureScope(viewer, request)
	if err != nil {
		return out, err
	}
	out.Revision = revision
	// The key leaves out q, the window and the order: each revision's whole
	// value list is counted once, and every typeahead keystroke, page and
	// order is served from it, with its total from the same pass.
	key := fmt.Sprintf("context:%s:%s:%s:%s", revision, viewer.Profile, RestrictionFence(viewer.Restrictions), request.Field)
	all, ok := s.state.facetCache.get(key, 0)
	if !ok {
		all, err = s.state.facetCache.do(key, func() (FacetPage, error) {
			if hit, ok := s.state.facetCache.get(key, 0); ok {
				return hit, nil
			}
			values, err := s.contextualFacetValues(viewer, request, source, scope, args)
			if err != nil {
				return FacetPage{}, err
			}
			page := FacetPage{Values: values}
			s.state.facetCache.put(key, facetCacheEntry{revision: 0, publications: dbwork.Publications(), expires: time.Now().Add(time.Minute), page: page})
			return page, nil
		})
		if err != nil {
			return out, err
		}
	}
	needle := sorttext.Fold(request.Q)
	filtered := make([]FacetValue, 0, len(all.Values))
	for _, value := range all.Values {
		if needle == "" || strings.Contains(sorttext.Fold(value.Label), needle) {
			filtered = append(filtered, value)
		}
	}
	if strings.HasPrefix(request.Sort, "title:") {
		desc := request.Sort == "title:desc"
		sort.Slice(filtered, func(i, j int) bool {
			order := compareNoCase(filtered[i].Label, filtered[j].Label)
			if order == 0 {
				order = strings.Compare(filtered[i].Value, filtered[j].Value)
			}
			if desc {
				return order > 0
			}
			return order < 0
		})
	}
	out.Total = len(filtered)
	if out.Total > browseTotalThreshold {
		out.Total, out.TotalIsLowerBound = browseTotalThreshold, true
	}
	start := min(max(0, request.Offset), len(filtered))
	for _, value := range filtered[start:min(start+request.Limit, len(filtered))] {
		if value.Count > browseTotalThreshold {
			value.Count, value.CountIsLowerBound = browseTotalThreshold, true
		}
		out.Values = append(out.Values, value)
	}
	return out, nil
}

// contextualFacetValues counts every value of a field over the measure scope,
// in the default order: count descending, then label without ASCII case, then
// value. Keep the full SQL source through the value join: a rare actor/genre
// or a library after the first 10,000 members must remain discoverable.
func (s *Service) contextualFacetValues(viewer Viewer, request FacetRequest, source facetSource, scope string, args []any) ([]FacetValue, error) {
	prefix := `WITH targets AS MATERIALIZED (` + scope + `)`
	// Expand only current visible members before reading values, so one hidden
	// episode cannot advertise its actor/genre/language via a visible sibling.
	memberVisible, memberArgs := viewer.itemVisibilitySQL("member.item_id")
	prefix += `, e AS MATERIALIZED (SELECT DISTINCT target.entity_id,target.library_id,target.year,member.item_id FROM targets target JOIN catalog_browse_memberships member ON member.entity_id=target.entity_id WHERE ` + memberVisible + `)`
	bound := append(append([]any{}, args...), memberArgs...)
	where := source.Value + ` IS NOT NULL AND CAST(` + source.Value + ` AS TEXT)<>''`
	if request.Field == "year" || request.Field == "decade" {
		where += ` AND e.year BETWEEN 1800 AND 2199`
	}
	rows, err := s.read().Query(prefix+` SELECT CAST(`+source.Value+` AS TEXT) AS value,`+source.Label+` AS label,count(DISTINCT e.entity_id) AS total FROM e `+source.Join+` WHERE `+where+` GROUP BY value`, bound...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []FacetValue{}
	for rows.Next() {
		var value FacetValue
		if err = rows.Scan(&value.Value, &value.Label, &value.Count); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].Count != values[j].Count {
			return values[i].Count > values[j].Count
		}
		if order := compareNoCase(values[i].Label, values[j].Label); order != 0 {
			return order < 0
		}
		return values[i].Value < values[j].Value
	})
	return values, nil
}

// compareNoCase orders two strings as SQLite's NOCASE collation does: bytes,
// with only ASCII letters folded.
func compareNoCase(a, b string) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		x, y := a[i], b[i]
		if 'A' <= x && x <= 'Z' {
			x += 'a' - 'A'
		}
		if 'A' <= y && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return int(x) - int(y)
		}
	}
	return len(a) - len(b)
}

func BrowseConjunction(a, b *BrowseNode) *BrowseNode { return browseAnd(a, b) }
