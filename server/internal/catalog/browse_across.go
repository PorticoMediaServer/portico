package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
)

// Browsing across libraries (Justin, 14 October: the same sort and filter in
// every grouping). A person's titles, a playlist, a personal collection, a
// Home row, My List, Favorites and the viewing history span the viewer's
// libraries; here they are sorted and filtered as one list, with the same
// vocabulary as a library's own.
//
// A list across libraries is always a group. Its indexed SQL membership
// remains intact through filtering, sorting, facets and whole-view commands;
// per-request pages and capped counts never become source-length ceilings.

// AcrossLibraries is the library a queue selector names for a list across
// the viewer's libraries (clients' acrossLibraries).
const AcrossLibraries = "*"

// acrossMaximum bounds count cost only. It never truncates membership,
// sorting, paging, facets or selection. Larger totals are honest lower bounds.
var acrossMaximum = 10000

// acrossPivots are the two shapes a list across libraries is read as: the
// titles themselves, or the things that are played one at a time.
var acrossPivots = []browsePivot{
	{ID: "titles", LabelKey: "library.titles", EntityKinds: []string{"movie", "show", "album", "book"}, SupportedViews: []string{"grid", "list"}, DefaultSort: titleAscending(), Browsable: true},
	{ID: "video-titles", LabelKey: "library.titles", EntityKinds: []string{"movie", "show"}, SupportedViews: []string{"grid", "list"}, DefaultSort: titleAscending(), Browsable: true},
	{ID: "items", LabelKey: "library.items", EntityKinds: []string{"movie", "episode", "song", "audiobook_file"}, SupportedViews: []string{"list", "grid"}, DefaultSort: titleAscending(), Browsable: true},
}

func acrossPivot(id string) (browsePivot, bool) {
	if id == "" {
		id = "titles"
	}
	for _, pivot := range acrossPivots {
		if pivot.ID == id {
			return pivot, true
		}
	}
	return browsePivot{}, false
}

// ErrBrowseAcrossGroup refuses a list across libraries that names no group.
var ErrBrowseAcrossGroup = browseIssue("query", "a list across libraries needs a person, playlist, collection, Home row, My List, Favorites or history condition")

// acrossSurfaces are the groups a list across libraries can be, each with the
// condition that enters it.
var acrossSurfaces = []struct{ id, field string }{{"person-titles", "person"}, {"cast-titles", "cast"}, {"crew-titles", "crew"}, {"list-titles", "list"}, {"saved-collection-titles", "savedCollection"}, {"row-titles", "homeRow"}, {"watchlist", "watchlisted"}, {"favorites", "favorite"}, {"history", "lastPlayedAt"}}

// acrossAnchor is the group condition of a list across libraries: the query
// itself or one condition of its top-level all.
func acrossAnchor(node *BrowseNode) *BrowseNode {
	if node == nil {
		return nil
	}
	anchors := func(n *BrowseNode) bool {
		switch n.Field {
		case "person", "cast", "crew", "list", "savedCollection", "homeRow":
			_, ok := n.Value.(string)
			return ok && n.Operator == "equals"
		case "watchlisted", "favorite":
			flag, ok := n.Value.(bool)
			return ok && n.Operator == "equals" && flag
		case "lastPlayedAt":
			return n.Operator == "is-present"
		}
		return false
	}
	if node.Field != "" {
		if anchors(node) {
			return node
		}
		return nil
	}
	for i := range node.All {
		if node.All[i].Field != "" && anchors(&node.All[i]) {
			return &node.All[i]
		}
	}
	return nil
}

// A genre tile opens its counted video domain. Existing mixed groups retain
// their original anchor precedence and semantics, regardless of filter order.
func acrossAnchorForPivot(node *BrowseNode, pivot browsePivot) *BrowseNode {
	if anchor := acrossAnchor(node); anchor != nil || pivot.ID != "video-titles" || node == nil {
		return anchor
	}
	genre := func(n *BrowseNode) bool {
		value, ok := n.Value.(string)
		return n.Field == "genre" && n.Operator == "equals" && ok && value != ""
	}
	if genre(node) {
		return node
	}
	for i := range node.All {
		if genre(&node.All[i]) {
			return &node.All[i]
		}
	}
	return nil
}

func withoutSelectedGenreAnchor(query, anchor *BrowseNode) *BrowseNode {
	if anchor == nil || anchor.Field != "genre" {
		return query
	}
	if query == anchor {
		return nil
	}
	copy := *query
	copy.All = nil
	for i := range query.All {
		if &query.All[i] != anchor {
			copy.All = append(copy.All, query.All[i])
		}
	}
	if len(copy.All) == 0 {
		return nil
	}
	return &copy
}

func (s *Service) acrossMembersForViewer(anchor *BrowseNode, viewer Viewer, rows map[string][]int64, sources ...map[string]HomeRowMembership) (string, []any, bool, error) {
	if anchor.Field != "genre" {
		return acrossMembers(anchor, viewer.Profile, rows, sources...)
	}
	// This source already checks every contributing member's restriction and
	// limits titles to movie/show in authorized video libraries. Do not derive
	// its members from a result page, or from hidden episodes of a visible show.
	scope, args, _, err := s.browseMeasureScope(viewer, FacetRequest{Pivot: "video-titles"})
	if err != nil {
		return "", nil, false, err
	}
	value, _ := anchor.Value.(string)
	query, bound := genreMembership(scope, args, viewer, value)
	return query, bound, false, nil
}

