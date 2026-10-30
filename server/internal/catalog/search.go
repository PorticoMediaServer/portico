package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/facettext"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/readingcap"
	"unicode/utf8"
)

// SearchGroupBudget bounds the work one group may spend. A group that overruns
// reports its own failure; the response as a whole still succeeds, because a
// slow or missing projection must never take the other groups down with it.
const SearchGroupBudget = 600 * time.Millisecond

var ErrSearchQuery = requestinput.New("search requires 1–128 characters and 1–8 title tokens; limit is 1–40")

type searchGroupDefinition struct {
	id, kind, label, view, source string
	sorts                         []string
}

var searchSorts = []string{"relevance", "title", "releaseYear", "dateAdded"}
var catalogSorts = []string{"relevance", "title", "releaseYear", "dateAdded"}
var nameSorts = []string{"relevance", "title"}
var searchGroups = []searchGroupDefinition{
	{"movies", "movie", "Movies", "item", "catalog", catalogSorts},
	{"shows", "show", "Shows", "show", "catalog", catalogSorts},
	{"episodes", "episode", "Episodes", "item", "catalog", catalogSorts},
	{"artists", "artist", "Artists", "artist", "catalog", nameSorts},
	{"albums", "album", "Albums", "album", "catalog", catalogSorts},
	{"songs", "song", "Songs", "item", "catalog", catalogSorts},
	{"books", "book", "Books", "book", "catalog", catalogSorts},
	{"reading", "reading", "Books", "item", "reading", catalogSorts},
	{"collections", "collection", "Collections", "collection", "catalog", nameSorts},
	{"authors", "author", "Authors", "author", "catalog", nameSorts},
	{"people", "person", "People", "person", "people", nameSorts},
	{"live-tv", "channel", "Live TV", "channel", "live", nameSorts},
}

type SearchGroupCapability struct {
	ID         string   `json:"id"`
	LabelKey   string   `json:"labelKey"`
	Title      string   `json:"title"`
	EntityKind string   `json:"entityKind"`
	Available  bool     `json:"available"`
	Reason     string   `json:"reason,omitempty"`
	Sorts      []string `json:"sorts"`
}
type SearchCapabilities struct {
	Groups        []SearchGroupCapability `json:"groups"`
	Sorts         []string                `json:"sorts"`
	Directions    []string                `json:"directions"`
	MaxLimit      int                     `json:"maxLimit"`
	GroupBudgetMs int                     `json:"groupBudgetMs"`
}
type SearchGroupResult struct {
	ID              string         `json:"id"`
	LabelKey        string         `json:"labelKey"`
	Title           string         `json:"title"`
	EntityKind      string         `json:"entityKind"`
	Status          string         `json:"status"`
	ErrorCode       string         `json:"errorCode"`
	Items           []ContentEntry `json:"items"`
	TotalCount      int            `json:"totalCount"`
	TotalCountExact bool           `json:"totalCountExact"`
	HasMore         bool           `json:"hasMore"`
	NextCursor      string         `json:"nextCursor"`
}
type SearchQueryEcho struct {
	Q          string   `json:"q"`
	Sort       string   `json:"sort"`
	Direction  string   `json:"direction"`
	Group      string   `json:"group"`
	Groups     []string `json:"groups"`
	LibraryIDs []string `json:"libraryIds"`
	Limit      int      `json:"limit"`
	SearchMode string   `json:"searchMode"`
	Recorded   bool     `json:"recorded"`
}
type SearchEnvelope struct {
	Scope        ContentScope        `json:"scope"`
	Revision     ContentRevision     `json:"revision"`
	Heading      ContentHeading      `json:"heading"`
	Query        SearchQueryEcho     `json:"query"`
	Groups       []SearchGroupResult `json:"groups"`
	Capabilities SearchCapabilities  `json:"capabilities"`
	Empty        *ContentHeading     `json:"empty,omitempty"`
}
type SearchRequest struct {
	Viewer                                                            Viewer
	ServerID, Profile, ViewerFence, Q, Group, Cursor, Sort, Direction string
	Limit                                                             int
	AllLibraries                                                      bool
	Libraries                                                         []string
	LibraryIDs                                                        []string
	Groups                                                            []string
	// LiveTV is a server-resolved viewer permission, never client input or
	// service presence alone. It gates all live search, including future programme results.
	LiveTV bool
	Record bool
	// Restrictions is applied to the catalog groups; people and live channels
	// carry no content rating and are governed by their own policies.
	Restrictions identity.ContentRestrictions
}

// searchGroupProbe is a test seam for per-group budget isolation. Production
// never sets it; a test injects a slow or failing group through it.
var searchGroupProbe func(context.Context, string) error

func NormalizeSearch(q string) (string, string, error) {
	q = strings.Join(strings.Fields(q), " ")
	if !utf8.ValidString(q) || utf8.RuneCountInString(q) < 1 || utf8.RuneCountInString(q) > 128 {
		return "", "", ErrSearchQuery
	}
	if len(strings.Fields(facettext.SearchFold(q))) == 0 || len(strings.Fields(facettext.SearchFold(q))) > 8 {
		return "", "", ErrSearchQuery
	}
	terms := []string{}
	for _, token := range facettext.SearchTerms(q) {
		terms = append(terms, `"`+token+`"*`)
	}

	return q, strings.Join(terms, " AND "), nil
}
func searchRevision(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, r SearchRequest, ids string) (ContentRevision, string, error) {
	var rev ContentRevision
	var rows string
	e := q.QueryRowContext(ctx, `SELECT COALESCE(sum(c),0),COALESCE(sum(v),0),COALESCE(json_group_array(json_array(id,c,v)),'[]') FROM (SELECT l.id,r.revision c,COALESCE(v.revision,0) v FROM libraries l JOIN library_revisions r ON r.library_id=l.id LEFT JOIN viewer_revisions v ON v.library_id=l.id AND v.profile_id=? WHERE (? OR l.id IN(SELECT value FROM json_each(?))) ORDER BY l.id)`, r.Profile, r.AllLibraries, ids).Scan(&rev.Catalog, &rev.Viewer, &rows)
	return rev, fmt.Sprintf("%x", sha256.Sum256([]byte(rows))), e
}

