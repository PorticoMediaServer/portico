package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"net/url"
	"github.com/PorticoMediaServer/portico/server/internal/readingpaths"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readingcap"
	"github.com/PorticoMediaServer/portico/server/internal/sorttext"
)

// BrowseCapabilitiesForViewer keeps Reading publication behind the same ready,
// visible scope as its facets and leaves. Other library kinds retain their
// established capability producer.
func (s *Service) BrowseCapabilitiesForViewer(viewer Viewer, library, pivot string) (BrowseCapabilities, error) {
	lib, err := s.library(library)
	if err != nil {
		return BrowseCapabilities{}, err
	}
	if !isReadingLibrary(lib.Kind) {
		return s.BrowseCapabilitiesFor(library, pivot)
	}
	if !viewer.AllowsLibrary(library) {
		return BrowseCapabilities{}, sql.ErrNoRows
	}
	if dbwork.Snapshot(s.Context()) == nil {
		var out BrowseCapabilities
		err = dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
			var e error
			out, e = s.WithContext(ctx).BrowseCapabilitiesForViewer(viewer, library, pivot)
			return e
		})
		return out, err
	}
	r, err := s.readingBrowseScope(library)
	if err != nil {
		return BrowseCapabilities{}, err
	}
	out := BrowseCapabilities{Library: lib, Pivots: []BrowsePivotCapability{}, Fields: []BrowseFieldCapability{}, Sorts: []BrowseSortCapability{}, QuickFilters: []BrowseQuickFilter{}, QueryLimits: browseQueryLimits()}
	visible, args := EntityRestrictionSQL("m.item_id", viewer.EffectiveRestrictions())
	var author, series bool
	common := r.memberScope() + ` AND ` + r.eligibleSQL("m.item_id") + ` AND (` + visible + `)`
	err = s.read().QueryRow(`SELECT EXISTS(SELECT 1 FROM reading_attribution_members m WHERE `+common+` AND m.author_key IS NOT NULL),EXISTS(SELECT 1 FROM reading_attribution_members m WHERE `+common+` AND m.series_key IS NOT NULL)`, append(append([]any{}, args...), args...)...).Scan(&author, &series)
	if err != nil {
		return out, err
	}
	var magazines, newspapers bool
	if lib.Kind == "book" {
		dated := common + ` AND m.series_id IS NOT NULL AND m.issue_date IS NOT NULL`
		err = s.read().QueryRow(`SELECT EXISTS(SELECT 1 FROM reading_attribution_members m WHERE `+dated+` AND m.publication_type='magazine'),EXISTS(SELECT 1 FROM reading_attribution_members m WHERE `+dated+` AND m.publication_type='newspaper' AND length(m.issue_date)=10)`, append(append([]any{}, args...), args...)...).Scan(&magazines, &newspapers)
		if err != nil {
			return out, err
		}
	}
	kind := "ebook"
	if lib.Kind == "comic" {
		kind = "comic"
	}
	fields := []string{"title", "year", "dateAdded"}
	if author {
		fields = append(fields, "author")
	}
	if series {
		fields = append(fields, "series")
	}
	for _, entry := range pivotsForKind(lib.Kind) {
		if entry.ID == "authors" && !author || entry.ID == "series" && !series {
			continue
		}
		p := BrowsePivotCapability{ID: entry.ID, LabelKey: entry.LabelKey, EntityKinds: entry.EntityKinds, DefaultSort: entry.DefaultSort, SupportedViews: entry.SupportedViews, Browsable: entry.Browsable, Filters: fields, Sorts: pivotSortIDs(lib.Kind, entry), Paging: "keyset"}
		if !entry.Browsable {
			field := "author"
			if entry.ID == "series" {
				field = "series"
			}
			p.FacetSource = &BrowseFacetSource{Endpoint: "/v1/libraries/{id}/facets", Field: field}
			p.Paging = "offset"
			p.DefaultSort = []BrowseSortSelection{}
		}
		out.Pivots = append(out.Pivots, p)
		if entry.ID == "books" {
			for _, periodical := range []struct {
				id, label string
				available bool
			}{{"magazines", "library.tab.magazines", magazines}, {"newspapers", "library.tab.newspapers", newspapers}} {
				if !periodical.available {
					continue
				}
				offered := BrowsePivotCapability{ID: periodical.id, LabelKey: periodical.label, EntityKinds: []string{}, DefaultSort: []BrowseSortSelection{}, SupportedViews: []string{"grid"}, Browsable: false, Sorts: []string{}, Filters: []string{}, Paging: "keyset", FacetSource: &BrowseFacetSource{Endpoint: readingpaths.PeriodicalGroups, Field: "publicationType"}}
				out.Pivots = append(out.Pivots, offered)
				if periodical.id == pivot {
					copy := offered
					out.ResolvedPivot = &copy
				}
			}
		}
		if entry.ID == pivot {
			copy := p
			out.ResolvedPivot = &copy
		}
	}
	if pivot != "" && out.ResolvedPivot == nil {
		return out, ErrBrowsePivot
	}
	for _, id := range fields {
		f, _ := browseFieldByID(id)
		cap := BrowseFieldCapability{ID: f.ID, LabelKey: f.LabelKey, Type: string(f.Type), Operators: f.Operators, ControlHint: f.ControlHint, Complexity: f.Complexity, Cost: f.Cost, ApplicableKinds: []string{kind}, AllowedValues: f.AllowedValues}
		if id == "author" || id == "series" {
			cap.FacetSource = &BrowseFacetSource{Endpoint: "/v1/libraries/{id}/facets", Field: id}
			cap.ControlHint = "facet-multi-select"
		}
		out.Fields = append(out.Fields, cap)
	}
	if out.ResolvedPivot == nil || out.ResolvedPivot.Browsable {
		for _, id := range []string{"title", "added", "year"} {
			f, _ := browseSortByID(id)
			out.Sorts = append(out.Sorts, BrowseSortCapability{ID: id, LabelKey: f.LabelKey, Directions: f.Directions, DefaultDirection: f.DefaultDirection, ApplicableKinds: []string{kind}})
		}
		out.Surfaces = []BrowseSurfaceCapability{{ID: "library", Endpoint: "/v1/libraries/{id}/browse", Sorts: []string{"title", "added", "year"}, Filters: fields, Paging: "keyset"}}
	}
	return out, nil
}