// genreMembership is searchGenreMembership's titles for one genre, read from
// the genre's own term postings instead of every title's genres: the term
// sources whose label is the genre, the episodes of any such show, the visible
// titles holding those items (or a title with no members, itself), then the
// same video-title scope.
func genreMembership(scope string, args []any, viewer Viewer, value string) (string, []any) {
	memberVisible, memberArgs := viewer.itemVisibilitySQL("member.item_id")
	query := `WITH genre_sources AS MATERIALIZED (
	 SELECT ts.entity_id FROM catalog_terms mg CROSS JOIN catalog_term_sources ts INDEXED BY catalog_term_sources_term ON ts.term_id=mg.id WHERE mg.vocab=1 AND mg.label=? AND ts.label_override IS NULL
	 UNION SELECT ts.entity_id FROM catalog_term_sources ts INDEXED BY catalog_term_sources_overrides CROSS JOIN catalog_terms mg ON mg.id=ts.term_id WHERE ts.label_override=? AND mg.vocab=1
	 ), genre_items AS MATERIALIZED (
	 SELECT entity_id AS item FROM genre_sources
	 UNION SELECT ep.entity_id FROM genre_sources source CROSS JOIN catalog_episodes ep INDEXED BY catalog_episodes_show ON ep.show_id=source.entity_id
	 ), genre_titles AS MATERIALIZED (
	 SELECT member.entity_id AS id FROM genre_items CROSS JOIN catalog_browse_memberships member INDEXED BY catalog_browse_membership_item ON member.item_id=genre_items.item WHERE ` + memberVisible + `
	 UNION SELECT item FROM genre_items WHERE NOT EXISTS(SELECT 1 FROM catalog_browse_memberships member WHERE member.entity_id=genre_items.item)
	 )
	 SELECT target.entity_id AS id,0 AS pos FROM genre_titles CROSS JOIN (` + scope + `) target ON target.entity_id=genre_titles.id
	 JOIN catalog_browse_rows title ON title.entity_id=target.entity_id AND title.kind IN(1,2)
	 JOIN catalog_libraries library ON library.id=target.library_id AND library.retired=0
	 JOIN libraries public_library ON public_library.id=library.library_id AND public_library.kind IN('movie','tv','anime')
	 WHERE ?<>''`
	bound := append(append([]any{value, value}, memberArgs...), args...)
	return query, append(bound, value)
}

const acrossVideoLibraryPredicate = `e.library_id IN(SELECT cl.id FROM catalog_libraries cl JOIN libraries l ON l.id=cl.library_id WHERE cl.retired=0 AND l.kind IN('movie','tv','anime'))`

// acrossMembers is SQL yielding (id, pos): the entities the group names, in
// its own order where it has one. The relation has no total-source ceiling.
func acrossMembers(anchor *BrowseNode, profile string, rows map[string][]int64, sources ...map[string]HomeRowMembership) (string, []any, bool, error) {
	value, _ := anchor.Value.(string)
	switch anchor.Field {
	case "person", "cast", "crew":
		role := ""
		switch anchor.Field {
		case "cast":
			role = ` AND ` + castDepartment
		case "crew":
			role = ` AND NOT ` + castDepartment
		}
		return `SELECT credit.entity_id AS id,min(credit.ord) AS pos FROM catalog_people person CROSS JOIN catalog_credits credit INDEXED BY catalog_credits_person ON credit.person_id=person.id JOIN catalog_credit_labels dept ON dept.id=credit.department_id WHERE person.token=?` + role + ` GROUP BY credit.entity_id`, []any{value}, false, nil
	case "list":
		// A playlist's order is its order keys (a reorder moves keys, not positions).
		return `SELECT entry.item_id AS id,ROW_NUMBER() OVER (ORDER BY entry.order_key,entry.id) AS pos FROM catalog_playlists list JOIN catalog_playlist_entries entry INDEXED BY catalog_playlist_entries_page ON entry.playlist_id=list.id WHERE list.token=? AND list.deleted=0 AND entry.item_id IS NOT NULL ORDER BY entry.order_key,entry.id`, []any{value}, true, nil
	case "savedCollection":
		return `SELECT sre.item_id AS id,ROW_NUMBER() OVER (ORDER BY sre.order_key,sre.id) AS pos FROM saved_resource_entries sre INDEXED BY saved_resource_entries_order WHERE sre.resource_id=? AND sre.item_id IS NOT NULL ORDER BY sre.order_key,sre.id`, []any{value}, true, nil
	case "homeRow":
		if len(sources) > 0 {
			if source, ok := sources[0][value]; ok {
				return source.query, source.args, true, nil
			}
		}
		ids, ok := rows[value]
		if !ok {
			return "", nil, false, ErrBrowseHomeRow
		}
		raw, _ := json.Marshal(ids)
		return `SELECT value AS id,key AS pos FROM json_each(?)`, []any{string(raw)}, true, nil
	case "watchlisted":
		return `SELECT pi.item_id AS id,0 AS pos FROM personal_items pi WHERE pi.profile_id=? AND pi.watchlisted=1`, []any{profile}, false, nil
	case "favorite":
		return `SELECT pi.item_id AS id,0 AS pos FROM personal_items pi WHERE pi.profile_id=? AND pi.favorite=1`, []any{profile}, false, nil
	case "lastPlayedAt":
		return `SELECT pi.item_id AS id,ROW_NUMBER() OVER (ORDER BY pi.last_played_at DESC,pi.item_id DESC) AS pos FROM personal_items pi INDEXED BY personal_last_played WHERE pi.profile_id=? AND pi.last_played_at>'' ORDER BY pi.last_played_at DESC,pi.item_id DESC`, []any{profile}, true, nil
	}
	return "", nil, false, ErrBrowseAcrossGroup
}

// AcrossRequest is one page of a list across the viewer's libraries.
type AcrossRequest struct {
	ServerID       string
	HomeRowDisplay string
	Pivot          string
	Query          *BrowseNode
	Sort           []BrowseSortSelection
	Limit          int
	Cursor         string
	Range          *BrowseRange
	Rows           map[string][]int64
	RowSources     map[string]HomeRowMembership
	profile        string
	fence          string
	libraries      []string
	restriction    identity.ContentRestrictions
}