// searchOrder names the ordering value for one group and sort. Relevance is
// FTS5 bm25 with an exact-title and a title-prefix boost; every other sort is a
// stored column so ordering never depends on client-side comparison.
type searchOrder struct {
	expr    string
	args    []any
	numeric bool
}

// searchScore is FTS5 bm25 lifted to "larger is better", with an exact-title and
// a title-prefix boost. The result is rounded so the value a cursor carries and
// the comparison that resumes from it are the same double.
func searchScore(title string) searchOrder {
	return searchOrder{`round(m.relevance+(CASE WHEN d.title_fold=? THEN 1000.0 ELSE 0.0 END)+(CASE WHEN d.title_fold LIKE ? ESCAPE '\' THEN 100.0 ELSE 0.0 END),6)`, []any{facettext.SearchFold(title), searchPrefix(facettext.SearchFold(title))}, true}
}
func catalogOrder(sortID, kind, q string) searchOrder {
	switch sortID {
	case "title":
		return searchOrder{`e.sort_key`, nil, false}
	// The browse row's year and added date, read by key: joined instead, the
	// serving view (two generations) is read whole.
	case "releaseYear":
		return searchOrder{`CAST(COALESCE((SELECT wb.year FROM catalog_browse_rows wb WHERE wb.entity_id=e.id),0) AS REAL)`, nil, true}
	case "dateAdded":
		return searchOrder{`COALESCE((SELECT wb.added_text FROM catalog_browse_rows wb WHERE wb.entity_id=e.id),'')`, nil, false}
	}
	return searchScore(q)
}
func searchDefaultDirection(sortID string) string {
	if sortID == "title" {
		return "asc"
	}
	return "desc"
}
func validSearchSort(sortID string) bool {
	for _, v := range searchSorts {
		if v == sortID {
			return true
		}
	}
	return false
}
func groupSupportsSort(g searchGroupDefinition, sortID string) bool {
	for _, v := range g.sorts {
		if v == sortID {
			return true
		}
	}
	return false
}

// resolveSearchSort keeps one response coherent: a group that cannot honour the
// requested sort falls back to the sort it does publish rather than failing.
func resolveSearchSort(g searchGroupDefinition, sortID string) string {
	if groupSupportsSort(g, sortID) {
		return sortID
	}
	if sortID == "releaseYear" || sortID == "dateAdded" {
		return "title"
	}
	return g.sorts[0]
}

// SearchPreviewLimit is how many results each group shows when every group is searched at once.
const SearchPreviewLimit = 12

