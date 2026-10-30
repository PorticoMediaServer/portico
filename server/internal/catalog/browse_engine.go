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
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/sorttext"
)

// ErrBrowsePivot is returned when a caller names a pivot the library kind does
// not publish.
var ErrBrowsePivot = requestinput.New("pivot is not published for this library")

type BrowseSortSelection struct {
	Field     string `json:"field"`
	Direction string `json:"direction"`
	Seed      uint32 `json:"seed,omitempty"`
}
type BrowseRange struct {
	Start    int    `json:"start"`
	Revision string `json:"revision,omitempty"`
	AnchorID string `json:"anchorId,omitempty"`
}
type BrowseSeek struct {
	Prefix string `json:"prefix"`
}
type BrowsePositionAnchor struct {
	Key   string `json:"key"`
	Index int    `json:"index"`
}
type BrowsePageInfo struct {
	Start int `json:"start"`
	Total int `json:"total"`
	// TotalIsLowerBound is set when a posted filter matched more than
	// browseTotalThreshold titles: Total is then the threshold, a lower
	// bound, and pages continue by their cursors.
	TotalIsLowerBound    bool   `json:"totalIsLowerBound"`
	Revision             string `json:"revision"`
	HasMore              bool   `json:"hasMore"`
	NextCursor           string `json:"nextCursor"`
	DurationMs           int64  `json:"durationMs"`
	DurationIsLowerBound bool   `json:"durationIsLowerBound"`
}

type BrowseApplied struct {
	Query *BrowseNode           `json:"query"`
	Sort  []BrowseSortSelection `json:"sort"`
	Seek  *BrowseSeek           `json:"seek,omitempty"`
}
type BrowseResult struct {
	selectionRelease func()
	Pivot            string                 `json:"pivot"`
	Applied          BrowseApplied          `json:"applied"`
	Entries          []ContentEntry         `json:"entries"`
	PageInfo         BrowsePageInfo         `json:"pageInfo"`
	PositionIndex    []BrowsePositionAnchor `json:"positionIndex"`
	DisplayTargets   []HomeDisplayTarget    `json:"displayTargets,omitempty"`
	// WholeSelection is the exact whole-view aggregate for "Select all N".
	// Absent whenever the view cannot be replayed exactly; never inferred.
	WholeSelection            *BrowseSelectionDescriptor  `json:"wholeSelection,omitempty"`
	WholeSelectionPreparing   *BrowseSelectionPreparing   `json:"wholeSelectionPreparing,omitempty"`
	WholeSelectionUnavailable *BrowseSelectionUnavailable `json:"wholeSelectionUnavailable,omitempty"`
	// Bound is the exact authorized WHERE this page was read with, for the
	// same-snapshot whole-selection aggregate. Never serialized.
	Bound *BoundBrowseQuery `json:"-"`
}

// BrowseRequest is the whole of the POST body plus the authorized scope the
// route resolved. Profile is the qualified personal key; ViewerFence folds the
// authorization facts into the revision so a permission change invalidates a
// range the same way a catalog change does.
type BrowseRequest struct {
	// preparedTotal is admitted only by BrowseReadingWithPreparedCount in the
	// same snapshot. It is never decoded from a public request.
	preparedTotal       *ReadingCountResult
	readingValidateOnly bool
	// The HTTP boundary sets this only after admitting the original ordinary
	// selected viewer in the same Reading snapshot. Browse alone grants no
	// personal state to internal or narrowed-session callers.
	ReadingPersonalAuthorize func(*sql.Tx) error
	SelectionBasis           string
	ServerID                 string
	Library                  string
	Profile                  string
	ViewerFence              string
	HomeRowDisplay           string
	Pivot                    string
	Query                    *BrowseNode
	Sort                     []BrowseSortSelection
	Limit                    int
	Cursor                   string
	Range                    *BrowseRange
	Seek                     *BrowseSeek
	// Restrictions is the viewer's content restriction. The browse engine applies
	// it in browseSelect; see restrictions.go for the predicate it compiles.
	Restrictions identity.ContentRestrictions
	// Rows are the Home rows the query names, resolved by the caller to their
	// titles in order (ResolveHomeRows); a homeRow condition reads nothing else.
	Rows       map[string][]int64
	RowSources map[string]HomeRowMembership
	viewer     Viewer
	// scope overrides the cursor scope so the legacy GET surface keeps its
	// existing continuation fencing.
	scope *cursorScope
}

func canonicalSort(selection []BrowseSortSelection) string {
	parts := make([]string, 0, len(selection))
	for _, entry := range selection {
		part := entry.Field + ":" + entry.Direction
		if entry.Field == "random" {
			part += fmt.Sprintf(":%d", entry.Seed)
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, ",")
}

func queryFingerprint(node *BrowseNode) string {
	if node == nil {
		return ""
	}
	raw, err := json.Marshal(node)
	if err != nil {
		return "invalid"
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:16])
}

// resolveBrowseSorts falls back to the pivot default and rejects anything the
// capabilities do not publish, naming the offending path.
func resolveBrowseSorts(pivot browsePivot, selection []BrowseSortSelection) ([]BrowseSortSelection, error) {
	if len(selection) == 0 {
		return append([]BrowseSortSelection{}, pivot.DefaultSort...), nil
	}
	if len(selection) > BrowseMaximumSorts {
		return nil, browseIssue("sort", fmt.Sprintf("at most %d sort entries are supported", BrowseMaximumSorts))
	}
	kindFilter := map[string]bool{}
	for _, kind := range entityKindMembers(pivot) {
		kindFilter[kind] = true
	}
	seen := map[string]bool{}
	out := make([]BrowseSortSelection, 0, len(selection))
	for index, entry := range selection {
		path := fmt.Sprintf("sort[%d]", index)
		definition, ok := browseSortByID(entry.Field)
		if !ok {
			return nil, browseIssue(path+".field", fmt.Sprintf("sort %q is not supported", entry.Field))
		}
		readingPivot := kindFilter["ebook"] || kindFilter["comic"]
		if readingPivot && entry.Field != "title" && entry.Field != "year" && entry.Field != "added" || !readingPivot && !anyKind(definition.ApplicableKinds, kindFilter) {
			return nil, browseIssue(path+".field", fmt.Sprintf("sort %q does not apply to this pivot", entry.Field))
		}
		if seen[entry.Field] {
			return nil, browseIssue(path+".field", "a sort field may appear once")
		}
		if entry.Field == "forYou" && len(selection) > 1 {
			return nil, browseIssue(path+".field", "forYou orders on its own")
		}
		seen[entry.Field] = true
		direction := entry.Direction
		if direction == "" {
			direction = definition.DefaultDirection
		}
		if !containsString(definition.Directions, direction) {
			return nil, browseIssue(path+".direction", fmt.Sprintf("sort %q takes direction %s", entry.Field, strings.Join(definition.Directions, " or ")))
		}
		if entry.Seed != 0 && entry.Field != "random" {
			return nil, browseIssue(path+".seed", "only random uses a seed")
		}
		out = append(out, BrowseSortSelection{Field: entry.Field, Direction: direction, Seed: entry.Seed})
	}
	return out, nil
}