// acrossKinds is the pivot's kinds as compact kind numbers.
func acrossKinds(pivot browsePivot) ([]any, error) {
	out := []any{}
	for _, kind := range entityKindMembers(pivot) {
		parsed, err := compactcatalog.ParseKind(kind)
		if err != nil {
			return nil, err
		}
		out = append(out, int(parsed))
	}
	return out, nil
}

// BrowseAcross reads one page of a group of titles across the viewer's
// libraries, sorted and filtered with the common vocabulary.
func (s *Service) BrowseAcross(viewer Viewer, request AcrossRequest) (BrowseResult, error) {
	var instantErr error
	s, instantErr = s.withBrowseInstant(request.Query, request.Cursor)
	if instantErr != nil {
		return BrowseResult{}, instantErr
	}
	if err := s.prepareViewer(viewer); err != nil {
		return BrowseResult{}, err
	}
	if dbwork.Snapshot(s.Context()) == nil {
		var result BrowseResult
		err := dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
			var readErr error
			result, readErr = s.WithContext(ctx).BrowseAcross(viewer, request)
			return readErr
		})
		return result, err
	}
	s = s.WithRecommendationRestrictions(viewer.EffectiveRestrictions())
	request.profile, request.fence, request.libraries, request.restriction = viewer.Profile, viewer.Fence, viewer.Libraries, viewer.EffectiveRestrictions()
	out, rows, err := s.acrossSelect(request)
	if err != nil {
		return out, err
	}
	if len(rows) > 0 {
		entries, err := s.browseProjection(request.profile, rows)
		if err != nil {
			return out, err
		}
		if err = s.attachViewerRatings(request.profile, entries); err != nil {
			return out, err
		}
		if err = s.nameEntryLibraries(rows, entries); err != nil {
			return out, err
		}
		if err = s.EnrichListeningEntries(viewer, entries); err != nil {
			return out, err
		}
		out.Entries = entries
	}
	out.PageInfo.DurationMs, out.PageInfo.DurationIsLowerBound, err = s.browseQueryDuration(viewer, FacetRequest{Pivot: out.Pivot, Query: request.Query, Rows: request.Rows, RowSources: request.RowSources})
	if err != nil {
		return out, err
	}
	return out, nil
}

// acrossRevision is the state a list across libraries reads: each library's
// order and this profile's marks, the group's own revision and the request.
func (s *Service) acrossRevision(request AcrossRequest, pivot browsePivot, sorts []BrowseSortSelection) (string, error) {
	var state sql.NullString
	if err := s.read().QueryRow(`SELECT group_concat(state,',') FROM (SELECT r.library_id||':'||r.order_revision||':'||COALESCE(v.revision,0) AS state FROM library_revisions r LEFT JOIN viewer_revisions v ON v.library_id=r.library_id AND v.profile_id=? WHERE r.library_id IN(SELECT value FROM json_each(?)) ORDER BY r.library_id)`, request.profile, idsJSON(request.libraries)).Scan(&state); err != nil {
		return "", err
	}
	group, err := s.browseGroupFence(request.Query, request.Rows, request.RowSources)
	if err != nil {
		return "", err
	}
	group += s.relativeBrowseFence(request.Query)
	digest := sha256.Sum256([]byte(fmt.Sprintf("across\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s", pivot.ID, request.fence, queryFingerprint(request.Query)+"\x00display:"+request.HomeRowDisplay, canonicalSort(sorts), state.String, group)))
	return "browse_" + hex.EncodeToString(digest[:]), nil
}