func (s *Service) Search(ctx context.Context, r SearchRequest) (SearchEnvelope, error) {
	if dbwork.Snapshot(ctx) == nil {
		if _, err := s.cursorKey(); err != nil {
			return SearchEnvelope{}, err
		}
		var out SearchEnvelope
		err := dbwork.WithReadSnapshot(ctx, s.db, func(snapshot context.Context) error {
			var err error
			out, err = s.WithContext(snapshot).Search(snapshot, r)
			return err
		})
		return out, err
	}

	if r.Profile != "" && r.Profile != r.Viewer.Profile || r.ViewerFence != "" && r.ViewerFence != r.Viewer.Fence || len(r.Libraries) > 0 && strings.Join(r.Libraries, "\x00") != strings.Join(r.Viewer.Libraries, "\x00") {
		if r.Cursor != "" {
			return SearchEnvelope{}, ErrCursor
		}
		return SearchEnvelope{}, ErrSearchQuery
	}
	if err := s.prepareViewer(r.Viewer); err != nil {
		return SearchEnvelope{}, err
	}
	r.Profile, r.ViewerFence, r.Libraries, r.Restrictions = r.Viewer.Profile, r.Viewer.Fence, r.Viewer.Libraries, r.Viewer.EffectiveRestrictions()
	// An "all libraries" request means all libraries inside this viewer scope;
	// the query must never bypass that list to walk the server's whole catalogue.
	r.AllLibraries = false
	out := SearchEnvelope{Heading: ContentHeading{Key: "search.title", Fallback: "Search"}, Scope: ContentScope{r.ServerID, "", "mixed", "search", "", r.ViewerFence}, Groups: []SearchGroupResult{}}
	q, match, e := NormalizeSearch(r.Q)
	if e != nil {
		return out, e
	}
	r.Q = q
	if r.Limit == 0 {
		r.Limit = 40
	}
	if r.Sort == "" {
		r.Sort = "relevance"
	}
	if r.Direction == "" {
		r.Direction = searchDefaultDirection(r.Sort)
	}
	if r.Limit < 1 || r.Limit > 40 || len(r.Cursor) > 4096 || !validSearchSort(r.Sort) || (r.Direction != "asc" && r.Direction != "desc") {
		return out, ErrSearchQuery
	}
	selected, e := searchSelection(r)
	if e != nil {
		return out, e
	}
	if !r.LiveTV {
		if r.Group == "live-tv" {
			if r.Cursor != "" {
				return out, ErrCursor
			}
			return out, ErrSearchQuery
		}
		visible := make([]searchGroupDefinition, 0, len(selected))
		for _, g := range selected {
			if g.source != "live" {
				visible = append(visible, g)
			}
		}
		selected = visible
	}
	if r.Cursor != "" && r.Group == "" {
		return out, ErrSearchQuery
	}
	if _, e = s.cursorKey(); e != nil {
		return out, e
	}
	libraries := append([]string{}, r.Libraries...)
	if len(r.LibraryIDs) > 0 {
		// A library restriction narrows the authorized set; it never widens it.
		allowed := map[string]bool{}
		for _, id := range libraries {
			allowed[id] = true
		}
		libraries = []string{}
		for _, id := range r.LibraryIDs {
			if !allowed[id] && !r.AllLibraries {
				return out, ErrSearchQuery
			}
			libraries = append(libraries, id)
		}
		r.AllLibraries = false
	}
	sort.Strings(libraries)
	r.Libraries = libraries
	raw, _ := json.Marshal(libraries)
	scopeHash := fmt.Sprintf("%x", sha256.Sum256(append([]byte(fmt.Sprint(r.AllLibraries)), raw...)))
	// Read through the request's snapshot when there is one, so the revision is
	// the one the groups below are read at and the check after them cannot see a
	// later publication.
	rev, fingerprint, e := searchRevision(ctx, s.read(), r, string(raw))
	if e != nil {
		return out, e
	}
	out.Revision = rev
	out.Query = SearchQueryEcho{Q: r.Q, Sort: r.Sort, Direction: r.Direction, Group: r.Group, Groups: groupIDs(selected), LibraryIDs: libraries, Limit: r.Limit, SearchMode: "normalized_prefix", Recorded: r.Record}
	out.Capabilities = s.searchCapabilities(ctx, r)
	pageSize := r.Limit
	// Every group at once: a row of each, enough to fill a wide screen's width.
	if r.Group == "" && pageSize > SearchPreviewLimit {
		pageSize = SearchPreviewLimit
	}
	// The groups are composed in order on this request's one read snapshot.
	//
	// They used to run one goroutine each, which meant one pooled connection
	// each: nine groups against a pool of eight, times up to a hundred concurrent
	// searches, inside an admission lane that believed it was admitting eight
	// requests. The lane accounting was wrong by nine, and the pool queue — which
	// admission cannot see — absorbed the difference. Measured on the release
	// tier, one search took twenty-eight pooled connections against a ceiling of
	// two.
	//
	// Sequential on one snapshot costs each group the ones before it in latency
	// and saves the whole server the contention. It is also more correct: every
	// group now reports on the same WAL frame, so two groups cannot disagree
	// about whether a title exists. The per-group budget is unchanged, and so is
	// the `search_group_timeout` a group reports when it runs out of it.
	results := make([]SearchGroupResult, len(selected))
	failures := make([]error, len(selected))
	for index, g := range selected {
		// Written before the work, not after it: a group that panics mid-query must
		// still leave a whole, honest group in the response rather than a zero value
		// the client cannot read.
		results[index] = SearchGroupResult{ID: g.id, LabelKey: searchGroupLabelKey(g.id), Title: g.label, EntityKind: g.kind, Status: "error", ErrorCode: "search_group_unavailable", Items: []ContentEntry{}}
		func() {
			// In sequence, but each group still contained: one bad row costs its own
			// group, is counted and attributed, and nobody else's request notices.
			defer supervise.Recover("catalog.search.group." + g.id)
			// PORTICO_CHAOS_PANIC=catalog.search.group:0.01 makes one search group in a
			// hundred fail the way a bad row would; the soak tier asserts the process
			// survives it.
			supervise.Chaos("catalog.search.group")
			result := SearchGroupResult{ID: g.id, LabelKey: searchGroupLabelKey(g.id), Title: g.label, EntityKind: g.kind, Status: "success", Items: []ContentEntry{}}
			budget, cancel := context.WithTimeout(ctx, SearchGroupBudget)
			defer cancel()
			if groupErr := s.searchGroup(budget, r, g, match, scopeHash, fingerprint, rev, pageSize, &result); groupErr != nil {
				failures[index] = groupErr
				result = SearchGroupResult{ID: g.id, LabelKey: searchGroupLabelKey(g.id), Title: g.label, EntityKind: g.kind, Status: "error", ErrorCode: "search_group_unavailable", Items: []ContentEntry{}}
				if errors.Is(groupErr, context.DeadlineExceeded) && ctx.Err() == nil {
					result.ErrorCode = "search_group_timeout"
				}
			}
			// Live channels count exactly; people say for themselves.
			if groupErr := failures[index]; groupErr == nil && g.source == "live" {
				result.TotalCountExact = true
			}
			results[index] = result
		}()
		if ctx.Err() != nil {
			break
		}
	}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	// A rejected continuation is a request error the caller must see; it is not
	// one group's bad day and must not be reported as partial success.
	for _, groupErr := range failures {
		if errors.Is(groupErr, ErrCursor) || errors.Is(groupErr, ErrStaleContinuation) {
			return out, groupErr
		}
	}
	out.Groups = results
	total := 0
	for _, group := range out.Groups {
		total += len(group.Items)
	}
	if total == 0 {
		out.Empty = &ContentHeading{Key: "search.empty", Fallback: "No matching titles"}
	}
	after, stamp, e := searchRevision(ctx, s.read(), r, string(raw))
	if e != nil {
		return out, e
	}
	if after != rev || stamp != fingerprint {
		return out, ErrStaleContinuation
	}
	// History is recorded by the HTTP layer, which knows the viewer and the
	// search.rememberHistory preference; the echo below reflects the outcome.
	return out, nil
}

func groupIDs(groups []searchGroupDefinition) []string {
	out := []string{}
	for _, g := range groups {
		out = append(out, g.id)
	}
	return out
}

// searchSelection resolves group and groups into the ordered group list this
// response will report. An unknown group is a request error, never a silent drop.
func searchSelection(r SearchRequest) ([]searchGroupDefinition, error) {
	known := map[string]searchGroupDefinition{}
	for _, g := range searchGroups {
		known[g.id] = g
	}
	if r.Group != "" {
		g, ok := known[r.Group]
		if !ok {
			return nil, ErrSearchQuery
		}
		if len(r.Groups) > 0 {
			return nil, ErrSearchQuery
		}
		return []searchGroupDefinition{g}, nil
	}
	if len(r.Groups) == 0 {
		return append([]searchGroupDefinition{}, searchGroups...), nil
	}
	if len(r.Groups) > len(searchGroups) {
		return nil, ErrSearchQuery
	}
	wanted := map[string]bool{}
	for _, id := range r.Groups {
		if _, ok := known[id]; !ok || wanted[id] {
			return nil, ErrSearchQuery
		}
		wanted[id] = true
	}
	out := []searchGroupDefinition{}
	for _, g := range searchGroups {
		if wanted[g.id] {
			out = append(out, g)
		}
	}
	return out, nil
}

func (s *Service) searchCapabilities(ctx context.Context, r SearchRequest) SearchCapabilities {
	out := SearchCapabilities{Groups: []SearchGroupCapability{}, Sorts: searchSorts, Directions: []string{"asc", "desc"}, MaxLimit: 40, GroupBudgetMs: int(SearchGroupBudget / time.Millisecond)}
	live := r.LiveTV && s.tableExists(ctx, "live_channel_versions")
	people := s.tableExists(ctx, "catalog_people_names")
	for _, g := range searchGroups {
		if g.source == "live" && !r.LiveTV {
			continue
		}
		capability := SearchGroupCapability{ID: g.id, LabelKey: searchGroupLabelKey(g.id), Title: g.label, EntityKind: g.kind, Available: true, Sorts: g.sorts}
		switch g.source {
		case "reading":
			if !s.tableExists(ctx, "reading_publications") {
				capability.Available = false
				capability.Reason = "Reading publications are not installed on this server."
			}
		case "live":
			if !live {
				capability.Available = false
				capability.Reason = "Live TV is not configured on this server."
			}
		case "people":
			if !people {
				capability.Available = false
				capability.Reason = "The people index is not installed on this server."
			}
		}
		out.Groups = append(out.Groups, capability)
	}
	return out
}