// browseKeyedSorts reports whether every selected sort is a column of
// `catalog_browse_rows`, which is what lets a page seek to its cursor rather than
// skip to it. Profile sorts use their own indexed valued/rest streams.
func browseKeyedSorts(selection []BrowseSortSelection) bool {
	for _, entry := range selection {
		definition, ok := browseSortByID(entry.Field)
		if !ok || definition.Ordering == -1 || definition.Profile {
			return false
		}
	}
	return len(selection) > 0
}

// browseBoundedSQL merges two disjoint, bounded index runs. An expanded OR
// can scan a library prefix; a tuple over an expression can scan every tie.
// Each arm here seeks either (key, id) or the next key, even with many ties.
// Each arm selects its rows' columns itself: joining the selected ids back to
// the serving view during a rebuild would materialize the whole library.
// `page.order_key` in columns is the row's sort key.
func browseBoundedSQL(columns, from, where string, args []any, expression, identity string, desc, inclusive bool, key any, anchor int64, limit, offset int) (string, []any) {
	op, direction := ">", "ASC"
	if desc {
		op, direction = "<", "DESC"
	}
	tie := op
	if inclusive {
		tie += "="
	}
	inner, outer := boundedColumns(columns)
	order := expression + " " + direction + "," + identity + " " + direction
	arm := `SELECT ` + inner + `,` + expression + ` AS order_key,` + identity + ` AS id FROM ` + from + ` WHERE ` + where
	// The tie arm orders by the identity alone: its key is fixed by equality,
	// and SQLite does not see that an equality on an expression index's key
	// satisfies an ORDER BY on the expression, so it would sort every tie
	// (a whole library of equal durations) before the limit.
	query := `SELECT ` + outer + ` FROM (
 SELECT * FROM (` + arm + ` AND ` + expression + `=? AND ` + identity + tie + `? ORDER BY ` + identity + " " + direction + ` LIMIT ?)
 UNION ALL SELECT * FROM (` + arm + ` AND ` + expression + op + `? ORDER BY ` + order + ` LIMIT ?)) page
 ORDER BY page.order_key ` + direction + `,page.id ` + direction + ` LIMIT ? OFFSET ?`
	bound := append(append([]any{}, args...), key, anchor, limit+offset)
	bound = append(bound, args...)
	bound = append(bound, key, limit+offset)
	bound = append(bound, limit, offset)
	return query, bound
}

// boundedColumns names each selected column for an arm and reads it back from
// the merged page under the same output name when it is a plain column.
func boundedColumns(columns string) (inner, outer string) {
	parts := splitSQLList(columns)
	inners, outers := make([]string, 0, len(parts)), make([]string, 0, len(parts))
	used := map[string]bool{"order_key": true, "id": true}
	for i, column := range parts {
		if column == "page.order_key" {
			outers = append(outers, column)
			continue
		}
		name := fmt.Sprintf("c%d", i)
		if dot := strings.LastIndexByte(column, '.'); dot > 0 && plainSQLName(column[:dot]) && plainSQLName(column[dot+1:]) && !used[column[dot+1:]] {
			name = column[dot+1:]
		}
		used[name] = true
		inners = append(inners, column+" AS "+name)
		outers = append(outers, "page."+name)
	}
	return strings.Join(inners, ","), strings.Join(outers, ",")
}

func plainSQLName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// splitSQLList splits a select list at its top-level commas.
func splitSQLList(list string) []string {
	out := []string{}
	depth, quoted, start := 0, false, 0
	for i := 0; i < len(list); i++ {
		switch c := list[i]; {
		case c == '\'':
			quoted = !quoted
		case quoted:
		case c == '(':
			depth++
		case c == ')':
			depth--
		case c == ',' && depth == 0:
			out = append(out, strings.TrimSpace(list[start:i]))
			start = i + 1
		}
	}
	return append(out, strings.TrimSpace(list[start:]))
}

// browseKeyValues types the cursor's stored key for comparison. A year is an
// integer column and SQLite orders text after every integer, so binding it as
// text would seek past the whole library.
func browseKeyValues(selection []BrowseSortSelection, keys []string) ([]any, bool) {
	if len(keys) != len(selection) {
		return nil, false
	}
	out := make([]any, 0, len(keys))
	for index, entry := range selection {
		switch entry.Field {
		case "year":
			year, err := strconv.Atoi(keys[index])
			if err != nil {
				return nil, false
			}
			out = append(out, year)
			continue
		case "duration", "communityRating", "criticRating", "episodeCount", "trackNumber", "fileSize", "resolution", "random":
			value, err := strconv.ParseFloat(keys[index], 64)
			if err != nil {
				return nil, false
			}
			out = append(out, value)
			continue
		}
		out = append(out, keys[index])
	}
	return out, true
}

func browseSortValue(row browseRow, field string) string {
	if row.keys != nil {
		if v, ok := row.keys[field]; ok {
			return v
		}
	}
	switch field {
	case "title":
		return row.sortKey
	case "added":
		if row.added.Valid {
			return row.added.String
		}
		return ""
	case "year":
		return strconv.Itoa(row.year)
	case "duration":
		return strconv.FormatFloat(row.duration, 'g', -1, 64)
	case "communityRating":
		return strconv.FormatFloat(row.rating, 'g', -1, 64)
	}
	return ""
}

func browseOrderSQL(profile string, selection []BrowseSortSelection) (string, []any) {
	parts := []string{}
	args := []any{}
	for _, entry := range selection {
		definition, ok := browseSortByID(entry.Field)
		if !ok {
			continue
		}
		if definition.Profile {
			args = append(args, profile)
		}
		direction := "ASC"
		if entry.Direction == "desc" {
			direction = "DESC"
		}
		parts = append(parts, definition.Expression+" "+direction)
	}
	// Ties break in the first sort's direction, so a descending order is the
	// ascending one reversed and the counted blocks serve both.
	if len(selection) > 0 && selection[0].Direction == "desc" {
		parts = append(parts, "e.entity_id DESC")
	} else {
		parts = append(parts, "e.entity_id ASC")
	}
	return strings.Join(parts, ","), args
}