func (s *Service) acrossSelect(request AcrossRequest) (BrowseResult, []browseRow, error) {
	var instantErr error
	s, instantErr = s.withBrowseInstant(request.Query, request.Cursor)
	if instantErr != nil {
		return BrowseResult{}, nil, instantErr
	}
	out := BrowseResult{Entries: []ContentEntry{}, PositionIndex: []BrowsePositionAnchor{}}
	pivot, ok := acrossPivot(request.Pivot)
	if !ok {
		return out, nil, ErrBrowsePivot
	}
	out.Pivot = pivot.ID
	sorts, err := resolveBrowseSorts(pivot, request.Sort)
	if err != nil {
		return out, nil, err
	}
	if sorts[0].Field == "forYou" {
		return out, nil, browseIssue("sort[0].field", "For you ranks one library at a time")
	}
	out.Applied = BrowseApplied{Query: request.Query, Sort: sorts}
	anchor := acrossAnchorForPivot(request.Query, pivot)
	if anchor == nil {
		return out, nil, ErrBrowseAcrossGroup
	}
	viewer := Viewer{Profile: request.profile, Fence: request.fence, Libraries: request.libraries, Restrictions: request.restriction}
	members, memberArgs, ordered, err := s.acrossMembersForViewer(anchor, viewer, request.Rows, request.RowSources)
	if err != nil {
		return out, nil, err
	}
	if sorts[0].Field == "groupOrder" && !ordered {
		return out, nil, ErrBrowseGroupOrder
	}
	limit := pageLimit(request.Limit)
	if request.Cursor != "" && request.Range != nil {
		return out, nil, browseIssue("range", "a direct range cannot include a cursor")
	}
	revision, err := s.acrossRevision(request, pivot, sorts)
	if err != nil {
		return out, nil, err
	}
	out.PageInfo.Revision = revision
	if anchor.Field == "lastPlayedAt" && (sorts[0].Field == "groupOrder" || sorts[0].Field == "lastPlayed") {
		return s.acrossHistory(request, pivot, sorts[0], out, limit)
	}

	// The group's members, and the titles that hold them or that they hold
	// (a show for a saved episode, a saved show's episodes), each at its first
	// member's place. The pivot's kinds then choose among them.
	countLimit := min(acrossMaximum, browseTotalThreshold)
	group := `WITH anchor(id,pos) AS MATERIALIZED (SELECT id,pos FROM (` + members + `)),
 ord(id,pos) AS MATERIALIZED (SELECT id,min(pos) FROM (SELECT id,pos FROM anchor
  UNION ALL SELECT bm.entity_id,anchor.pos FROM anchor CROSS JOIN catalog_browse_memberships bm INDEXED BY catalog_browse_membership_item ON bm.item_id=anchor.id
  UNION ALL SELECT bm.item_id,anchor.pos FROM anchor CROSS JOIN catalog_browse_memberships bm ON bm.entity_id=anchor.id) GROUP BY id)`
	kinds, err := acrossKinds(pivot)
	if err != nil {
		return out, nil, err
	}
	where := `e.library_id IN(SELECT id FROM catalog_libraries WHERE library_id IN(SELECT value FROM json_each(?))) AND e.kind IN(` + placeholders(len(kinds)) + `)`
	if pivot.ID == "video-titles" {
		where += ` AND ` + acrossVideoLibraryPredicate
	}
	args := append([]any{idsJSON(request.libraries)}, kinds...)
	compiler := &browseCompiler{now: s.recommendationNow(s.browseInstant), profile: request.profile, restrictions: request.restriction, rows: request.Rows, rowSources: request.RowSources}
	clause, err := compiler.compile(withoutSelectedGenreAnchor(request.Query, anchor))
	if err != nil {
		return out, nil, err
	}
	where += " AND " + clause
	args = append(args, compiler.args...)
	if live, liveArgs := EntityRestrictionSQL("e.entity_id", request.restriction); live != "1" {
		where += " AND " + live
		args = append(args, liveArgs...)
	}
	from := `ord CROSS JOIN catalog_browse_rows e ON e.entity_id=ord.id CROSS JOIN catalog_entities ce ON ce.id=e.entity_id`

	start, total, counted := 0, 0, false
	scope := cursorScope{Viewer: request.fence, Profile: request.profile, View: "across:" + pivot.ID, Sort: canonicalSort(sorts), Category: queryFingerprint(request.Query) + "\x00display:" + request.HomeRowDisplay, Limit: limit}
	expires := time.Now().Add(BrowseCursorTTLSecond * time.Second).Unix()
	if request.Cursor != "" {
		// The revision fingerprint stands for every library's revisions at once.
		cursor, err := s.decodeRevisionCursor(request.Cursor, scope, ContentRevision{})
		if err != nil {
			return out, nil, err
		}
		if cursor.Fingerprint != revision {
			return out, nil, ErrStaleContinuation
		}
		if start, err = strconv.Atoi(cursor.Value); err != nil || start < 0 {
			return out, nil, ErrCursor
		}
		expires, total, counted = cursor.Expires, cursor.Total, cursor.Total > 0
	}
	if request.Range != nil {
		if request.Range.Start < 0 || len(request.Range.Revision) > 128 || len(request.Range.AnchorID) > 256 {
			return out, nil, browseIssue("range", "range identity is invalid")
		}
		if request.Range.Revision != "" && request.Range.Revision != out.PageInfo.Revision {
			return out, nil, ErrStaleContinuation
		}
		start = request.Range.Start
	}
	key, keyArgs, order, orderArgs := acrossOrder(sorts[0], request.profile)
	// One ordered pass over the group serves its count, its letter rail and
	// each page, and is kept briefly under the list's complete revision, so a
	// continuation does not expand every member again. A list past the counted
	// maximum keeps only its head; a page or rail beyond it reads the group.
	fresh := request.Cursor == "" && (request.Range == nil || request.Range.Revision == "")
	walk, err := s.acrossOrdered(request, revision, fresh, group+` SELECT e.entity_id,`+key+`,`+acrossLetterSQL(sorts[0])+` FROM `+from+` WHERE `+where+` ORDER BY `+order+` LIMIT ?`, append(append(append(append(append([]any{}, memberArgs...), keyArgs...), args...), orderArgs...), countLimit+1)...)
	if err != nil {
		return out, nil, err
	}
	if !counted {
		total = len(walk.ids)
	}
	capped := total > countLimit
	out.PageInfo.Total, out.PageInfo.TotalIsLowerBound = min(total, countLimit), capped
	if !capped && start > 0 && start >= total {
		start = max(0, total-1)
	}
	out.PageInfo.Start = start

	// A title sort publishes where each letter starts, for the letter rail and
	// for finding a place again after a refresh: one pass over the bounded group.
	if sorts[0].Field == "title" && request.Cursor == "" && total > 0 {
		if walk.capped {
			out.PositionIndex, err = s.acrossLetters(group+` SELECT letter,MIN(position) FROM (SELECT portico_letter(e.sort_key) AS letter,ROW_NUMBER() OVER (ORDER BY `+order+`)-1 AS position FROM `+from+` WHERE `+where+`) GROUP BY letter ORDER BY MIN(position)`, append(append(append([]any{}, memberArgs...), orderArgs...), args...)...)
			if err != nil {
				return out, nil, err
			}
		} else {
			out.PositionIndex = acrossOrderedLetters(walk)
		}
	}
	selectedLimit := limit
	if capped {
		selectedLimit++
	}
	var selected []browseRow
	if !walk.capped || start+selectedLimit <= len(walk.ids) {
		selected, err = s.acrossOrderedRows(walk, start, selectedLimit, sorts[0].Field)
	} else {
		query := group + ` SELECT ` + browseRowColumns + `,` + key + ` FROM ` + from + ` WHERE ` + where + ` ORDER BY ` + order + ` LIMIT ? OFFSET ?`
		bound := append(append(append([]any{}, memberArgs...), keyArgs...), args...)
		bound = append(append(bound, orderArgs...), selectedLimit, start)
		selected, err = s.queryBrowseKeyRows(query, sorts[0].Field, bound...)
	}
	if err != nil {
		return out, nil, err
	}
	if capped {
		out.PageInfo.HasMore = len(selected) > limit
		if out.PageInfo.HasMore {
			selected = selected[:limit]
		}
	} else {
		out.PageInfo.HasMore = start+len(selected) < total
	}
	if out.PageInfo.HasMore && request.Range == nil && len(selected) > 0 {
		out.PageInfo.NextCursor, err = s.encodeRevisionCursor(cursorValue{EvaluationAt: s.relativeBrowseTime(request.Query), Scope: scope, Value: strconv.Itoa(start + len(selected)), Fingerprint: revision, Total: total, Expires: expires}, ContentRevision{})
		if err != nil {
			return out, nil, err
		}
	}
	return out, selected, nil
}