func (r *readingBrowseScope) project(s *Service, rows []browseRow, profile string, authorizePersonal func(*sql.Tx) error) ([]ContentEntry, error) {
	ids := make([]any, len(rows))
	for i, row := range rows {
		ids[i] = row.entity
	}
	formats := r.sourceFormatsSQL()
	// Every fact comes from this ready membership and the same canonical
	// primary publication as the row. Page counts also require the exact
	// current inventory observation; a prior file's count is never reused.
	pageCount := `(SELECT c.page_count FROM reading_publications p
 JOIN catalog_asset_links link ON link.entity_id=p.item_id AND link.asset_id=p.asset_id
 JOIN catalog_assets a ON a.id=p.asset_id
 JOIN reading_text_counts c ON c.item_id=p.item_id AND c.asset_id=p.asset_id
 JOIN inventory_objects o ON o.id=c.inventory_object_id AND o.asset_id=a.token AND o.revision=c.source_revision
 JOIN library_sources source ON source.id=o.source_id AND source.library_id=m.library_id AND source.incarnation=o.root_incarnation
 WHERE p.item_id=m.item_id AND p.asset_id=` + r.primaryPublicationSQL("m.item_id") + `
 AND p.format IN(` + readingcap.ComicFormatsSQL() + `) AND c.rule_version=` + strconv.Itoa(readingcap.TextCountRuleVersion) + ` AND c.page_rule_version=1 AND c.page_count BETWEEN 1 AND 4096
 AND o.retired=0 AND o.state='available' AND o.unsupported_reason='' AND o.size=a.size AND o.modified_ns=a.modified_ns
 AND source.enabled=1 AND source.health NOT IN('offline','root_changed','removing') ORDER BY o.id LIMIT 1)`
	mark, markRevision := "NULL", "NULL"
	queryArgs := []any{}
	if authorizePersonal != nil && profile != "" {
		if err := authorizePersonal(dbwork.Snapshot(s.Context())); err != nil {
			return nil, err
		}
		mark = `(SELECT watched FROM personal_watched_intents WHERE profile_id=? AND item_id=m.item_id)`
		markRevision = `(SELECT revision FROM personal_items WHERE profile_id=? AND item_id=m.item_id)`
		queryArgs = append(queryArgs, profile, profile)
	}
	query := `SELECT m.item_id,l.library_id,m.publication_type,COALESCE(m.author_value,''),m.series_value,m.numbered_order,m.number_value,d.added_text,` + pageCount + `,` + mark + `,` + markRevision + `,
 EXISTS(SELECT 1 FROM reading_publications p JOIN catalog_asset_links selected_link ON selected_link.entity_id=p.item_id AND selected_link.asset_id=p.asset_id
 JOIN catalog_assets a ON a.id=p.asset_id JOIN inventory_item_availability available ON available.item_id=p.item_id AND available.asset_id=a.token
 WHERE p.item_id=m.item_id AND p.format IN(` + formats + `) AND p.asset_id=` + r.primaryPublicationSQL("m.item_id") + ` AND available.available=1)
 FROM reading_attribution_members m JOIN catalog_libraries l ON l.id=m.library_id LEFT JOIN catalog_item_details d ON d.entity_id=m.item_id
 WHERE ` + r.memberScope() + ` AND m.item_id IN(` + placeholders(len(ids)) + `)`
	read, err := s.read().Query(query, append(queryArgs, ids...)...)
	if err != nil {
		return nil, err
	}
	defer read.Close()
	facts := map[int64]ContentEntry{}
	for read.Next() {
		var id int64
		var entry ContentEntry
		var available bool
		var publicationType, series, added sql.NullString
		var numbered int
		var number float64
		var pages, revision sql.NullInt64
		var finished sql.NullBool
		if err = read.Scan(&id, &entry.LibraryID, &publicationType, &entry.Author, &series, &numbered, &number, &added, &pages, &finished, &revision, &available); err != nil {
			return nil, err
		}
		if publicationType.Valid {
			switch publicationType.String {
			case "book", "magazine", "newspaper":
				entry.PublicationType = ReadingPublicationType(publicationType.String)
			default:
				return nil, ErrReadingProjectionChanged
			}
		}
		if series.Valid {
			value := AudioSeries{Name: series.String}
			if numbered == 0 {
				value.Position = strconv.FormatFloat(number, 'f', -1, 64)
			}
			entry.Series = []AudioSeries{value}
		}
		if added.Valid && added.String != "" {
			value := added.String
			entry.AddedAt = &value
		}
		if pages.Valid {
			value := int(pages.Int64)
			entry.PageCount = &value
		}
		if finished.Valid && revision.Valid {
			entry.ReadingMark = &ReadingMark{Read: finished.Bool, Revision: revision.Int64}
		}
		entry.Subtitle = entry.Author
		entry.Available = &available
		facts[id] = entry
	}
	if err = read.Err(); err != nil {
		return nil, err
	}
	entities := make([]int64, len(rows))
	for i, row := range rows {
		entities[i] = row.entity
	}
	covers, err := compactcatalog.ReadingCoverQueriesTx(s.Context(), dbwork.Snapshot(s.Context()), entities)
	if err != nil {
		return nil, err
	}
	out := make([]ContentEntry, 0, len(rows))
	for _, row := range rows {
		entry, ok := facts[row.entity]
		if !ok {
			return nil, ErrStaleContinuation
		}
		entry.ID = row.id
		if entry.ReadingMark != nil {
			entry.ReadingMark.ItemID = row.id
		}
		if digest := covers[row.entity]; digest != "" {
			entry.PosterURL = "/v1/items/" + url.PathEscape(row.id) + "/art/cover?" + digest
		}

		entry.Kind = row.kind
		entry.Title = row.title
		entry.SortLetter = sorttext.Letter(row.sortKey)
		entry.Navigation = &ContentNavigation{View: "item", EntityID: row.id}
		if row.year > 0 {
			year := row.year
			entry.Year = &year
		}
		out = append(out, entry)
	}
	return out, nil
}