// browseScopeSQL bounds every query to the authorized library and the pivot's
// entity kinds before a single user predicate is applied.
func browseScopeSQL(library string, pivot browsePivot) (string, []any) {
	members := entityKindMembers(pivot)
	args := []any{library}
	for _, kind := range members {
		parsed, err := compactcatalog.ParseKind(kind)
		if err != nil {
			return "0", nil
		}
		args = append(args, int(parsed))
	}
	// One kind is written as an equality rather than a one-element IN, because an
	// equality on the leading columns of `catalog_browse_title` lets the index
	// satisfy the ORDER BY as well as the filter, and `IN` does not. Every pivot
	// this server publishes names exactly one kind, so this is the ordinary case.
	if len(members) == 1 {
		return "e.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND e.kind=?", args
	}
	return "e.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND e.kind IN (" + placeholders(len(members)) + ")", args
}

func compactBrowseKindSQL(column string) string {
	return `CASE ` + column + ` WHEN 1 THEN 'movie' WHEN 2 THEN 'show' WHEN 3 THEN 'season' WHEN 4 THEN 'episode' WHEN 5 THEN 'artist' WHEN 6 THEN 'album' WHEN 7 THEN 'song' WHEN 8 THEN 'book' WHEN 9 THEN 'audiobook_file' WHEN 10 THEN 'collection' WHEN 11 THEN 'extra' WHEN 12 THEN 'disc' WHEN 13 THEN 'author' WHEN 20 THEN 'ebook' WHEN 21 THEN 'comic' END`
}

func (s *Service) browseRevision(request BrowseRequest, pivot browsePivot, sorts []BrowseSortSelection) (string, error) {
	revision, err := s.browseContentRevision(request.Library, request.Profile)
	if err != nil {
		return "", err
	}
	extra, err := s.browseGroupFence(request.Query, request.Rows, request.RowSources)
	if err != nil {
		return "", err
	}
	if len(sorts) > 0 && sorts[0].Field == "forYou" {
		// For you also reads the profile's taste, the recommendation data and
		// the day (rotation): a page from another of any of them is another list.
		fence, err := s.recBrowseFence(request.Profile)
		if err != nil {
			return "", err
		}
		extra += "\x00" + fence
	}
	// Only Reading continuations reuse exact live-predicate totals. Ordinary
	// browse keeps its serving/order revision: a raw decoration change must
	// not invalidate P2-13's published-generation continuation. Prepared
	// whole-view aggregates bind their complete witness separately.
	reading, err := s.readingBrowseScope(request.Library)
	if err != nil {
		return "", err
	}
	if reading != nil {
		witness, err := s.browseExactWitness(request.Library, request.Profile, request.Restrictions)
		if err != nil {
			return "", err
		}
		extra += "\x00" + witness
	}
	extra += s.relativeBrowseFence(request.Query)
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%d\x00%d\x00%s", request.Library, pivot.ID, request.ViewerFence, queryFingerprint(request.Query)+"\x00display:"+request.HomeRowDisplay, canonicalSort(sorts), revision.Catalog, revision.Viewer, extra)))
	return "browse_" + hex.EncodeToString(digest[:]), nil
}

type browseRow struct {
	keys            map[string]string
	id, kind, title string
	entity          int64
	sortKey         string
	year            int
	added           sql.NullString
	duration        float64
	rating          float64
}