// acrossLetterSQL is each ordered row's rail letter, read only under a title
// sort, which is the only order that publishes a letter rail.
func acrossLetterSQL(selection BrowseSortSelection) string {
	if selection.Field == "title" {
		return `portico_letter(e.sort_key)`
	}
	return `''`
}

// acrossOrdered reads a list's order, reusing a recent one with the same
// revision, library state and restriction for a continuation. A fresh first
// page always reads the group again.
func (s *Service) acrossOrdered(request AcrossRequest, revision string, fresh bool, query string, args ...any) (*acrossOrdered, error) {
	witness, err := s.measureWitness("", request.libraries)
	if err != nil {
		return nil, err
	}
	key := revision + "\x00" + witness + "\x00" + RestrictionFence(request.restriction)
	cache := s.state.acrossOrders
	if !fresh && cache != nil {
		if ordered, ok := cache.get(key); ok {
			return ordered, nil
		}
	}
	rows, err := s.read().Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ordered := &acrossOrdered{}
	for rows.Next() {
		var id int64
		var value any
		var letter string
		if err = rows.Scan(&id, &value, &letter); err != nil {
			return nil, err
		}
		ordered.ids, ordered.keys, ordered.letters = append(ordered.ids, id), append(ordered.keys, browseKeyText(value)), append(ordered.letters, letter)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	ordered.capped = len(ordered.ids) > min(acrossMaximum, browseTotalThreshold)
	if cache != nil {
		cache.put(key, ordered, time.Minute)
	}
	return ordered, nil
}

// acrossOrderedLetters is each letter and the position of its first title.
func acrossOrderedLetters(ordered *acrossOrdered) []BrowsePositionAnchor {
	out := []BrowsePositionAnchor{}
	seen := map[string]bool{}
	for position, letter := range ordered.letters {
		if !seen[letter] {
			seen[letter] = true
			out = append(out, BrowsePositionAnchor{Key: letter, Index: position})
		}
	}
	return out
}

// acrossOrderedRows reads one window of an ordered list: only its rows.
func (s *Service) acrossOrderedRows(ordered *acrossOrdered, start, limit int, field string) ([]browseRow, error) {
	from, until := min(start, len(ordered.ids)), min(start+limit, len(ordered.ids))
	if from >= until {
		return []browseRow{}, nil
	}
	ids := make([]any, 0, until-from)
	for _, id := range ordered.ids[from:until] {
		ids = append(ids, id)
	}
	// Literal ids, not a subquery, so a rebuild's serving rows are searched
	// by id rather than materialized.
	rows, err := s.queryBrowseRows(`SELECT `+browseRowColumns+` FROM catalog_browse_rows e CROSS JOIN catalog_entities ce ON ce.id=e.entity_id WHERE e.entity_id IN(`+placeholders(len(ids))+`)`, ids...)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]browseRow, len(rows))
	for _, row := range rows {
		byID[row.entity] = row
	}
	out := make([]browseRow, 0, len(ids))
	for i := from; i < until; i++ {
		row, ok := byID[ordered.ids[i]]
		if !ok {
			return nil, ErrStaleContinuation
		}
		row.keys = map[string]string{field: ordered.keys[i]}
		out = append(out, row)
	}
	return out, nil
}

// acrossLetters reads a letter index: each letter and the position its first
// title holds in the list.
func (s *Service) acrossLetters(query string, args ...any) ([]BrowsePositionAnchor, error) {
	out := []BrowsePositionAnchor{}
	rows, err := s.read().Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var anchor BrowsePositionAnchor
		if err = rows.Scan(&anchor.Key, &anchor.Index); err != nil {
			return nil, err
		}
		out = append(out, anchor)
	}
	return out, rows.Err()
}

// historyOrder is an order the viewing history across libraries is read in
// straight from the profile's own rows: the index, the ordered column and its
// direction. Its own order and last played are most recent first ascending
// and descending respectively; title reads the titles' sort keys the rows
// keep.
func historyOrder(selection BrowseSortSelection) (index, column, direction string) {
	descending := selection.Direction == "desc"
	switch selection.Field {
	case "groupOrder":
		descending = !descending
	case "title":
		index, column = "catalog_personal_history_title", "personal.sort_key COLLATE NOCASE"
	}
	if index == "" {
		index, column = "catalog_personal_history", "personal.last_played_at"
	}
	direction = "ASC"
	if descending {
		direction = "DESC"
	}
	return index, column, direction
}