// tableExists asks `sqlite_master` whether an optional feature's schema is
// installed. That is a fact about the database file, settled before the listener
// serves anything, so it is resolved once per name and remembered. It was being
// asked twice per search, over a `sqlite_master` with thousands of rows.
func (s *Service) tableExists(ctx context.Context, name string) bool {
	s.state.schemaMu.Lock()
	if known, ok := s.state.schemaTables[name]; ok {
		s.state.schemaMu.Unlock()
		return known
	}
	s.state.schemaMu.Unlock()
	var n int
	if e := s.read().QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE name=? AND type IN('table','view')`, name).Scan(&n); e != nil {
		return false
	}
	s.state.schemaMu.Lock()
	if s.state.schemaTables == nil {
		s.state.schemaTables = map[string]bool{}
	}
	s.state.schemaTables[name] = n > 0
	s.state.schemaMu.Unlock()
	return n > 0
}

func (s *Service) searchGroup(ctx context.Context, r SearchRequest, g searchGroupDefinition, match, scopeHash, fingerprint string, rev ContentRevision, pageSize int, out *SearchGroupResult) error {
	if searchGroupProbe != nil {
		if e := searchGroupProbe(ctx, g.id); e != nil {
			return e
		}
	}
	switch g.source {
	case "people":
		return s.searchPeopleGroup(ctx, r, g, match, scopeHash, fingerprint, rev, pageSize, out)
	case "live":
		return s.searchLiveGroup(ctx, r, g, scopeHash, fingerprint, rev, pageSize, out)
	}
	return s.searchCatalogGroup(ctx, r, g, match, scopeHash, fingerprint, rev, pageSize, out)
}

func (s *Service) groupCursorScope(r SearchRequest, g searchGroupDefinition, scopeHash, sortID string) cursorScope {
	return cursorScope{Library: scopeHash, Profile: r.Profile, Viewer: r.ViewerFence, View: "search", Section: g.id, Search: r.Q, Limit: r.Limit, Sort: sortID, Direction: r.Direction}
}

func (s *Service) compactSearchReady() error {
	return s.compactProjectionReady(17)
}

func (s *Service) searchCatalogGroup(ctx context.Context, r SearchRequest, g searchGroupDefinition, match, scopeHash, fingerprint string, rev ContentRevision, pageSize int, out *SearchGroupResult) error {
	if err := s.compactSearchReady(); err != nil {
		return err
	}
	readingGroup := g.source == "reading"
	kind, err := compactcatalog.ParseKind(g.kind)
	if readingGroup {
		kind, err = compactcatalog.Ebook, nil
	}
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(r.Libraries)
	kindClause := `e.kind=?`
	filterArgs := []any{kind, r.AllLibraries, string(raw)}
	if readingGroup {
		kindClause = `e.kind IN(20,21)`
		filterArgs = []any{r.AllLibraries, string(raw)}
	}
	where := ` AND ` + kindClause + ` AND (? OR l.library_id IN(SELECT value FROM json_each(?))) AND l.retired=0 AND NOT EXISTS(SELECT 1 FROM dvr_catalog_retirements gone WHERE gone.item_id=e.id)`
	if readingGroup {
		where += ` AND e.retired=0 AND EXISTS(SELECT 1 FROM reading_publications reading_publication
 JOIN catalog_asset_links reading_link ON reading_link.entity_id=reading_publication.item_id AND reading_link.asset_id=reading_publication.asset_id
 JOIN libraries reading_library ON reading_library.id=l.library_id
 WHERE reading_publication.item_id=e.id AND ((e.kind=20 AND reading_library.kind='book' AND reading_publication.format IN('epub','pdf')) OR (e.kind=21 AND reading_library.kind='comic' AND reading_publication.format IN(` + readingcap.ComicFormatsSQL() + `))) AND ` + readingcap.LocalComicArchiveSQL("reading_publication") + `)`
	}

	for _, word := range strings.Fields(facettext.SearchFold(r.Q)) {
		if strings.ContainsFunc(word, facettext.CJK) {
			where += ` AND instr(d.normalized,?)>0`
			filterArgs = append(filterArgs, word)
		}
	}
	// Placed media needs a live container. A source-backed episode with no
	// placement is independently playable until its show/number is identified.
	switch g.kind {
	case "episode":
		where += ` AND ((e.parent_id IS NULL AND NOT EXISTS(SELECT 1 FROM catalog_episodes loose WHERE loose.entity_id=e.id)) OR EXISTS(SELECT 1 FROM catalog_episodes ep JOIN catalog_entities sh ON sh.id=ep.show_id WHERE ep.entity_id=e.id AND sh.library_id=e.library_id AND sh.retired=0))`
	case "song":
		where += ` AND EXISTS(SELECT 1 FROM catalog_songs s JOIN catalog_entities a ON a.id=s.album_id WHERE s.entity_id=e.id AND a.library_id=e.library_id AND a.retired=0)`
	}
	if restriction, restrictionArgs := EntityRestrictionSQL("e.id", r.Restrictions); restriction != "1" {
		where += " AND " + restriction
		filterArgs = append(filterArgs, restrictionArgs...)
	}
	// The count is asked for only when the page cannot answer it. A group's total
	// is exact and it is in the response, and it was a second pass over the whole
	// FTS match set with the viewer's restriction evaluated per match — measured
	// on the release tier at 13.4 ms a group, 3,969 executions, 53 seconds of the
	// run. A first page that comes back short has counted the group by reading
	// it, and most groups of a real search are short.
	//
	// A group ranks every match of its kind in the viewer's libraries — the
	// kind and library tokens select them inside the index — so relevance,
	// every sort and the count are exact at any depth. A very broad prefix
	// costs its matches, within the group's time budget.
	libraryScope := ""
	if !r.AllLibraries {
		// Candidates are chosen by rank inside the index, so the viewer's
		// libraries are part of the match: ranked server-wide, another
		// library's titles could fill every candidate slot.
		ids, err := s.searchLibraryTokens(ctx, r.Libraries)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			ids = []string{"lnone"} // no library: a token no document carries
		}
		libraryScope = ` AND kind : (` + strings.Join(ids, " OR ") + `)`
	}
	kindMatch := `kind : k` + strconv.Itoa(int(kind))
	if readingGroup {
		kindMatch = `kind : (k20 OR k21)`
	}
	match = kindMatch + libraryScope + ` AND search : (` + match + `)`
	sortID := resolveSearchSort(g, r.Sort)
	// The full-text rank is computed for every match it is selected for; only
	// relevance orders by it.
	matches := `(SELECT rowid AS entity_id FROM catalog_search_titles WHERE catalog_search_titles MATCH ?) m`
	joins := ` CROSS JOIN catalog_search_documents d ON d.entity_id=m.entity_id CROSS JOIN catalog_entities e ON e.id=d.entity_id JOIN catalog_libraries l ON l.id=e.library_id`
	candidates := matches + joins
	if sortID == "relevance" {
		candidates = `(SELECT rowid AS entity_id,-rank AS relevance FROM catalog_search_titles WHERE catalog_search_titles MATCH ?) m` + joins
	}
	countTerms, countIndexed := facettext.IndexedSearchTerms(r.Q)
	countGroup := func() error {
		// No single-kind prefix counter describes this union; keep its bounded count honest.
		if readingGroup {
			out.TotalCount, out.TotalCountExact = len(out.Items), false
			return nil
		}
		// One indexed word has a maintained count. Several words would count
		// their whole intersection, so their total is the page's and is marked
		// as not exact, as relevance search marks it.
		if countIndexed && len(countTerms) == 1 {
			total, exact, err := s.prefixSearchTotal(ctx, r, int(kind), countTerms[0])
			if err != nil || exact {
				out.TotalCount, out.TotalCountExact = total, exact
				return err
			}
		} else if countIndexed {
			out.TotalCount, out.TotalCountExact = len(out.Items), false
			return nil
		}
		countArgs := append([]any{match}, filterArgs...)
		err := s.read().QueryRowContext(ctx, `SELECT count(*) FROM `+matches+joins+` WHERE 1`+where, countArgs...).Scan(&out.TotalCount)
		out.TotalCountExact = err == nil
		return err
	}
	contextJoin, subtitle := searchContext(g.kind)
	order := catalogOrder(sortID, g.kind, r.Q)
	// Only the columns needed to rank matches belong before the page boundary.
	// Release year and date added are the browse row's, as their ordered
	// indexes (searchWalks) hold them.
	orderJoin := ""
	args := append([]any{}, order.args...)
	args = append(args, match)
	args = append(args, filterArgs...)
	ranked := `SELECT d.entity_id entity_id,d.entity_pid entity_pid,` + order.expr + ` ord FROM ` + candidates + orderJoin + ` WHERE 1` + where
	prefixTerms, prefix := facettext.IndexedSearchTerms(r.Q)
	prefix = prefix && sortID == "relevance" && !readingGroup
	var prefixCursor *cursorValue
	if r.Cursor != "" {
		if c, err := s.decodeRevisionCursor(r.Cursor, s.groupCursorScope(r, g, scopeHash, "relevance_prefix"), rev); err == nil {
			prefixCursor = &c
		} else {
			prefix = false
		}
	}
	prefixLead := ""
	tie := "entity_pid"
	if prefix {
		var prefixArgs []any
		var prefixRanked string
		prefixRanked, prefixArgs, prefixLead, err = s.prefixSearchCandidates(ctx, r, int(kind), prefixTerms, where, filterArgs, pageSize+1, s.groupCursorScope(r, g, scopeHash, "relevance_prefix"), rev)
		if errors.Is(err, errPrefixFallback) {
			prefix = false
		} else if err != nil {
			return err
		}
		if prefix {
			knownTotal, knownExact := 0, false
			if len(prefixTerms) == 1 {
				knownTotal, knownExact, err = s.prefixSearchTotal(ctx, r, int(kind), prefixTerms[0])
				if err != nil {
					return err
				}
				if knownExact && knownTotal == 0 {
					out.TotalCount, out.TotalCountExact = 0, true
					return nil
				}
			}
			ranked = `SELECT entity_id,entity_pid,max(ord) ord FROM (` + prefixRanked + `) GROUP BY entity_id`
			args, tie = prefixArgs, "entity_id"
			order = searchOrder{numeric: true}
			countGroup = func() error {
				if knownExact {
					out.TotalCount, out.TotalCountExact = knownTotal, true
					return nil
				}
				// Multiple words and member restrictions are paged without an
				// exhaustive intersection count. The wire explicitly marks this.
				out.TotalCount, out.TotalCountExact = len(out.Items), false
				return nil
			}
		}
	}
	if prefixCursor != nil && !prefix {
		prefix = true
		prefixLead = prefixTerms[0]
		if len(prefixCursor.Keys) > 0 {
			prefixLead = prefixCursor.Keys[0]
			valid := false
			for _, word := range prefixTerms {
				if word == prefixLead {
					valid = true
				}
			}
			if !valid {
				return ErrCursor
			}
		}
		// A continuation retains its ranking model even when a growing numeric
		// dictionary now needs the full-text selector. Its signature still binds
		// every viewer, library, query and sort field.
		order = searchOrder{expr: `CASE WHEN d.title_fold=? THEN 3 ELSE COALESCE((SELECT max(p.tier) FROM catalog_search_prefix_rows p INDEXED BY catalog_search_prefix_rows_entity WHERE p.entity_id=d.entity_id AND p.term_id=(SELECT id FROM catalog_search_prefix_terms WHERE prefix=?)),0) END`, numeric: true}
		order.args = []any{facettext.SearchFold(r.Q), prefixLead}
		args = append(append(append([]any{}, order.args...), match), filterArgs...)
		ranked = `SELECT d.entity_id entity_id,d.entity_pid entity_pid,` + order.expr + ` ord FROM ` + candidates + ` WHERE 1` + where
		tie = "entity_id"
		countGroup = func() error { out.TotalCount, out.TotalCountExact = len(out.Items), false; return nil }
	}
	// Title, release year and date added break ties by the internal
	// identity, so a page taken from an ordered index and one ranked from the
	// full-text matches continue each other.
	internal := prefix
	if _, walks := searchWalks[sortID]; walks && !prefix {
		internal, tie = true, "entity_id"
		if walked, ok, err := s.searchTitleWalk(ctx, r, int(kind), sortID, where, filterArgs, pageSize+1, scopeHash, g, rev); err != nil {
			return err
		} else if ok {
			ranked, args = `SELECT json_extract(value,'$[0]') entity_id,json_extract(value,'$[1]') entity_pid,json_extract(value,'$[2]') ord FROM json_each(?)`, []any{walked}
		}
	}
	page := `WITH page AS MATERIALIZED (SELECT entity_id,entity_pid,ord FROM(` + ranked + `)`
	scope := s.groupCursorScope(r, g, scopeHash, sortID)
	if prefix {
		scope.Sort = "relevance_prefix"
	}
	if r.Cursor != "" {
		c, e := s.decodeRevisionCursor(r.Cursor, scope, rev)
		if e != nil {
			return e
		}
		if c.Fingerprint != fingerprint {
			c.Total = 0
		}
		value, e := cursorOrderValue(c.Value, order.numeric)
		if e != nil {
			return e
		}
		predicate := cursorPredicate(r.Direction)
		var anchor any = c.ID
		if internal {
			predicate = strings.ReplaceAll(predicate, "entity_pid", "entity_id")
			id, err := strconv.ParseInt(c.ID, 10, 64)
			if err != nil {
				return ErrCursor
			}
			anchor = id
		}
		page += ` WHERE ` + predicate
		args = append(args, value, value, anchor)
	}
	page += ` ORDER BY ord ` + r.Direction + `,` + tie + ` ` + r.Direction + ` LIMIT ?)`
	args = append(args, pageSize+1)
	// MATERIALIZED fixes the limit boundary before the detail joins and asset
	// subqueries. Those reads now run for at most pageSize+1 selected identities.
	// Personal resume state is read only for the admitted leaf identities, in
	// this same viewer snapshot. Missing progress stays absent, rather than
	// turning a container or an unseen item into a synthetic resume target.
	progress, episodeFacts := "NULL", "NULL,NULL,''"
	if g.view == "item" && !readingGroup {
		progress = `(SELECT p.position/1000.0 FROM progress p WHERE p.profile_id=? AND p.item_id=e.id)`
		args = append(args, r.Profile)
	}
	if g.kind == "episode" {
		episodeFacts = `context_season.number,context_episode.number,COALESCE(context_episode.numbering,'')`
	}
	query := page + ` SELECT pid(e.public_id),l.library_id,COALESCE(e.year,0),d.title,` + subtitle + ` subtitle,COALESCE(detail.poster_url,'') poster_url,COALESCE(detail.overview,'') overview,COALESCE(detail.added_text,'') added_at,COALESCE((SELECT max(a.duration) FROM catalog_asset_links link JOIN catalog_assets a ON a.id=link.asset_id WHERE link.entity_id=e.id),0) duration,EXISTS(SELECT 1 FROM catalog_asset_links link JOIN catalog_assets a ON a.id=link.asset_id WHERE link.entity_id=e.id AND link.available=1) available,page.ord,e.kind,e.id,` + progress + `,` + episodeFacts + ` FROM page JOIN catalog_entities e ON e.id=page.entity_id JOIN catalog_libraries l ON l.id=e.library_id JOIN catalog_search_documents d ON d.entity_id=e.id LEFT JOIN catalog_item_details detail ON detail.entity_id=e.id ` + contextJoin + ` ORDER BY page.ord ` + r.Direction + `,page.` + tie + ` ` + r.Direction
	rows, e := s.read().QueryContext(ctx, query, args...)
	if e != nil {
		return e
	}
	entries := []ContentEntry{}
	readingIDs := []int64{}
	orders := []string{}
	for rows.Next() {
		var entry ContentEntry
		var added string
		var duration float64
		var resume sql.NullFloat64
		var season, episode sql.NullInt64
		var numbering string
		var available bool
		var ord any
		var year int
		var entryKind compactcatalog.Kind
		var internalID int64
		if e = rows.Scan(&entry.ID, &entry.LibraryID, &year, &entry.Title, &entry.Subtitle, &entry.PosterURL, &entry.Overview, &added, &duration, &available, &ord, &entryKind, &internalID, &resume, &season, &episode, &numbering); e != nil {
			rows.Close()
			return e
		}
		entry.Kind = g.kind
		if readingGroup {
			entry.Kind, err = entryKind.Name()
			if err != nil || entry.Kind != "ebook" && entry.Kind != "comic" {
				rows.Close()
				return errors.New("invalid reading search projection")
			}
			readingIDs = append(readingIDs, internalID)
		}
		// A movie or show result says its year, so two titles of one name can be told apart.
		if year > 0 && (g.kind == "movie" || g.kind == "show") {
			entry.Year = &year
		}
		// PERF-12: search rows omit the synopsis (episodes keep it for the
		// episode-row rendering); backdrop stays as the poster fallback.
		if entry.Kind != "episode" {
			entry.Overview = ""
		}
		artwork := Item{ID: entry.ID, PosterURL: entry.PosterURL}
		projectArtwork(&artwork)
		entry.PosterURL = artwork.PosterURL
		entry.Navigation = &ContentNavigation{View: g.view, EntityID: entry.ID}
		if added != "" {
			entry.AddedAt = &added
		}
		if g.view == "item" && !readingGroup {
			entry.Duration = &duration
			entry.Available = &available
			if resume.Valid {
				entry.ProgressSeconds = &resume.Float64
			}
			if available {
				entry.Playback = &ContentPlayback{ItemID: entry.ID}
			}
		}
		if g.kind == "episode" {
			if season.Valid {
				number := int(season.Int64)
				entry.SeasonNumber = &number
				entry.Special = number == 0
			}
			if episode.Valid {
				number := int(episode.Int64)
				entry.EpisodeNumber = &number
				if numbering == "absolute" {
					entry.AbsoluteNumber = &number
				}
			}
			entry.DateBased = numbering == "date"
		}
		entries = append(entries, entry)
		orders = append(orders, formatOrderValue(ord))
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	if readingGroup {
		if e = s.nameReadingSearch(ctx, entries, readingIDs); e != nil {
			return e
		}
	}
	// Search containers have no items.poster_url. Resolve the selected artwork
	// for this bounded page in one query; an episode inherits season/show poster
	// art through the same resolver used by Browse and Home.
	targets := make([]artworkTarget, 0, len(entries))
	for _, entry := range entries {
		kind := entry.Kind
		if isItemEntity(kind) {
			kind = "item"
		}
		targets = append(targets, artworkTarget{Kind: kind, ID: entry.ID})
	}
	artwork, e := s.resolveArtworkContext(ctx, targets)
	if e != nil {
		return e
	}
	for index := range entries {
		art := artwork[targets[index]]
		if art.PosterURL != "" {
			entries[index].PosterURL = art.PosterURL
		}
		if art.BackdropURL != "" {
			entries[index].BackdropURL = art.BackdropURL
		}
	}
	if e = s.nameEntryMakers(entries); e != nil {
		return e
	}
	if len(entries) > pageSize {
		entries = entries[:pageSize]
		last := entries[len(entries)-1]
		anchorID := last.ID
		if internal {
			var id int64
			if e = s.read().QueryRowContext(ctx, `SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)`, last.ID).Scan(&id); e != nil {
				return e
			}
			anchorID = strconv.FormatInt(id, 10)
		}
		out.NextCursor, e = s.encodeRevisionCursor(cursorValue{Scope: scope, Value: orders[pageSize-1], ID: anchorID, Keys: func() []string {
			if prefix {
				return []string{prefixLead}
			}
			return nil
		}(), Fingerprint: fingerprint, Expires: time.Now().Add(30 * time.Minute).Unix()}, rev)
		if e != nil {
			return e
		}
		out.HasMore = true
	} else if r.Cursor == "" {
		// The first page asked for one more row than it wanted and did not get
		// it, so the group holds exactly what was read.
		out.TotalCount = len(entries)
		out.TotalCountExact = true
		out.Items = entries
		return nil
	}
	out.Items = entries
	if e = countGroup(); e != nil {
		return e
	}
	return nil
}

func cursorPredicate(direction string) string {
	if direction == "desc" {
		return `(ord<? OR (ord=? AND entity_pid<?))`
	}
	return `(ord>? OR (ord=? AND entity_pid>?))`
}
func cursorOrderValue(raw string, numeric bool) (any, error) {
	if !numeric {
		return raw, nil
	}
	v, e := strconv.ParseFloat(raw, 64)
	if e != nil {
		return nil, ErrCursor
	}
	return v, nil
}
func formatOrderValue(v any) string {
	switch value := v.(type) {
	case nil:
		return ""
	case string:
		return value
	case []byte:
		return string(value)
	case int64:
		return strconv.FormatFloat(float64(value), 'f', 6, 64)
	case float64:
		return strconv.FormatFloat(value, 'f', 6, 64)
	}
	return fmt.Sprint(v)
}

func searchContext(kind string) (string, string) {
	switch kind {
	case "episode":
		return ` LEFT JOIN catalog_episodes context_episode ON context_episode.entity_id=e.id LEFT JOIN catalog_entities context_show ON context_show.id=context_episode.show_id AND context_show.library_id=e.library_id LEFT JOIN catalog_seasons context_season ON context_season.entity_id=context_episode.season_id AND context_season.show_id=context_show.id `,
			`COALESCE(context_show.title||' · '||CASE WHEN context_episode.numbering='absolute' THEN 'Absolute episode '||context_episode.number||' (unassigned season)' WHEN context_season.number=0 THEN 'Specials · Episode '||context_episode.number WHEN context_season.number IS NOT NULL THEN 'Season '||context_season.number||' · Episode '||context_episode.number ELSE 'Episode '||context_episode.number END,'')`
	case "song":
		return ` LEFT JOIN catalog_songs context_song ON context_song.entity_id=e.id LEFT JOIN catalog_entities context_album ON context_album.id=context_song.album_id AND context_album.library_id=e.library_id LEFT JOIN catalog_albums context_album_detail ON context_album_detail.entity_id=context_album.id LEFT JOIN catalog_entities context_artist ON context_artist.id=context_album_detail.artist_id AND context_artist.library_id=e.library_id `,
			`COALESCE(context_album.title,'')||CASE WHEN context_artist.title IS NOT NULL THEN ' · '||context_artist.title ELSE '' END`
	case "album":
		return ` LEFT JOIN catalog_albums context_album ON context_album.entity_id=e.id LEFT JOIN catalog_entities context_artist ON context_artist.id=context_album.artist_id AND context_artist.library_id=e.library_id `,
			`COALESCE(context_artist.title,'')`
	}
	return "", "''"
}

// searchPeopleGroup pages canonical people by an offset cursor whose signed
// ordering stays the same throughout the traversal.
func (s *Service) searchPeopleGroup(ctx context.Context, r SearchRequest, g searchGroupDefinition, match, scopeHash, fingerprint string, rev ContentRevision, pageSize int, out *SearchGroupResult) error {
	libraries := r.Libraries
	if r.AllLibraries {
		all, e := s.allLibraryIDs(ctx)
		if e != nil {
			return e
		}
		libraries = all
	}
	viewer := Viewer{Profile: r.Profile, Fence: r.ViewerFence, Libraries: libraries, Restrictions: r.Restrictions}
	if e := s.prepareViewer(viewer); e != nil {
		return e
	}
	sortID := resolveSearchSort(g, r.Sort)
	scope := s.groupCursorScope(r, g, scopeHash, sortID)
	offset := 0
	// The signed cursor names the ordering. Older offset-only cursors could
	// cross from a name walk to relevance partway through the same traversal.
	ordering := "people:" + sortID
	if r.Cursor != "" {
		c, e := s.decodeRevisionCursor(r.Cursor, scope, rev)
		if e != nil {
			return e
		}
		if c.Fingerprint != fingerprint {
			c.Total = 0
		}
		if c.Value != ordering {
			return ErrCursor
		}
		if offset, e = strconv.Atoi(c.ID); e != nil || offset < 0 || offset > int(^uint(0)>>1)-pageSize-1 {
			return ErrCursor
		}
	}
	people, total, exact, e := s.peopleMatches(r.Q, match, viewer, pageSize+1, offset, sortID, r.Direction)
	if e != nil {
		return e
	}
	// The matches are counted up to peopleRankedMaximum; a broader name
	// search says so rather than counting every match.
	out.TotalCount, out.TotalCountExact = total, exact
	more := len(people) > pageSize
	if more {
		people = people[:pageSize]
	}
	entries := []ContentEntry{}
	for _, person := range people {
		count := person.CreditCount
		entries = append(entries, ContentEntry{ID: person.ID, Kind: g.kind, Title: person.Name, Subtitle: strings.Join(person.Roles, " · "), PosterURL: person.PortraitURL, Count: &count, Navigation: &ContentNavigation{View: g.view, EntityID: person.ID}})
	}
	out.Items = entries
	if more {
		if offset+pageSize > int(^uint(0)>>1)-pageSize-1 {
			return ErrCursor
		}
		out.HasMore = true
		out.NextCursor, e = s.encodeRevisionCursor(cursorValue{Scope: scope, Value: ordering, ID: strconv.Itoa(offset + pageSize), Fingerprint: fingerprint, Expires: time.Now().Add(30 * time.Minute).Unix()}, rev)
		if e != nil {
			return e
		}
	}
	return nil
}

func (s *Service) allLibraryIDs(ctx context.Context) ([]string, error) {
	rows, e := s.read().QueryContext(ctx, `SELECT library_id FROM catalog_libraries WHERE retired=0 ORDER BY library_id`)
	if e != nil {
		return nil, e
	}
	out := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return nil, e
		}
		out = append(out, id)
	}
	e = rows.Err()
	rows.Close()
	return out, e
}

// searchLiveGroup matches the active published channel lineup. Channels are not
// library scoped; the caller states whether this viewer may read live TV at all.
func (s *Service) searchLiveGroup(ctx context.Context, r SearchRequest, g searchGroupDefinition, scopeHash, fingerprint string, rev ContentRevision, pageSize int, out *SearchGroupResult) error {
	// Enforce denial at the lookup too: another composition must not bypass
	// the permission by calling this without Search's group selection.
	if !r.LiveTV {
		if r.Cursor != "" {
			return ErrCursor
		}
		return ErrSearchQuery
	}
	contains := "%" + strings.ToLower(searchPrefix(r.Q))
	where := ` FROM live_channel_versions v JOIN live_sources s ON s.active_generation=v.generation_id AND s.state='active' WHERE lower(v.name) LIKE ? ESCAPE '\'`
	if e := s.read().QueryRowContext(ctx, `SELECT count(*)`+where, contains).Scan(&out.TotalCount); e != nil {
		return e
	}
	sortID := resolveSearchSort(g, r.Sort)
	order := `CASE WHEN lower(v.name)=? THEN 1000 ELSE 0 END+CASE WHEN lower(v.name) LIKE ? ESCAPE '\' THEN 100 ELSE 0 END`
	args := []any{strings.ToLower(r.Q), strings.ToLower(searchPrefix(r.Q)), contains}
	ordering := ` ORDER BY ord ` + r.Direction + `,lower(v.name),v.channel_id`
	if sortID == "title" {
		order = `0`
		args = []any{contains}
		ordering = ` ORDER BY lower(v.name) ` + r.Direction + `,v.channel_id`
	}
	scope := s.groupCursorScope(r, g, scopeHash, sortID)
	offset := 0
	if r.Cursor != "" {
		c, e := s.decodeRevisionCursor(r.Cursor, scope, rev)
		if e != nil {
			return e
		}
		if c.Fingerprint != fingerprint {
			c.Total = 0
		}
		if offset, e = strconv.Atoi(c.ID); e != nil || offset < 0 || offset > 100000 {
			return ErrCursor
		}
	}
	args = append(args, pageSize+1, offset)
	rows, e := s.read().QueryContext(ctx, `SELECT s.id,v.channel_id,v.name,v.number,v.group_name,`+order+` ord`+where+ordering+` LIMIT ? OFFSET ?`, args...)
	if e != nil {
		return e
	}
	entries := []ContentEntry{}
	for rows.Next() {
		var source, id, name, number, group string
		var ord float64
		if e = rows.Scan(&source, &id, &name, &number, &group, &ord); e != nil {
			rows.Close()
			return e
		}
		subtitle := strings.TrimSpace(strings.Trim(number+" · "+group, " ·"))
		entries = append(entries, ContentEntry{ID: id, Kind: g.kind, Title: name, Subtitle: subtitle, Navigation: &ContentNavigation{View: g.view, EntityID: id, SourceID: source, ChannelID: id, Provenance: "live-source"}})
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	if len(entries) > pageSize {
		entries = entries[:pageSize]
		out.HasMore = true
		out.NextCursor, e = s.encodeRevisionCursor(cursorValue{Scope: scope, Value: "offset", ID: strconv.Itoa(offset + pageSize), Fingerprint: fingerprint, Expires: time.Now().Add(30 * time.Minute).Unix()}, rev)
		if e != nil {
			return e
		}
	}
	out.Items = entries
	return nil
}

// searchLibraryTokens names the viewer's libraries as the title index's
// library tokens (l<id>).
func (s *Service) searchLibraryTokens(ctx context.Context, libraries []string) ([]string, error) {
	raw, _ := json.Marshal(libraries)
	rows, err := s.read().QueryContext(ctx, `SELECT id FROM catalog_libraries WHERE library_id IN(SELECT value FROM json_each(?)) AND retired=0`, string(raw))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, "l"+strconv.FormatInt(id, 10))
	}
	return out, rows.Err()
}

func searchGroupLabelKey(id string) string {
	switch id {
	case "books":
		return "search.chip.audiobooks"
	case "live-tv":
		return "search.chip.liveTV"
	}
	return "search.chip." + id
}

func searchCJKPredicate(column, q string) (string, []any) {
	clause := ""
	args := []any{}
	for _, word := range strings.Fields(facettext.SearchFold(q)) {
		if strings.ContainsFunc(word, facettext.CJK) {
			clause += " AND instr(" + column + ",?)>0"
			args = append(args, word)
		}
	}
	return clause, args
}