// BrowseEntities executes one expression query and projects the page.
func (s *Service) BrowseEntities(viewer Viewer, request BrowseRequest) (BrowseResult, error) {
	var instantErr error
	s, instantErr = s.withBrowseInstant(request.Query, request.Cursor)
	if instantErr != nil {
		return BrowseResult{}, instantErr
	}
	if !viewer.AllowsLibrary(request.Library) {
		return BrowseResult{}, sql.ErrNoRows
	}
	if err := s.prepareViewer(viewer); err != nil {
		return BrowseResult{}, err
	}
	s = s.WithRecommendationRestrictions(viewer.EffectiveRestrictions())
	request.Profile, request.ViewerFence, request.Restrictions, request.viewer = viewer.Profile, viewer.Fence, viewer.EffectiveRestrictions(), viewer
	if dbwork.Snapshot(s.Context()) == nil {
		var result BrowseResult
		err := dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
			var readErr error
			result, readErr = s.WithContext(ctx).BrowseEntities(viewer, request)
			return readErr
		})
		return result, err
	}
	if err := s.checkBrowseSelectionBasis(viewer, request.Library, request.SelectionBasis); err != nil {
		return BrowseResult{}, err
	}
	if request.SelectionBasis != "" && selectionBatchHasLocalSearch(request.Query) {
		return s.browsePreparedSelection(viewer, request)
	}
	if request.Pivot == "playlists" {
		return s.browseMusicPlaylists(request)
	}
	out, rows, err := s.browseSelect(request)
	if errors.Is(err, ErrVisibilityBuilding) || errors.Is(err, ErrStaleContinuation) {
		if lib, lookupErr := s.library(request.Library); lookupErr == nil && isReadingLibrary(lib.Kind) {
			return out, ErrReadingProjectionChanged
		}
	}
	if errors.Is(err, ErrVisibilityBuilding) {
		out.Entries = []ContentEntry{}
		return out, nil
	}
	if err != nil {
		return out, err
	}
	readingScope, err := s.readingBrowseScope(request.Library)
	if err != nil {
		return out, err
	}
	if len(rows) > 0 {
		var entries []ContentEntry
		if readingScope != nil {
			entries, err = readingScope.project(s, rows, request.Profile, request.ReadingPersonalAuthorize)
		} else if request.SelectionBasis != "" && len(rows) > BrowseMaximumLimit {
			// Keep each existing hydrator's ordinary page bound. The proof
			// conversion is at most200 and all chunks use this same snapshot.
			entries = make([]ContentEntry, 0, len(rows))
			for start := 0; start < len(rows); start += BrowseMaximumLimit {
				var chunk []ContentEntry
				chunk, err = s.browseProjection(request.Profile, rows[start:min(start+BrowseMaximumLimit, len(rows))])
				if err != nil {
					break
				}
				entries = append(entries, chunk...)
			}
		} else {
			entries, err = s.browseProjection(request.Profile, rows)
		}
		if err != nil {
			return out, err
		}
		if err = s.attachViewerRatings(request.Profile, entries); err != nil {
			return out, err
		}
		if len(request.Sort) > 0 && request.Sort[0].Field == "latestAired" {
			if err = s.nameLatestEpisodes(rows, entries); err != nil {
				return out, err
			}
		}
		if err = s.EnrichListeningEntries(viewer, entries); err != nil {
			return out, err
		}
		out.Entries = entries
	}
	if out.PageInfo.Revision != "" && readingScope == nil {
		out.PageInfo.DurationMs, out.PageInfo.DurationIsLowerBound, err = s.browseQueryDuration(viewer, FacetRequest{Library: request.Library, Pivot: out.Pivot, Query: request.Query, Rows: request.Rows, RowSources: request.RowSources})
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

// browseSelect resolves the pivot, compiles the expression and returns the
// selected entity rows. Ranges and keyset continuations share one compiled
// clause, so a virtualized grid and a paged list agree about membership.
func (s *Service) browseSelect(request BrowseRequest) (BrowseResult, []browseRow, error) {
	var instantErr error
	s, instantErr = s.withBrowseInstant(request.Query, request.Cursor)
	if instantErr != nil {
		return BrowseResult{}, nil, instantErr
	}
	out := BrowseResult{Entries: []ContentEntry{}, PositionIndex: []BrowsePositionAnchor{}}
	lib, err := s.library(request.Library)
	if err != nil {
		return out, nil, err
	}
	if request.Pivot == "" {
		request.Pivot = defaultPivot(lib.Kind)
	}
	pivot, ok := pivotForKind(lib.Kind, request.Pivot)
	if !ok {
		return out, nil, ErrBrowsePivot
	}
	out.Pivot = pivot.ID
	readingReady, err := s.readingBrowseScope(request.Library)
	if err != nil {
		return out, nil, err
	}
	if err := s.browseReady(request.Library); err != nil {
		if readingReady != nil {
			return out, nil, ErrStaleContinuation
		}
		return out, nil, err
	}
	if !pivot.Browsable {
		if pivot.Aggregate == "" {
			return out, nil, browseIssue("pivot", "this pivot is not browsable; read it through its own surface")
		}
		aggregate, err := s.browseAggregate(request, pivot)
		return aggregate, nil, err
	}
	sorts, err := resolveBrowseSorts(pivot, request.Sort)
	if err != nil {
		return out, nil, err
	}
	out.Applied = BrowseApplied{Query: request.Query, Sort: sorts, Seek: request.Seek}
	if len(sorts) == 1 && sorts[0].Field == "forYou" {
		fixed := *s
		fixed.browseInstant = s.recommendationNow(s.browseInstant)
		s = &fixed
	}
	requestedLimit := request.Limit
	request.Limit = pageLimit(request.Limit)
	// A held whole view may resolve one bounded explicit selection (at most200).
	// Ordinary page limits and revision semantics remain unchanged.
	if request.SelectionBasis != "" && requestedLimit > 0 {
		request.Limit = min(requestedLimit, BrowseSelectionConversionLimit)
	}
	if request.Cursor != "" && request.Range != nil {
		return out, nil, browseIssue("range", "a direct range cannot include a cursor")
	}
	if request.Seek != nil && (len(request.Seek.Prefix) == 0 || len(request.Seek.Prefix) > 8) {
		return out, nil, browseIssue("seek.prefix", "seek prefix must be between 1 and 8 characters")
	}
	seekable := len(sorts) > 0 && sorts[0].Field == "title" && sorts[0].Direction == "asc"
	if request.Seek != nil && !seekable {
		return out, nil, browseIssue("seek", "seek requires the first sort to be title ascending")
	}

	where, args := browseScopeSQL(request.Library, pivot)
	compiler := &browseCompiler{now: s.recommendationNow(s.browseInstant), profile: request.Profile, restrictions: request.Restrictions, count: s.countRows, ids: s.entityIDs, itemPivot: playablePivot(pivot), rows: request.Rows, rowSources: request.RowSources}
	for _, member := range entityKindMembers(pivot) {
		if kind, err := compactcatalog.ParseKind(member); err == nil {
			compiler.kinds = append(compiler.kinds, int(kind))
		}
	}
	readingEligible, err := s.bindReadingBrowse(request.Library, compiler)
	if err != nil {
		return out, nil, err
	}
	where += " AND " + readingEligible
	clause, err := compiler.compile(request.Query)
	if err != nil {
		return out, nil, err
	}
	where += " AND " + clause
	args = append(args, compiler.args...)
	unrestricted, unrestrictedArgs := where, append([]any{}, args...)
	// The viewer's content restriction is the last clause, after the authorized
	// library scope and the client's own query, so neither can widen it.
	live, liveArgs := EntityRestrictionSQL("e.entity_id", request.Restrictions)
	// Non-Reading surfaces retain their existing restriction reuse guard.
	// Reading binds the full exact witness into its fingerprint below.
	restrictionBound := live != "1"
	if restrictionBound {
		// The published class refuses a title the viewer may see none of by
		// one key lookup, before the live test walks a refused show's every
		// episode; the live test still decides for the titles it holds.
		published, err := s.publishedClassesSQL("e.entity_id", []string{request.Library}, request.Restrictions)
		if err != nil {
			return out, nil, err
		}
		if published != "1" {
			live = published + " AND " + live
		}
		where += " AND " + live
		args = append(args, liveArgs...)
	}
	order, orderArgs := browseOrderSQL(request.Profile, sorts)

	revision, err := s.browseRevision(request, pivot, sorts)
	if err != nil {
		return out, nil, err
	}
	out.PageInfo.Revision = revision
	out.Bound = &BoundBrowseQuery{Where: where, Args: append([]any{}, args...), Library: request.Library, Pivot: pivot.ID, Sorts: sorts, Query: request.Query, Revision: revision, snapshot: dbwork.Snapshot(s.Context()), orderInstant: s.browseInstant}
	stale := request.Range != nil && request.Range.Revision != "" && request.Range.Revision != revision
	if stale && compiler.reading != nil {
		return out, nil, ErrStaleContinuation
	}
	if stale && request.HomeRowDisplay != "" {
		return out, nil, ErrStaleContinuation
	}

	// The count, the letter index and the anchor rank used to be three passes over
	// the whole pivot. When the client has posted no expression of its own — which
	// is the library grid, the case that is actually large — all three are
	// answered from the counting read model in one small read.
	summary := browseSummary{}
	if request.Query == nil && compiler.reading == nil {
		if summary, err = s.browseSummarise(request.Library, entityKindMembers(pivot), request.Restrictions); err != nil {
			return out, nil, err
		}
	}
	if request.Query != nil && compiler.reading == nil {
		if summary, err = s.availabilitySummary(request.Library, entityKindMembers(pivot), request.Query, request.Restrictions); err != nil {
			return out, nil, err
		}
	}
	// A Reading continuation carries the exact total its first page counted for
	// this revision. Counting here, before the signed cursor has been decoded,
	// expiry-checked, scope-checked and anchored in the current ready scope,
	// would charge every scroll of an unchanged Reading library for a whole
	// eligible-set recount even when the cursor is refused moments later.
	deferReadingCount := compiler.reading != nil && request.Cursor != ""
	// A posted query's total is exact: it is counted when the list opens (a pass
	// over the query's matches, the price of an ad hoc filter) and carried in
	// the signed cursor, so later pages don't count again.
	var total int
	counted := false
	if request.preparedTotal != nil {
		if compiler.reading == nil {
			return out, nil, ErrReadingProjectionChanged
		}
		total, counted = request.preparedTotal.RawTotal, true
	}
	if !counted && !deferReadingCount && (request.Query == nil || summary.exact) {
		if summary.exact {
			total = summary.total
		} else if err = s.read().QueryRow(`SELECT count(*) FROM catalog_browse_rows e WHERE `+where, args...).Scan(&total); err != nil {
			return out, nil, err
		}
		counted = true
	}

	start := 0
	scope := cursorScope{Library: request.Library, Viewer: request.ViewerFence, Profile: request.Profile, View: "browse:" + pivot.ID, Sort: canonicalSort(sorts), Category: queryFingerprint(request.Query) + "\x00display:" + request.HomeRowDisplay, Search: seekPrefix(request.Seek), Limit: request.Limit}
	if request.scope != nil {
		scope = *request.scope
	}
	expires := time.Now().Add(BrowseCursorTTLSecond * time.Second).Unix()
	seekKeys := []string{}
	seekAnchor := ""
	if request.Cursor != "" {
		if compiler.reading != nil {
			// Reading continuations retain their original expiry. The legacy
			// generic decoder can renew expired cursors for viewport recovery;
			// that behavior is not part of this new ready-generation surface.
			signed, err := s.readSignedBrowseCursor(request.Cursor)
			if err != nil {
				return out, nil, err
			}
			if signed.Expires <= time.Now().Unix() {
				return out, nil, ErrStaleContinuation
			}
		}
		cursor, err := s.decodeCursor(request.Cursor, scope)
		if err != nil {
			return out, nil, err
		}
		if cursor.Fingerprint != revision {
			return out, nil, ErrStaleContinuation
		}
		expires = cursor.Expires
		if start, err = strconv.Atoi(cursor.Value); err != nil || start < 0 {
			return out, nil, ErrCursor
		}
		seekKeys, seekAnchor = cursor.Keys, cursor.ID
		if compiler.reading != nil {
			seekKeys, err = s.readingBrowseCursorKeys(where, args, sorts, cursor.Keys, cursor.ID)
			if err != nil {
				return out, nil, err
			}
		}
		if !counted && (cursor.Total > 0 || cursor.TotalPresent && cursor.Total == 0) && (!restrictionBound || compiler.reading != nil) {
			total, counted = cursor.Total, true
		}
	}
	if request.readingValidateOnly {
		if compiler.reading == nil || request.Cursor == "" {
			return out, nil, ErrCursor
		}
		after, e := s.browseRevision(request, pivot, sorts)
		if e != nil {
			return out, nil, e
		}
		if after != revision {
			return out, nil, ErrStaleContinuation
		}
		return out, nil, nil
	}
	if request.preparedTotal != nil && request.preparedTotal.ExpiresAt > 0 && expires > request.preparedTotal.ExpiresAt {
		expires = request.preparedTotal.ExpiresAt
	}
	if !counted {
		switch {
		case request.Query == nil:
			// An unfiltered Reading continuation with an absent legacy total
			// still owes the exact total,
			// never the capped filter count.
			if err = s.read().QueryRow(`SELECT count(*) FROM catalog_browse_rows e WHERE `+where, args...).Scan(&total); err != nil {
				return out, nil, err
			}
		case restrictionBound && compiler.reading == nil:
			// The carried total was refused above because the live restriction
			// is not revision-invariant, so this page recounts after every
			// cursor check. A posted filter keeps its existing capped count and
			// lower-bound meaning, but the result is not published to the
			// revision-keyed cache: the classification can change without that
			// revision, so that entry could only ever be stale.
			if err = s.read().QueryRow(`SELECT count(*) FROM (SELECT 1 FROM catalog_browse_rows e WHERE `+where+` LIMIT `+strconv.Itoa(browseTotalThreshold+1)+`)`, args...).Scan(&total); err != nil {
				return out, nil, err
			}
		default:
			if cached, hit := s.cachedBrowseTotal(revision); hit {
				total = cached
			} else if err = s.read().QueryRow(`SELECT count(*) FROM (SELECT 1 FROM catalog_browse_rows e WHERE `+where+` LIMIT `+strconv.Itoa(browseTotalThreshold+1)+`)`, args...).Scan(&total); err != nil {
				return out, nil, err
			} else {
				s.storeBrowseTotal(revision, total)
			}
		}
	}
	lowerBound := request.Query != nil && !summary.exact && total > browseTotalThreshold
	out.PageInfo.Total, out.PageInfo.TotalIsLowerBound = total, lowerBound
	if lowerBound {
		out.PageInfo.Total = browseTotalThreshold
	}
	if request.Range != nil {
		if request.Range.Start < 0 || len(request.Range.Revision) > 128 || len(request.Range.AnchorID) > 256 {
			return out, nil, browseIssue("range", "range identity is invalid")
		}
		start = request.Range.Start
	}

	if request.Query != nil && len(sorts) > 0 && sorts[0].Field == "title" && (request.Cursor == "" || request.Seek != nil) {
		anchors, hit := s.cachedAnchors(out.PageInfo.Revision)
		if s.readingCountMemoryActive() {
			anchors = nil
			hit = false
		}
		if !hit {
			if !restrictionBound && len(sorts) == 1 && !s.readingCountMemoryActive() {
				// The rail's pass also publishes the result's sparse range
				// anchors, so a deep range after it does not walk its prefix.
				var ranges []rangeAnchor
				anchors, ranges, err = s.browseLetterAndRangeIndex(where, order, args, orderArgs)
				if err == nil {
					s.storeRangeAnchors(out.PageInfo.Revision, ranges)
				}
			} else {
				anchors, err = s.browsePositionIndex(where, order, args, orderArgs)
			}
			if err != nil {
				return out, nil, err
			}
			if !s.readingCountMemoryActive() {
				s.storeAnchors(out.PageInfo.Revision, anchors)
			}
		}
		out.PositionIndex = anchors
	}
	// The letter index is only meaningful under an ascending title sort; it is
	// computed once per range so a scrub rail and the grid share one ranking.
	if request.Query == nil && seekable && (request.Cursor == "" || request.Seek != nil) {
		if summary.exact {
			if summary.letter != nil {
				out.PositionIndex = summary.letter
			}
		} else {
			anchors, err := s.browsePositionIndex(where, order, args, orderArgs)
			if err != nil {
				return out, nil, err
			}
			out.PositionIndex = anchors
		}
	} else if request.Query == nil && compiler.reading == nil && request.Cursor == "" && len(sorts) > 0 && browseHasPositionAnchors(sorts[0].Field) {
		// Every other sort publishes anchors too (M10): title descending by
		// letter, the rest by value buckets.
		var anchors []BrowsePositionAnchor
		var err error
		cached, hit := s.cachedAnchors(out.PageInfo.Revision)
		if hit {
			anchors = cached
		} else {
			anchors, err = s.compactBrowseSortAnchors(request, pivot, sorts[0])
		}
		if err != nil {
			return out, nil, err
		}
		if !hit {
			s.storeAnchors(out.PageInfo.Revision, anchors)
		}
		out.PositionIndex = anchors
	}
	if request.Seek != nil {
		start = total
		wanted := sorttext.Letter(request.Seek.Prefix)
		for _, anchor := range out.PositionIndex {
			if anchor.Key == wanted {
				start = anchor.Index
				break
			}
		}
	}
	group, groupArgs := "", []any(nil)
	if sorts[0].Field == "groupOrder" {
		if group, groupArgs, err = s.groupOrderSource(groupOrderTarget(request.Query), request.Rows, request.RowSources); err != nil {
			return out, nil, err
		}
	}
	shape := pageShape{reading: compiler.reading != nil, group: group, groupArgs: groupArgs, request: request, sorts: sorts, where: where, args: args, unrestricted: unrestricted, unrestrictedArgs: unrestrictedArgs, live: live, liveArgs: liveArgs, order: order, orderArgs: orderArgs,
		limit: request.Limit, total: total, summary: summary, seekKeys: seekKeys, seekAnchor: seekAnchor, revision: revision}
	if members := entityKindMembers(pivot); len(members) == 1 {
		parsed, parseErr := compactcatalog.ParseKind(members[0])
		if parseErr != nil {
			return out, nil, parseErr
		}
		shape.kind = int(parsed)
	}
	// A surviving anchor re-locates the viewport after a publication rather than
	// throwing the reader back to the top of the library: the anchor's rank in a
	// counted order is its block's running total plus a count inside the block.
	// Indexed fact orders count the index prefix. Profile orders retain the
	// requested position; a current revision
	// ignores the anchor entirely.
	if request.Range != nil && request.Range.AnchorID != "" && stale {
		primary, _ := shape.primary()
		if shape.positional() && !primary.Profile && (primary.Ordering >= 0 || primary.Ordering == -2) {
			var rank int
			var found bool
			var err error
			if primary.Ordering == -2 && primary.Blocks > 0 && shape.summary.classKey == "" {
				rank, found, err = s.browseAnchorRankIn(shape, request.Range.AnchorID, primary.Blocks)
			} else if primary.Ordering == -2 {
				rank, found, err = s.indexedFactAnchorRank(shape, request.Range.AnchorID)
			} else {
				rank, found, err = s.browseAnchorRank(shape, request.Range.AnchorID)
			}
			if err != nil {
				return out, nil, err
			}
			if found {
				start = rank
			}
		}
	}

	if start > 0 && start >= total && !lowerBound {
		start = max(0, total-1)
	}
	out.PageInfo.Start = start
	shape.start = start
	if request.Query != nil {
		shape.limit++ // One extra match answers hasMore even after the count reaches its cap.
	}
	selected, err := s.browsePage(shape)
	if err != nil {
		return out, nil, err
	}
	if request.Query != nil {
		out.PageInfo.HasMore = len(selected) > request.Limit
		if out.PageInfo.HasMore {
			selected = selected[:request.Limit]
		}
	} else {
		out.PageInfo.HasMore = start+len(selected) < total
	}
	if out.PageInfo.HasMore && request.Range == nil && len(selected) > 0 {
		last := selected[len(selected)-1]
		keys := make([]string, 0, len(sorts))
		for _, entry := range sorts {
			keys = append(keys, browseSortValue(last, entry.Field))
			if entry.Field == "personalRating" || entry.Field == "lastPlayed" || entry.Field == "playCount" {
				keys = append(keys, last.keys["phase"], last.keys["restTitle"])
			}
		}
		if compiler.reading != nil {
			keys = readingBrowseBoundedKeys(keys)
		}
		// The cursor carries the integer entity id; cursors are opaque and signed.
		out.PageInfo.NextCursor, err = s.encodeCursor(cursorValue{EvaluationAt: s.relativeBrowseTime(request.Query), Scope: scope, Value: strconv.Itoa(start + len(selected)), ID: strconv.FormatInt(last.entity, 10), Keys: keys, Fingerprint: revision, Total: total, TotalPresent: true, Expires: expires})
		if err != nil {
			return out, nil, err
		}
	}
	after, err := s.browseRevision(request, pivot, sorts)
	if err != nil {
		return out, nil, err
	}
	if after != revision {
		return out, nil, ErrStaleContinuation
	}
	return out, selected, nil
}
func seekPrefix(seek *BrowseSeek) string {
	if seek == nil {
		return ""
	}
	return sorttext.Letter(seek.Prefix)
}

func (s *Service) browsePositionIndex(where, order string, args, orderArgs []any) ([]BrowsePositionAnchor, error) {
	out := []BrowsePositionAnchor{}
	query := `SELECT letter,MIN(position) FROM (SELECT portico_letter(e.sort_key) AS letter,ROW_NUMBER() OVER (ORDER BY ` + order + `)-1 AS position FROM catalog_browse_rows e WHERE ` + where + `) GROUP BY letter ORDER BY MIN(position)`
	rows, err := s.read().Query(query, append(append([]any{}, orderArgs...), args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var anchor BrowsePositionAnchor
		if err = rows.Scan(&anchor.Key, &anchor.Index); err != nil {
			return nil, err
		}
		if err = s.reserveReadingCountAux(128 + int64(len(anchor.Key))*32); err != nil {
			return nil, err
		}
		out = append(out, anchor)
	}
	return out, rows.Err()
}

// browseLetterAndRangeIndex is browsePositionIndex's letter rail and the
// result's sparse range anchors from one ordered pass under a title sort: the
// rows that start a letter run or sit on an anchor position.
func (s *Service) browseLetterAndRangeIndex(where, order string, args, orderArgs []any) ([]BrowsePositionAnchor, []rangeAnchor, error) {
	query := `SELECT letter,position,order_key,entity_id FROM (SELECT portico_letter(e.sort_key) AS letter,e.sort_key COLLATE NOCASE AS order_key,e.entity_id AS entity_id,ROW_NUMBER() OVER w-1 AS position,lag(portico_letter(e.sort_key)) OVER w AS previous FROM catalog_browse_rows e WHERE ` + where + ` WINDOW w AS (ORDER BY ` + order + `)) WHERE position%` + strconv.Itoa(rangeAnchorEvery) + `=0 OR previous IS NULL OR previous<>letter ORDER BY position`
	rows, err := s.read().Query(query, append(append([]any{}, args...), orderArgs...)...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	letters, ranges := []BrowsePositionAnchor{}, []rangeAnchor{}
	seen := map[string]bool{}
	for rows.Next() {
		var letter string
		var anchor rangeAnchor
		if err = rows.Scan(&letter, &anchor.position, &anchor.key, &anchor.id); err != nil {
			return nil, nil, err
		}
		if !seen[letter] {
			seen[letter] = true
			letters = append(letters, BrowsePositionAnchor{Key: letter, Index: anchor.position})
		}
		if anchor.position%rangeAnchorEvery == 0 {
			ranges = append(ranges, anchor)
		}
	}
	return letters, ranges, rows.Err()
}

// browseProjection keeps the existing ContentEntry shape: item-backed rows carry
// the full media projection, container rows carry a count and a destination.
func (s *Service) browseProjection(profile string, rows []browseRow) ([]ContentEntry, error) {
	out := make([]ContentEntry, 0, len(rows))
	itemIDs := []string{}
	containerIDs := []int64{}
	for _, row := range rows {
		if isItemEntity(row.kind) {
			itemIDs = append(itemIDs, row.id)
		} else {
			containerIDs = append(containerIDs, row.entity)
		}
	}
	media := map[string]Item{}
	if len(itemIDs) > 0 {
		items, err := s.mediaPage(profile, itemIDs, false)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			media[item.ID] = item
		}
	}
	counts := map[int64]int{}
	if len(containerIDs) > 0 {
		args := make([]any, 0, len(containerIDs))
		for _, id := range containerIDs {
			args = append(args, id)
		}
		// A card's count is its maintained membership counter, not a pass
		// over an artist's songs or a show's episodes.
		rows, err := s.read().Query(`SELECT m.entity_id,m.members FROM catalog_browse_member_links m WHERE m.entity_id IN (`+placeholders(len(containerIDs))+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			var count int
			if err = rows.Scan(&id, &count); err != nil {
				rows.Close()
				return nil, err
			}
			counts[id] = count
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	targets := make([]artworkTarget, 0, len(rows))
	for _, row := range rows {
		if !isItemEntity(row.kind) {
			targets = append(targets, artworkTarget{row.kind, row.id})
		}
	}
	artwork, err := s.resolveArtwork(targets)
	if err != nil {
		return nil, err
	}
	columns, err := s.browseListColumns(rows)
	if err != nil {
		return nil, err
	}
	artists, err := s.browseAlbumArtists(rows)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if isItemEntity(row.kind) {
			item, ok := media[row.id]
			if !ok {
				continue
			}
			entry := contentItem(item)
			entry.SortLetter = sorttext.Letter(row.sortKey)
			if item.Year > 0 {
				year := item.Year
				entry.Year = &year
			}
			if row.rating > 0 {
				rating := row.rating
				entry.Rating = &rating
			}
			extra := columns[row.entity]
			entry.Resolution = extra.resolution
			if row.kind == "episode" {
				entry.AirDate = extra.released
			}
			out = append(out, entry)
			continue
		}
		count := counts[row.entity]
		art := artwork[artworkTarget{row.kind, row.id}]
		entry := ContentEntry{SortLetter: sorttext.Letter(row.sortKey), PosterURL: art.PosterURL, BackdropURL: art.BackdropURL, ID: row.id, Kind: row.kind, Title: row.title, Count: &count, AddedAt: nullableString(row.added), Navigation: &ContentNavigation{View: containerView(row.kind), EntityID: row.id}}
		if row.year > 0 {
			entry.Subtitle = strconv.Itoa(row.year)
			year := row.year
			entry.Year = &year
		}
		if row.rating > 0 {
			rating := row.rating
			entry.Rating = &rating
		}
		if artist, ok := artists[row.entity]; ok {
			entry.Artist = &artist
		}
		out = append(out, entry)
	}
	if err = s.nameEntryMakers(out); err != nil {
		return nil, err
	}
	if err = s.ProjectCoverSamples(profile, out); err != nil {
		return nil, err
	}
	if err = s.countEntryPlays(profile, out); err != nil {
		return nil, err
	}
	return out, nil
}

// nameLatestEpisodes says, for each show on a Recently aired page, which
// episode aired last and how many aired with it. It reads the episodes of the
// page's shows only: the cost follows the page, not the library.
func (s *Service) nameLatestEpisodes(rows []browseRow, entries []ContentEntry) error {
	index := map[int64]int{}
	ids := []int64{}
	for i, row := range rows {
		if row.kind == "show" && i < len(entries) {
			index[row.entity] = i
			ids = append(ids, row.entity)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	raw, _ := json.Marshal(ids)
	found, err := s.read().Query(`SELECT ep.show_id,d.release_date,se.number,ep.number,ee.title
	 FROM json_each(?) j CROSS JOIN catalog_episodes ep ON ep.show_id=j.value
	 JOIN catalog_item_details d ON d.entity_id=ep.entity_id AND COALESCE(d.release_date,'')<>''
	 JOIN catalog_entities ee ON ee.id=ep.entity_id LEFT JOIN catalog_seasons se ON se.entity_id=ep.season_id`, string(raw))
	if err != nil {
		return err
	}
	defer found.Close()
	latest := map[int64]*LatestEpisode{}
	for found.Next() {
		var show int64
		var date, title string
		var season sql.NullInt64
		var number int
		if err = found.Scan(&show, &date, &season, &number, &title); err != nil {
			return err
		}
		current := latest[show]
		if current != nil && date < current.AirDate {
			continue
		}
		if current == nil || date > current.AirDate {
			current = &LatestEpisode{AirDate: date}
			latest[show] = current
		}
		current.Count++
		// Of the episodes that aired that day, the card names the last in order.
		seasonNumber := -1
		if season.Valid {
			seasonNumber = int(season.Int64)
		}
		named := -1
		if current.SeasonNumber != nil {
			named = *current.SeasonNumber
		}
		if current.Count == 1 || seasonNumber > named || seasonNumber == named && number > current.EpisodeNumber {
			current.EpisodeNumber, current.Title = number, title
			current.SeasonNumber = nil
			if season.Valid {
				n := seasonNumber
				current.SeasonNumber = &n
			}
		}
	}
	if err = found.Err(); err != nil {
		return err
	}
	for show, episode := range latest {
		entries[index[show]].LatestEpisode = episode
	}
	return nil
}

// browseAlbumArtists names the artist of each album row on the page, so an
// album card always says whose it is.
func (s *Service) browseAlbumArtists(rows []browseRow) (map[int64]ContentEntityRef, error) {
	out := map[int64]ContentEntityRef{}
	args := []any{}
	for _, row := range rows {
		if row.kind == "album" {
			args = append(args, row.entity)
		}
	}
	if len(args) == 0 {
		return out, nil
	}
	found, err := s.read().Query(`SELECT a.entity_id,pid(ar.public_id),ar.title FROM catalog_albums a JOIN catalog_entities ar ON ar.id=a.artist_id WHERE a.entity_id IN (`+placeholders(len(args))+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer found.Close()
	for found.Next() {
		var album int64
		var ref ContentEntityRef
		if err = found.Scan(&album, &ref.ID, &ref.Name); err != nil {
			return nil, err
		}
		if ref.Name != "" {
			out[album] = ref
		}
	}
	return out, found.Err()
}

// browseListColumn is what a list view shows beside an item that the browse
// row does not carry: its best file's picture class and its release date.
type browseListColumn struct{ resolution, released string }

// browseListColumns reads those for the page's item rows: two lookups by the
// page's ids, whatever the library's size.
func (s *Service) browseListColumns(rows []browseRow) (map[int64]browseListColumn, error) {
	out := map[int64]browseListColumn{}
	args := []any{}
	for _, row := range rows {
		if isItemEntity(row.kind) {
			args = append(args, row.entity)
		}
	}
	if len(args) == 0 {
		return out, nil
	}
	in := placeholders(len(args))
	heights, err := s.read().Query(`SELECT ia.entity_id,max(ast.height) FROM catalog_asset_links ia JOIN catalog_assets ast ON ast.id=ia.asset_id WHERE ia.entity_id IN (`+in+`) GROUP BY ia.entity_id`, args...)
	if err != nil {
		return nil, err
	}
	for heights.Next() {
		var entity int64
		var height sql.NullInt64
		if err = heights.Scan(&entity, &height); err != nil {
			heights.Close()
			return nil, err
		}
		if height.Valid && height.Int64 > 0 {
			column := out[entity]
			switch {
			case height.Int64 >= 2000:
				column.resolution = "4k"
			case height.Int64 >= 1000:
				column.resolution = "1080p"
			case height.Int64 >= 700:
				column.resolution = "720p"
			default:
				column.resolution = "sd"
			}
			out[entity] = column
		}
	}
	err = heights.Err()
	heights.Close()
	if err != nil {
		return nil, err
	}
	dates, err := s.read().Query(`SELECT entity_id,release_date FROM catalog_item_details WHERE entity_id IN (`+in+`) AND release_date<>''`, args...)
	if err != nil {
		return nil, err
	}
	defer dates.Close()
	for dates.Next() {
		var entity int64
		var released string
		if err = dates.Scan(&entity, &released); err != nil {
			return nil, err
		}
		column := out[entity]
		column.released = released
		out[entity] = column
	}
	return out, dates.Err()
}

// nullableString is a row's text, or nil when the row has none. The browse
// rows are selected with an empty string standing in for "no date", and a row
// with no added date (an author, a grouping) says null on the wire, never an
// empty string a client would try to read as a date.
func nullableString(value sql.NullString) *string {
	if !value.Valid || value.String == "" {
		return nil
	}
	copied := value.String
	return &copied
}

func isItemEntity(kind string) bool {
	switch kind {
	case "show", "season", "artist", "album", "book", "author", "collection":
		return false
	}
	return true
}

func containerView(kind string) string {
	switch kind {
	case "author":
		return "author"
	default:
		return kind
	}
}

func browseHasPositionAnchors(field string) bool {
	definition, ok := browseSortByID(field)
	return ok && (definition.Ordering >= 0 || field == "personalRating" || field == "lastPlayed" || field == "forYou")
}

// countRows runs one count(*) query in the read snapshot.
func (s *Service) countRows(query string, args ...any) (int, error) {
	var n int
	err := s.read().QueryRow(query, args...).Scan(&n)
	return n, err
}

// entityIDs runs one query of integer ids in the read snapshot.
func (s *Service) entityIDs(query string, args ...any) ([]int64, error) {
	rows, err := s.read().Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// playablePivot reports whether every kind of the pivot is a playable item.
func playablePivot(pivot browsePivot) bool {
	members := entityKindMembers(pivot)
	if len(members) == 0 {
		return false
	}
	for _, kind := range members {
		switch kind {
		case "movie", "episode", "song", "audiobook_file", "extra": // catalog_kinds.playable
		default:
			return false
		}
	}
	return true
}

// Release keeps prepared conversion delivery charged through its HTTP write.
// It is an idempotent no-op for ordinary Browse results.
func (r BrowseResult) Release() {
	if r.selectionRelease != nil {
		r.selectionRelease()
	}
}