// acrossHistory pages the viewing history across libraries in its own order
// (most recent first, or oldest first), by last played, or by title, straight
// from the maintained per-title personal rows and their history indexes, so
// it runs past any number of plays: a page reads its own rows from the index,
// filters and restrictions applied as it goes, and the total is counted up to
// the totals limit and then given as a lower bound that grows as the viewer
// pages on.
func (s *Service) acrossHistory(request AcrossRequest, pivot browsePivot, selection BrowseSortSelection, out BrowseResult, limit int) (BrowseResult, []browseRow, error) {
	kinds, err := acrossKinds(pivot)
	if err != nil {
		return out, nil, err
	}
	where := `personal.profile_id=? AND personal.last_played_at<>'' AND e.library_id IN(SELECT id FROM catalog_libraries WHERE library_id IN(SELECT value FROM json_each(?))) AND e.kind IN(` + placeholders(len(kinds)) + `)`
	if pivot.ID == "video-titles" {
		where += ` AND ` + acrossVideoLibraryPredicate
	}
	args := append([]any{request.profile, idsJSON(request.libraries)}, kinds...)
	compiler := &browseCompiler{now: s.recommendationNow(s.browseInstant), profile: request.profile, restrictions: request.restriction, rows: request.Rows, rowSources: request.RowSources}
	clause, err := compiler.compile(request.Query)
	if err != nil {
		return out, nil, err
	}
	where += " AND " + clause
	args = append(args, compiler.args...)
	if live, liveArgs := EntityRestrictionSQL("e.entity_id", request.restriction); live != "1" {
		where += " AND " + live
		args = append(args, liveArgs...)
	}
	index, column, direction := historyOrder(selection)
	from := `catalog_personal_sort_rows personal INDEXED BY ` + index + ` CROSS JOIN catalog_browse_rows e ON e.entity_id=personal.entity_id CROSS JOIN catalog_entities ce ON ce.id=e.entity_id`
	start, counted, total := 0, false, 0
	scope := cursorScope{Viewer: request.fence, Profile: request.profile, View: "across:history:" + pivot.ID, Sort: canonicalSort([]BrowseSortSelection{selection}), Category: queryFingerprint(request.Query) + "\x00display:" + request.HomeRowDisplay, Limit: limit}
	expires := time.Now().Add(BrowseCursorTTLSecond * time.Second).Unix()
	if request.Cursor != "" {
		cursor, err := s.decodeRevisionCursor(request.Cursor, scope, ContentRevision{})
		if err != nil {
			return out, nil, err
		}
		if cursor.Fingerprint != out.PageInfo.Revision {
			return out, nil, ErrStaleContinuation
		}
		if start, err = strconv.Atoi(cursor.Value); err != nil || start < 0 {
			return out, nil, ErrCursor
		}
		expires, total, counted = cursor.Expires, cursor.Total, cursor.Total > 0
	}
	if request.Range != nil {
		if request.Range.Start < 0 || len(request.Range.Revision) > 128 || len(request.Range.AnchorID) > 256 {
			return out, nil, browseIssue("range", "range identity is invalid")
		}
		if request.Range.Revision != "" && request.Range.Revision != out.PageInfo.Revision {
			return out, nil, ErrStaleContinuation
		}
		start = request.Range.Start
	}
	if !counted {
		if err = s.read().QueryRow(`SELECT count(*) FROM (SELECT 1 FROM `+from+` WHERE `+where+` LIMIT ?)`, append(append([]any{}, args...), browseTotalThreshold+1)...).Scan(&total); err != nil {
			return out, nil, err
		}
	}
	capped := total > browseTotalThreshold || counted && total >= browseTotalThreshold
	if capped && !counted {
		total = browseTotalThreshold
	}
	if !capped && start > 0 && start >= total {
		start = max(0, total-1)
	}
	out.PageInfo.Start = start
	key := strings.TrimSuffix(column, " COLLATE NOCASE")
	selected, err := s.queryBrowseKeyRows(`SELECT `+browseRowColumns+`,`+key+` FROM `+from+` WHERE `+where+` ORDER BY `+column+` `+direction+`,personal.entity_id `+direction+` LIMIT ? OFFSET ?`, selection.Field, append(append([]any{}, args...), limit+1, start)...)
	if err != nil {
		return out, nil, err
	}
	more := len(selected) > limit
	if more {
		selected = selected[:limit]
	}
	if capped {
		// Past the counted limit the total only says "at least": it grows with
		// the pages read, and stays open while more remain.
		total = max(total, start+len(selected))
		if more {
			total = max(total, start+len(selected)+1)
		}
	}
	out.PageInfo.Total, out.PageInfo.TotalIsLowerBound, out.PageInfo.HasMore = total, capped && more, more
	if more && request.Range == nil && len(selected) > 0 {
		out.PageInfo.NextCursor, err = s.encodeRevisionCursor(cursorValue{EvaluationAt: s.relativeBrowseTime(request.Query), Scope: scope, Value: strconv.Itoa(start + len(selected)), Fingerprint: out.PageInfo.Revision, Total: total, Expires: expires}, ContentRevision{})
		if err != nil {
			return out, nil, err
		}
	}
	return out, selected, nil
}

// acrossOrder is a sort over the members of a list across libraries: the key
// a row carries and the full ORDER BY, each with its binds. A personal order
// lists the titles with a value first (largest or smallest by the direction)
// and the rest in title order, as a library's does.
func acrossOrder(selection BrowseSortSelection, profile string) (key string, keyArgs []any, order string, orderArgs []any) {
	definition, _ := browseSortByID(selection.Field)
	direction := "ASC"
	if selection.Direction == "desc" {
		direction = "DESC"
	}
	tie := `,e.sort_key COLLATE NOCASE ASC,e.entity_id ` + direction
	switch {
	case definition.ID == "groupOrder":
		return `ord.pos`, nil, `ord.pos ` + direction + `,e.entity_id ` + direction, nil
	case definition.Profile:
		column, zero, _ := personalSortColumn(definition.ID)
		key = `COALESCE((SELECT personal.` + column + ` FROM catalog_personal_sort_rows personal WHERE personal.profile_id=? AND personal.entity_id=e.entity_id),` + zero + `)`
		return key, []any{profile}, `CASE WHEN ` + key + `>` + zero + ` THEN 0 ELSE 1 END ASC,` + key + ` ` + direction + tie, []any{profile, profile}
	case definition.ID == "random":
		op := ">="
		if direction == "DESC" {
			op = "<="
		}
		return definition.Expression, nil, `CASE WHEN ` + definition.Expression + op + `? THEN 0 ELSE 1 END ASC,` + definition.Expression + ` ` + direction + `,e.entity_id ` + direction, []any{int64(selection.Seed)}
	}
	return definition.Expression, nil, definition.Expression + ` ` + direction + tie, nil
}

