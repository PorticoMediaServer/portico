package catalog

import (
	"encoding/json"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"

	"time"
)

// groupMatchLimit is the most matching titles a filtered grouping groups
// exactly; a broader filter counts each group up to browseTotalThreshold.
var groupMatchLimit = 10000

// Group lists use their own indexed keys; their predicates filter member
// titles with exactly the same grammar as the entity pivot they open.
func (s *Service) browseAggregate(request BrowseRequest, pivot browsePivot) (BrowseResult, error) {
	out := BrowseResult{Pivot: pivot.ID, Entries: []ContentEntry{}, PositionIndex: []BrowsePositionAnchor{}}
	sorts := request.Sort
	// A library-wide title sort may be carried onto a grouping. Keep the
	// grouping's meaningful sorts and report name order for other sorts.
	if len(sorts) != 1 || sorts[0].Field != "title" && sorts[0].Field != "count" && sorts[0].Field != "recentActivity" {
		sorts = titleAscending()
	}
	sorting := sorts[0]
	defaults := map[string]string{"title": "asc", "count": "desc", "recentActivity": "desc"}
	def, ok := defaults[sorting.Field]
	if !ok {
		return out, browseIssue("sort", "this grouping sorts by name, count or recent activity")
	}
	if sorting.Direction == "" {
		sorting.Direction = def
	}
	if sorting.Direction != "asc" && sorting.Direction != "desc" || sorting.Seed != 0 {
		return out, browseIssue("sort", "grouping sort is invalid")
	}
	sorts = []BrowseSortSelection{sorting}
	out.Applied = BrowseApplied{Query: request.Query, Sort: sorts}
	request.Limit = pageLimit(request.Limit)
	if request.Cursor != "" && request.Range != nil {
		return out, browseIssue("range", "a direct range cannot include a cursor")
	}
	// Groupings have no letter index; a carried letter jump leaves the
	// ordinary page position unchanged and is absent from Applied.
	request.Seek = nil
	revision, err := s.browseRevision(request, pivot, sorts)
	if err != nil {
		return out, err
	}
	out.PageInfo.Revision = revision
	scope := cursorScope{Library: request.Library, Viewer: request.ViewerFence, Profile: request.Profile, View: "browse:" + pivot.ID, Sort: canonicalSort(sorts), Category: queryFingerprint(request.Query), Limit: request.Limit}
	key, id := "", ""
	start := 0
	expires := time.Now().Add(BrowseCursorTTLSecond * time.Second).Unix()
	if request.Cursor != "" {
		c, err := s.decodeCursor(request.Cursor, scope)
		if err != nil {
			return out, err
		}
		if len(c.Keys) != 1 {
			return out, ErrCursor
		}
		key, id = c.Keys[0], c.ID
		expires = c.Expires
		start, err = strconv.Atoi(c.Value)
		if err != nil || start < 0 {
			return out, ErrCursor
		}
	}
	if request.Range != nil {
		if request.Range.Start < 0 || len(request.Range.Revision) > 128 || len(request.Range.AnchorID) > 256 {
			return out, browseIssue("range", "range identity is invalid")
		}
		start = request.Range.Start
	}
	// Unfiltered group keys are maintained by per-member deltas. An arbitrary
	// predicate or restriction changes the count itself, so that shape derives
	// its exact keys in SQL over indexed facet memberships.
	from := `catalog_browse_groups g`
	where := `g.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND g.field=?`
	args := []any{request.Library, pivot.Aggregate}
	// pageCounts: a broad filter's groups are counted for the returned page
	// only, each up to the threshold.
	pageCounts := false
	var countGroups func(values []string) (map[string]int, error)
	if request.Query != nil || request.Restrictions.Active() {
		compiler := browseCompiler{now: s.recommendationNow(s.browseInstant), profile: request.Profile, restrictions: request.Restrictions, count: s.countRows, ids: s.entityIDs, rows: request.Rows}
		// The grouping's titles are the library's facet holders: a value-side
		// candidate list is bounded by their kinds.
		if lib, libErr := s.library(request.Library); libErr == nil {
			compiler.kinds = facetHolderKinds[lib.Kind]
			compiler.itemPivot = lib.Kind == "movie" || lib.Kind == "music" || lib.Kind == "audiobook"
		}
		clause, err := compiler.compile(request.Query)
		if err != nil {
			return out, err
		}
		restriction, restrictionArgs := EntityRestrictionSQL("e.entity_id", request.Restrictions)
		// Only an entity that holds a value of the grouped field can be counted,
		// so the predicate is evaluated for the possible holders alone. In a
		// music library that is the songs, not also every artist and album
		// (each of which would re-walk all of its songs); in a show library the
		// shows, not also every season and episode.
		from = `(WITH matched AS MATERIALIZED (SELECT value AS entity_id FROM json_each(?))
 SELECT m.value,lower(portico_sort_title(m.value,'','')) AS name_key,count(*) AS total,max(m.recent_key) AS recent_key
 FROM matched CROSS JOIN catalog_facet_members m ON m.entity_id=matched.entity_id
 WHERE m.field=? GROUP BY m.value) g`
		where = "1"
		// The matching titles are selected once, at most one past
		// groupMatchLimit. A broad filter matches too many titles to group
		// them all: each group then counts its own matches up to one past the
		// threshold, walking its members on the facet index, and a group
		// beyond it shows the threshold as a lower bound.
		probe := append(append(append([]any{request.Library}, compiler.args...), restrictionArgs...), groupMatchLimit+1)
		ids, err := s.entityIDs(`SELECT e.entity_id FROM catalog_browse_rows e WHERE e.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND `+compactcatalog.FacetHolderSQL("e")+`
 AND (`+clause+`) AND (`+restriction+`) LIMIT ?`, probe...)
		if err != nil {
			return out, err
		}
		list, _ := json.Marshal(ids)
		args = []any{string(list), pivot.Aggregate}
		matched := len(ids)
		if matched > groupMatchLimit {
			members := `FROM catalog_facet_members m INDEXED BY catalog_facet_group_recent CROSS JOIN catalog_browse_rows e ON e.entity_id=m.entity_id
 WHERE m.library_id=g.library_id AND m.field=g.field AND m.value=g.value AND (` + clause + `) AND (` + restriction + `)`
			memberArgs := append(append([]any{}, compiler.args...), restrictionArgs...)
			capped := `(SELECT count(*) FROM (SELECT 1 ` + members + ` LIMIT ` + strconv.Itoa(browseTotalThreshold+1) + `))`
			switch sorting.Field {
			case "count":
				// Ordering by count needs every group's capped count.
				from = `(SELECT value,name_key,total,recent_key FROM (SELECT g.value AS value,g.name_key AS name_key,` + capped + ` AS total,g.recent_key AS recent_key
 FROM catalog_browse_groups g WHERE g.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND g.field=?) WHERE total>0) g`
				args = append(append([]any{}, memberArgs...), request.Library, pivot.Aggregate)
			case "recentActivity":
				// A group's newest matching member orders it; a group with
				// none is left out. Counts are taken for the page alone.
				from = `(SELECT value,name_key,0 AS total,recent_key FROM (SELECT g.value AS value,g.name_key AS name_key,(SELECT m.recent_key ` + members + ` ORDER BY m.recent_key DESC LIMIT 1) AS recent_key
 FROM catalog_browse_groups g WHERE g.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND g.field=?) WHERE recent_key IS NOT NULL) g`
				args = append(append([]any{}, memberArgs...), request.Library, pivot.Aggregate)
				pageCounts = true
			default:
				// Name order needs only whether a group has a match, which a
				// broad filter answers at the group's first members. Counts
				// are taken for the page alone.
				from = `(SELECT g.value AS value,g.name_key AS name_key,0 AS total,g.recent_key AS recent_key
 FROM catalog_browse_groups g WHERE g.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND g.field=? AND EXISTS(SELECT 1 ` + members + `)) g`
				args = append([]any{request.Library, pivot.Aggregate}, memberArgs...)
				pageCounts = true
			}
			// One statement for the page's groups: a filter's own sets (a
			// full-text match, a profile's state) are then built once.
			countGroups = func(values []string) (map[string]int, error) {
				list, _ := json.Marshal(values)
				rows, err := s.read().Query(`SELECT g.value,`+capped+` FROM catalog_browse_groups g WHERE g.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND g.field=? AND g.value IN(SELECT value FROM json_each(?))`,
					append(append([]any{}, memberArgs...), request.Library, pivot.Aggregate, string(list))...)
				if err != nil {
					return nil, err
				}
				defer rows.Close()
				out := map[string]int{}
				for rows.Next() {
					var value string
					var n int
					if err = rows.Scan(&value, &n); err != nil {
						return nil, err
					}
					out[value] = n
				}
				return out, rows.Err()
			}
		}
	}
	orderKey := map[string]string{"title": "g.name_key", "count": "g.total", "recentActivity": "g.recent_key"}[sorting.Field]
	direction, op := "ASC", ">"
	if sorting.Direction == "desc" {
		direction, op = "DESC", "<"
	}
	type group struct {
		value, key string
		count      int
	}
	page := func(key, id string, limit int) ([]group, int, error) {
		bound := where
		a := append([]any{}, args...)
		if id != "" {
			var typed any = key
			if sorting.Field == "count" {
				n, e := strconv.Atoi(key)
				if e != nil {
					return nil, 0, ErrCursor
				}
				typed = n
			}
			bound += ` AND (` + orderKey + `,g.value)` + op + `(?,?)`
			a = append(a, typed, id)
		}
		// Evaluate membership once, then rank the much smaller group vocabulary.
		// A direct range must not re-evaluate every title for each skipped page.
		query := `WITH groups AS MATERIALIZED (SELECT g.value,g.total,` + orderKey + ` AS order_key FROM ` + from + ` WHERE ` + where + `),
 metadata AS (SELECT count(*) AS total FROM groups),
 ranked AS (SELECT value,total,order_key,row_number() OVER(ORDER BY order_key ` + direction + `,value ` + direction + `)-1 AS position FROM groups)
 SELECT metadata.total,ranked.value,ranked.total,ranked.order_key FROM metadata LEFT JOIN ranked ON ranked.position>=min(?,max(0,metadata.total-1)) AND ranked.position<min(?,max(0,metadata.total-1))+? ORDER BY ranked.position`
		if request.Range == nil {
			// Continuations seek indexed maintained keys when there is no filter.
			query = `WITH metadata AS (SELECT count(*) AS total FROM ` + from + ` WHERE ` + where + `),
 page AS (SELECT g.value,g.total,` + orderKey + ` AS order_key FROM ` + from + ` WHERE ` + bound + ` ORDER BY ` + orderKey + ` ` + direction + `,g.value ` + direction + ` LIMIT ?)
 SELECT metadata.total,page.value,page.total,page.order_key FROM metadata LEFT JOIN page ON 1 ORDER BY page.order_key ` + direction + `,page.value ` + direction
			if request.Query != nil || request.Restrictions.Active() {
				query = `WITH groups AS MATERIALIZED (SELECT * FROM ` + from + ` WHERE ` + where + `),
 metadata AS (SELECT count(*) AS total FROM groups),
 page AS (SELECT g.value,g.total,` + orderKey + ` AS order_key FROM groups g WHERE ` + bound + ` ORDER BY ` + orderKey + ` ` + direction + `,g.value ` + direction + ` LIMIT ?)
 SELECT metadata.total,page.value,page.total,page.order_key FROM metadata LEFT JOIN page ON 1 ORDER BY page.order_key ` + direction + `,page.value ` + direction
				a = append(append([]any{}, args...), a[len(args):]...)
				a = append(a, limit)
			} else {
				a = append(append(append([]any{}, args...), a...), limit)
			}
		} else {
			a = append(append([]any{}, args...), start, start, limit)
		}
		rows, err := s.read().Query(query, a...)
		if err != nil {
			return nil, 0, err
		}
		defer rows.Close()
		result := []group{}
		total := 0
		for rows.Next() {
			var g group
			var k any
			var value *string
			var count *int
			if err = rows.Scan(&total, &value, &count, &k); err != nil {
				return nil, 0, err
			}
			if value == nil {
				continue
			}
			g.value, g.count = *value, *count
			g.key = formatOrderValue(k)
			if n, ok := k.(int64); ok {
				g.key = strconv.FormatInt(n, 10)
			}
			result = append(result, g)
		}
		return result, total, rows.Err()
	}
	rows, total, err := page(key, id, request.Limit+1)
	if err != nil {
		return out, err
	}
	if pageCounts && len(rows) > 0 {
		values := []string{}
		for i := range rows {
			if i < request.Limit {
				values = append(values, rows[i].value)
			}
		}
		counts, err := countGroups(values)
		if err != nil {
			return out, err
		}
		for i := range rows {
			rows[i].count = counts[rows[i].value]
		}
	}
	if request.Range != nil && start >= total {
		start = max(0, total-1)
	}
	out.PageInfo.Total = total
	out.PageInfo.Start = start
	out.PageInfo.HasMore = len(rows) > request.Limit
	if out.PageInfo.HasMore {
		rows = rows[:request.Limit]
	}
	musicGenres := false
	if pivot.Aggregate == "genre" {
		library, err := s.library(request.Library)
		if err != nil {
			return out, err
		}
		musicGenres = library.Kind == "music"
	}
	for _, g := range rows {
		label := g.value
		if pivot.Aggregate == "decade" {
			label += "s"
		}
		count := g.count
		entry := ContentEntry{ID: pivot.Aggregate + ":" + g.value, LibraryID: request.Library, Kind: pivot.EntityKinds[0], Title: label, Count: &count, Navigation: &ContentNavigation{View: "browse", Category: pivot.Aggregate + ":" + g.value}}
		if musicGenres {
			entry.Navigation.View = "releases"
			entry.Navigation.Browse = &ContentSeeAll{Pivot: "albums", Query: &BrowseNode{Field: "genre", Operator: "equals", Value: g.value}, Sort: titleAscending()}
			entry.ArtworkPaths, err = s.musicGenreCovers(request.viewer, request.Library, g.value)
			if err != nil {
				return out, err
			}
		}
		if count > browseTotalThreshold {
			// "10,000+": the count is a lower bound.
			count = browseTotalThreshold
			bound := browseTotalThreshold
			entry.TotalAtLeast = &bound
		}
		out.Entries = append(out.Entries, entry)
	}
	if out.PageInfo.HasMore && request.Range == nil && len(rows) > 0 {
		last := rows[len(rows)-1]
		out.PageInfo.NextCursor, err = s.encodeCursor(cursorValue{EvaluationAt: s.relativeBrowseTime(request.Query), Scope: scope, Value: strconv.Itoa(start + len(rows)), ID: last.value, Keys: []string{last.key}, Total: total, Expires: expires})
	}
	return out, err
}

// facetHolderKinds are the kinds a library's groupings count
// (compactcatalog.FacetHolderSQL), by library kind.
var facetHolderKinds = map[string][]int{
	"movie": {int(compactcatalog.Movie)}, "tv": {int(compactcatalog.Show)}, "anime": {int(compactcatalog.Show)},
	"music": {int(compactcatalog.Track)}, "audiobook": {int(compactcatalog.Part)},
}