// readingFacetPage groups exact accepted values in SQL before applying the
// bounded window. The label is owner spelling when present, otherwise the most
// frequent accepted spelling; ties are binary lexical, as ReadingFacets rules.
func (s *Service) readingFacetPage(viewer Viewer, request FacetRequest, r *readingBrowseScope) (FacetPage, error) {
	out := FacetPage{Field: request.Field, Values: []FacetValue{}}
	if request.Field != "author" && request.Field != "series" {
		return out, browseIssue("field", "Reading counts author and series facets")
	}
	if request.Pivot != "" {
		if _, ok := pivotForKind(r.kind, request.Pivot); !ok {
			return out, ErrBrowsePivot
		}
	}
	if len(request.Q) > 100 || request.Offset < 0 {
		return out, browseIssue("q", "facet window is invalid")
	}
	limit := request.Limit
	if limit < 1 || limit > browseFacetLimit {
		limit = browseFacetLimit
	}
	if request.Sort != "" && request.Sort != "title" && request.Sort != "count" {
		return out, browseIssue("sort", "Reading facets sort by title or count")
	}
	query := browseAnd(request.Group, request.Query)
	var err error
	s, err = s.withBrowseInstant(query, "")
	if err != nil {
		return out, err
	}
	c := &browseCompiler{reading: r, now: s.recommendationNow(s.browseInstant), profile: viewer.Profile, restrictions: viewer.EffectiveRestrictions()}
	clause, err := c.compile(query)
	if err != nil {
		return out, err
	}
	visible, va := EntityRestrictionSQL("e.entity_id", viewer.EffectiveRestrictions())
	field := request.Field
	cte := `WITH eligible AS MATERIALIZED(SELECT m.` + field + `_key AS value_key,m.` + field + `_value AS label,m.` + field + `_source AS source
 FROM reading_attribution_members m CROSS JOIN catalog_browse_rows e ON e.entity_id=m.item_id AND e.library_id=m.library_id
 WHERE ` + r.memberScope() + ` AND m.` + field + `_key IS NOT NULL AND ` + r.eligibleSQL("m.item_id") + ` AND (` + clause + `) AND (` + visible + `)),
 spellings AS(SELECT value_key,label,count(*) AS frequency,sum(source='owner') AS owners FROM eligible GROUP BY value_key,label),
 ranked AS(SELECT value_key,label,sum(frequency) OVER(PARTITION BY value_key) AS total,
 row_number() OVER(PARTITION BY value_key ORDER BY owners>0 DESC,CASE WHEN owners>0 THEN owners ELSE frequency END DESC,label COLLATE BINARY) AS rank FROM spellings),
 facets AS(SELECT value_key,label,total FROM ranked WHERE rank=1 AND instr(value_key,?)>0) `
	args := append(append([]any{}, c.args...), va...)
	args = append(args, compactcatalog.ReadingValueKey(request.Q))
	if err = s.read().QueryRow(cte+`SELECT count(*) FROM facets`, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	order := `value_key COLLATE BINARY`
	if request.Sort == "count" {
		order = `total DESC,value_key COLLATE BINARY`
	}
	read, err := s.read().Query(cte+`SELECT label,total FROM facets ORDER BY `+order+` LIMIT ? OFFSET ?`, append(append([]any{}, args...), limit, request.Offset)...)
	if err != nil {
		return out, err
	}
	for read.Next() {
		var value FacetValue
		if err = read.Scan(&value.Value, &value.Count); err != nil {
			break
		}
		value.Label = value.Value
		if field == "series" {
			value.SeriesID = ReadingFacetSeriesID(compactcatalog.ReadingSeriesID(request.Library, value.Value))
		}
		out.Values = append(out.Values, value)
	}
	if err == nil {
		err = read.Err()
	}
	read.Close()
	if err != nil {
		return out, err
	}
	revision, err := s.ContentRevision(request.Library, viewer.Profile)
	if err != nil {
		return out, err
	}
	// No cached or sampled aggregate: all inputs and the selected window share
	// this snapshot. Revision changes with the input/ready lifetime and viewer.
	out.Revision = fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%d:%d:%s:%s:%s:%s", request.Library, request.Field, revision.Catalog, revision.Viewer, r.fence(), viewer.Fence, RestrictionFence(viewer.EffectiveRestrictions()), queryFingerprint(query)+s.relativeBrowseFence(query)))))
	return out, nil
}