// nameEntryLibraries says which library each entry of a list across
// libraries is in, so a card opens its title where it lives.
func (s *Service) nameEntryLibraries(rows []browseRow, entries []ContentEntry) error {
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.entity)
	}
	raw, _ := json.Marshal(ids)
	found, err := s.read().Query(`SELECT pid(ce.public_id),l.library_id FROM json_each(?) j JOIN catalog_entities ce ON ce.id=j.value JOIN catalog_libraries l ON l.id=ce.library_id`, string(raw))
	if err != nil {
		return err
	}
	defer found.Close()
	libraries := map[string]string{}
	for found.Next() {
		var id, library string
		if err = found.Scan(&id, &library); err != nil {
			return err
		}
		libraries[id] = library
	}
	if err = found.Err(); err != nil {
		return err
	}
	for i := range entries {
		if entries[i].LibraryID == "" {
			entries[i].LibraryID = libraries[entries[i].ID]
		}
	}
	return nil
}

// BrowseAcrossCapabilities is the vocabulary of lists across libraries: the
// fields and sorts that apply to the pivot's kinds in any of the viewer's
// libraries, and the groups such a list can be.
func (s *Service) BrowseAcrossCapabilities(viewer Viewer, pivot string) (BrowseCapabilities, error) {
	out := BrowseCapabilities{Pivots: []BrowsePivotCapability{}, Fields: []BrowseFieldCapability{}, Sorts: []BrowseSortCapability{}, QuickFilters: []BrowseQuickFilter{}, QueryLimits: browseQueryLimits(), Surfaces: []BrowseSurfaceCapability{}}
	if err := s.prepareViewer(viewer); err != nil {
		return out, err
	}
	resolved, ok := acrossPivot(pivot)
	if !ok {
		return out, requestinput.New("pivot is not published for lists across libraries")
	}
	sortsFor := func(entry browsePivot) []string {
		allowed := map[string]bool{}
		for _, kind := range entityKindMembers(entry) {
			allowed[kind] = true
		}
		ids := []string{}
		for _, definition := range browseSorts {
			if definition.ID != "forYou" && anyKind(definition.ApplicableKinds, allowed) {
				ids = append(ids, definition.ID)
			}
		}
		return ids
	}
	var online bool
	if err := s.read().QueryRow(`SELECT EXISTS(SELECT 1 FROM libraries l LEFT JOIN library_metadata_agents a ON a.library_id=l.id WHERE l.id IN(SELECT value FROM json_each(?)) AND l.kind IN('movie','tv','anime','music') AND COALESCE(a.agent,'online')='online')`, viewer.librariesJSON()).Scan(&online); err != nil {
		return out, err
	}
	fieldsFor := func(entry browsePivot) []string {
		allowed := map[string]bool{}
		for _, kind := range entityKindMembers(entry) {
			allowed[kind] = true
		}
		ids := []string{}
		for _, field := range browseFields {
			if field.ID == "match" && !online {
				continue
			}
			if anyKind(field.ApplicableKinds, allowed) && field.ID != "memberOf" && field.ID != "collection" && field.ID != "episodeGroup" {
				ids = append(ids, field.ID)
			}
		}
		return ids
	}
	for _, entry := range acrossPivots {
		capability := BrowsePivotCapability{ID: entry.ID, LabelKey: entry.LabelKey, EntityKinds: entry.EntityKinds, DefaultSort: entry.DefaultSort, SupportedViews: entry.SupportedViews, Browsable: true, Sorts: sortsFor(entry), Filters: fieldsFor(entry), Paging: "offset"}
		out.Pivots = append(out.Pivots, capability)
		if entry.ID == resolved.ID {
			copied := capability
			out.ResolvedPivot = &copied
		}
	}
	filters := out.ResolvedPivot.Filters
	for _, id := range filters {
		field, _ := browseFieldByID(id)
		capability := BrowseFieldCapability{ID: field.ID, LabelKey: field.LabelKey, Type: string(field.Type), Operators: field.Operators, ControlHint: field.ControlHint, Complexity: field.Complexity, Cost: field.Cost, ApplicableKinds: field.ApplicableKinds, AllowedValues: field.AllowedValues}
		if field.Facet != "" {
			capability.FacetSource = &BrowseFacetSource{Endpoint: "/v1/browse/facets", Field: field.Facet}
		}
		out.Fields = append(out.Fields, capability)
	}
	for _, id := range out.ResolvedPivot.Sorts {
		entry, _ := browseSortByID(id)
		out.Sorts = append(out.Sorts, BrowseSortCapability{ID: entry.ID, LabelKey: entry.LabelKey, Directions: entry.Directions, DefaultDirection: entry.DefaultDirection, Seeded: entry.ID == "random", OwnerOnly: entry.ID == "fileSize", RequiresGroup: entry.Ordering == -3, ApplicableKinds: entry.ApplicableKinds})
	}
	for _, quick := range BrowseQuickFilters() {
		if containsString(filters, quick.Query.Field) {
			out.QuickFilters = append(out.QuickFilters, quick)
		}
	}
	for _, surface := range acrossSurfaces {
		out.Surfaces = append(out.Surfaces, BrowseSurfaceCapability{ID: surface.id, Endpoint: "/v1/browse", MembershipField: surface.field, Sorts: out.ResolvedPivot.Sorts, Filters: filters, Paging: "offset"})
	}
	if out.ResolvedPivot.ID == "video-titles" {
		out.Surfaces = append(out.Surfaces, BrowseSurfaceCapability{ID: "genre-titles", Endpoint: "/v1/browse", MembershipField: "genre", Sorts: out.ResolvedPivot.Sorts, Filters: filters, Paging: "offset"})
	}
	return out, nil
}

// FacetsAcross counts a facet's values over the viewer's libraries: each
// library's maintained counts, added together by value. A list's own facets
// (request.Group) are counted over the list's titles instead.
func (s *Service) FacetsAcross(viewer Viewer, request FacetRequest) (FacetPage, error) {
	return s.contextualFacets(viewer, request)
}

// acrossQueue is what playing a list across libraries plays: its titles in
// the order shown (the same members, conditions and order as its pages),
// each title's playable items in their own order.
func (s *Service) acrossQueue(ctx context.Context, tx *sql.Tx, viewer Viewer, query *QueueQuery, rows map[string][]int64) (QueueSelection, error) {
	pivot, ok := acrossPivot(query.Pivot)
	if !ok {
		return QueueSelection{}, ErrBrowsePivot
	}
	sorts, err := resolveBrowseSorts(pivot, query.Sort)
	if err != nil {
		return QueueSelection{}, err
	}
	if sorts[0].Field == "forYou" {
		return QueueSelection{}, browseIssue("sort[0].field", "For you ranks one library at a time")
	}
	anchor := acrossAnchorForPivot(query.Query, pivot)
	if anchor == nil {
		return QueueSelection{}, ErrBrowseAcrossGroup
	}
	members, memberArgs, ordered, err := s.acrossMembersForViewer(anchor, viewer, rows)
	if err != nil {
		return QueueSelection{}, err
	}
	if sorts[0].Field == "groupOrder" && !ordered {
		return QueueSelection{}, ErrBrowseGroupOrder
	}
	kinds, err := acrossKinds(pivot)
	if err != nil {
		return QueueSelection{}, err
	}
	where := `e.library_id IN(SELECT id FROM catalog_libraries WHERE library_id IN(SELECT value FROM json_each(?))) AND e.kind IN(` + placeholders(len(kinds)) + `)`
	if pivot.ID == "video-titles" {
		where += ` AND ` + acrossVideoLibraryPredicate
	}
	args := append([]any{idsJSON(viewer.Libraries)}, kinds...)
	compiler := &browseCompiler{now: s.recommendationNow(s.browseInstant), profile: viewer.Profile, restrictions: viewer.Restrictions, rows: rows}
	clause, err := compiler.compile(withoutSelectedGenreAnchor(query.Query, anchor))
	if err != nil {
		return QueueSelection{}, err
	}
	where += " AND " + clause
	args = append(args, compiler.args...)
	if live, liveArgs := EntityRestrictionSQL("e.entity_id", viewer.Restrictions); live != "1" {
		where += " AND " + live
		args = append(args, liveArgs...)
	}
	_, _, order, orderArgs := acrossOrder(sorts[0], viewer.Profile)
	visible, visibleArgs := viewer.itemVisibilitySQL("i.id")
	group := `WITH anchor(id,pos) AS MATERIALIZED (SELECT id,pos FROM (` + members + `)),
 ord(id,pos) AS MATERIALIZED (SELECT id,min(pos) FROM (SELECT id,pos FROM anchor
  UNION ALL SELECT bm.entity_id,anchor.pos FROM anchor CROSS JOIN catalog_browse_memberships bm INDEXED BY catalog_browse_membership_item ON bm.item_id=anchor.id
  UNION ALL SELECT bm.item_id,anchor.pos FROM anchor CROSS JOIN catalog_browse_memberships bm ON bm.entity_id=anchor.id) GROUP BY id)`
	ranked := group + `, ranked AS (SELECT e.entity_id,row_number() OVER(ORDER BY ` + order + `) AS ordinal FROM ord CROSS JOIN catalog_browse_rows e ON e.entity_id=ord.id WHERE ` + where + `)`
	if anchor.Field == "lastPlayedAt" && (sorts[0].Field == "groupOrder" || sorts[0].Field == "lastPlayed" || sorts[0].Field == "title") {
		// The whole history, in its order, from the profile's own rows: a
		// queue of it is not cut at the members a list reads.
		index, column, direction := historyOrder(sorts[0])
		ranked = `WITH ranked AS (SELECT e.entity_id,row_number() OVER(ORDER BY ` + column + ` ` + direction + `,personal.entity_id ` + direction + `) AS ordinal
 FROM catalog_personal_sort_rows personal INDEXED BY ` + index + ` CROSS JOIN catalog_browse_rows e ON e.entity_id=personal.entity_id
 WHERE personal.profile_id=? AND personal.last_played_at<>'' AND ` + where + `)`
		memberArgs, orderArgs = []any{viewer.Profile}, nil
	}
	query2 := ranked + `, members AS (
 SELECT i.id,min(r.ordinal) AS ordinal FROM ranked r JOIN catalog_browse_memberships bm ON bm.entity_id=r.entity_id JOIN catalog_entities i ON i.id=bm.item_id JOIN catalog_kinds k ON k.id=i.kind AND k.playable=1
 WHERE ` + visible + recordingsClause("i.id", viewer.Restrictions) + ` AND NOT EXISTS(SELECT 1 FROM dvr_catalog_retirements gone WHERE gone.item_id=i.id) GROUP BY i.id)
 SELECT pid(i.public_id) FROM members m JOIN catalog_entities i ON i.id=m.id
 LEFT JOIN catalog_episodes ep ON ep.entity_id=i.id LEFT JOIN catalog_seasons season ON season.entity_id=ep.season_id
 LEFT JOIN catalog_songs song ON song.entity_id=i.id LEFT JOIN catalog_entities album ON album.id=song.album_id LEFT JOIN catalog_book_files part ON part.entity_id=i.id
 ORDER BY m.ordinal,COALESCE(season.number,1),COALESCE(ep.number,0),COALESCE(album.year,0),COALESCE(album.sort_key,''),COALESCE(album.id,0),COALESCE(song.disc_number,part.disc_number,1),COALESCE(song.track_number,part.part_number,0),i.id`
	bound := append(append(append(append([]any{}, memberArgs...), orderArgs...), args...), visibleArgs...)
	revision, err := s.acrossRevision(AcrossRequest{Query: query.Query, Rows: rows, profile: viewer.Profile, fence: viewer.Fence, libraries: viewer.Libraries}, pivot, sorts)
	if err != nil {
		return QueueSelection{}, err
	}
	served, err := compactcatalog.SearchServingSQL(ctx, tx, compactcatalog.ServingSQL(query2))
	return QueueSelection{SQL: served, Args: bound, Revision: revision}, err
}
