package db

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server-next/internal/searchtext"
)

// A search matches whole words and the beginnings of words, in titles and in the names of
// people, ignoring case, accents and punctuation. It does not correct typing mistakes.
//
// The index knows nothing of who may see what. A search takes the ids that match, best first,
// from search.db and checks them against the main database with the same condition every
// other read uses, a batch at a time, until the page is full.
//
// Best first means: a title that is exactly what was typed, then one that begins with it,
// then one that contains every word, then a match through a name outside the title (the
// original title, or the show, artist or album it belongs to). Inside each of those the order
// is the order titles were added to the server, which keeps a scrolled list stable.

// SearchBest is the sort a search has unless another is asked for. A search of one kind can
// also be sorted by SortTitle, SortYear or SortAdded.
const SearchBest = "best"

const (
	searchGroupSize = 12 // results of each kind when every kind is asked for
	searchGroupMax  = 40
	// searchMaxWords bounds a query written without spaces, where every character is a word.
	searchMaxWords = 24
)

var (
	// searchCheckCap is the most matches one request checks against the main database for
	// one kind. It bounds what a profile with restrictions, or a sort, costs on a search as
	// wide as one letter. A page that stops at it carries a cursor to go on from; a count of
	// people that stops at it is marked as a minimum (titles are always counted exactly);
	// a sort over more matches than this is refused.
	searchCheckCap = 2000
)

// SearchQuery is what to search for. Kind is one of the group names ("movie", "person", ...)
// or empty for the first results of every group.
type SearchQuery struct {
	Text   string
	Kind   string
	Sort   string
	Desc   bool
	Cursor string
	Limit  int
}

// SearchGroup is the results of one kind. People is filled for the "person" group and Items
// for every other. Total is how many results of this kind the viewer may see; it is counted
// (Counted) on a first page and not on later ones. TotalIsMinimum says the count stopped at
// searchCheckCap matches and there may be more.
type SearchGroup struct {
	Kind           string
	Items          []Item
	People         []Person
	Counted        bool
	Total          int
	TotalIsMinimum bool
	NextCursor     string
}

// SearchResults is the answer to a search: its groups in the order they are shown, and for
// the items in them, what each sits under (an episode's season and show, a track's album and
// artist) by public id, so a result can say where it belongs.
type SearchResults struct {
	Groups []SearchGroup
	Above  map[string]ItemRef
}

// Suggestion is one title or name to offer while a person types.
type Suggestion struct {
	Kind     string // an item kind, or "person"
	PublicID string
	Title    string
}

// searchTerms is what was typed, as words. See searchtext.Words for the two forms.
type searchTerms struct{ split, joined []string }

func parseSearch(text string) (searchTerms, error) {
	text = strings.TrimSpace(text)
	switch {
	case text == "":
		return searchTerms{}, invalid("type something to search for")
	case utf8.RuneCountInString(text) > 128:
		return searchTerms{}, invalid("a search is at most 128 characters")
	case len(strings.Fields(text)) > 8:
		return searchTerms{}, invalid("a search is at most 8 words")
	}
	split, joined := searchtext.Words(text)
	if len(split) == 0 {
		return searchTerms{}, invalid("a search needs a letter or a number")
	}
	if len(split) > searchMaxWords {
		split, joined = split[:searchMaxWords], nil
	}
	return searchTerms{split, joined}, nil
}

// either writes an expression for both forms of the words, so "spider-man" finds a title
// indexed as "spider man" and one indexed as "spiderman".
func (t searchTerms) either(expr func(words []string) string) string {
	if t.joined == nil {
		return expr(t.split)
	}
	return "(" + expr(t.split) + " OR " + expr(t.joined) + ")"
}

// searchMatch is the full-text expressions of one search: a tier for each quality of match,
// best first and none overlapping another, and one for all of them.
type searchMatch struct {
	tiers []string
	all   string
}

// match builds the expressions. Words hold only letters and numbers, so they need no
// escaping, and none can match one of a row's keys.
func (t searchTerms) match() searchMatch {
	const title = "{title joined}"
	exact := t.either(func(w []string) string { return `"` + searchWhole(w) + `"` })
	begins := t.either(func(w []string) string { return `(` + title + ` : ^ "` + strings.Join(w, " ") + `" *)` })
	every := func(columns string) string {
		return t.either(func(w []string) string { return `(` + columns + `("` + strings.Join(w, `"* AND "`) + `"*))` })
	}
	inTitle, anywhere := every(title+" : "), every("")
	return searchMatch{all: anywhere, tiers: []string{exact, begins + " NOT " + exact, inTitle + " NOT " + begins, anywhere + " NOT " + inTitle}}
}

// searchCursor is where the next page of one kind starts. Clients treat it as opaque.
type searchCursor struct {
	Kind   string `json:"k"`
	Sort   string `json:"s"`
	Desc   bool   `json:"d,omitempty"`
	Tier   int    `json:"t,omitempty"`
	After  int64  `json:"a,omitempty"` // best first: the last row of the tier already sent
	Offset int    `json:"o,omitempty"` // any other sort: how many rows were sent
}

func (c searchCursor) encode() string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// searcher is one request's view of the two files.
type searcher struct {
	ctx   context.Context
	tx    *sql.Tx // the main database
	index *sql.DB // search.db
	v     Viewer
	acc   access
	match searchMatch
	// narrow is added to an expression over titles to leave out what the viewer's libraries
	// and rating limit hide, by the facts each row carries. It only saves work: every row
	// sent is still checked against the main database, and a count is tested against that.
	narrow string
	// What counting people already found out about who may be seen: each row up to
	// checkedTo, by group. A page asks the main database only beyond that.
	checked   map[int64]bool
	checkedTo map[int64]int64
}

// narrowing writes the viewer's libraries and rating limit as an expression over the facts
// in the index: " AND (its libraries) AND (the rating ages within its limit)", or nothing.
func (a access) narrowing() string {
	var out string
	if a.known && !a.allLibraries {
		keys := make([]string, len(a.libraries))
		for n, id := range a.libraries {
			keys[n] = `"` + searchLibrary(id) + `"`
		}
		if len(keys) == 0 {
			keys = []string{`"` + searchLibrary(0) + `"`} // no library at all: nothing matches
		}
		out += " AND (" + strings.Join(keys, " OR ") + ")"
	}
	if a.maxAge != nil {
		keys := []string{`"` + searchRating(-1) + `"`} // a limit below every rating: nothing matches
		for age := 0; age <= *a.maxAge; age++ {
			keys = append(keys, `"`+searchRating(int64(age))+`"`)
		}
		out += " AND (" + strings.Join(keys, " OR ") + ")"
	}
	return out
}

// over is an expression for the rows of one group: narrowed when the group holds titles.
func (s *searcher) over(expr string, code int64) string {
	if code == searchPeople || code == 0 || s.narrow == "" {
		return expr
	}
	return "(" + expr + ")" + s.narrow
}

// searcher opens both files for one request. The caller calls done when it has its answer.
func (d *DB) searcher(ctx context.Context, v Viewer, terms searchTerms) (s *searcher, done func(), err error) {
	ix := d.search
	if ix == nil {
		return nil, nil, ErrSearchUnavailable
	}
	ix.mu.RLock()
	if ix.read == nil {
		ix.mu.RUnlock()
		return nil, nil, ErrSearchUnavailable
	}
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err == nil {
		s = &searcher{ctx: ctx, tx: tx, index: ix.read, v: v, match: terms.match(), checked: map[int64]bool{}, checkedTo: map[int64]int64{}}
		if s.acc, err = loadAccess(ctx, tx, v); err != nil {
			tx.Rollback()
		}
		s.narrow = s.acc.narrowing()
	}
	if err != nil {
		ix.mu.RUnlock()
		return nil, nil, err
	}
	return s, func() { tx.Rollback(); ix.mu.RUnlock() }, nil
}

// Search returns results in groups. With no Kind every group comes with its first results and
// its total. With a Kind, that group comes with a page of results; its first page also
// carries every group's total, so an app can show them all.
func (d *DB) Search(ctx context.Context, v Viewer, q SearchQuery) (SearchResults, error) {
	var answer SearchResults
	fail := func(err error) (SearchResults, error) { return SearchResults{}, err }
	terms, err := parseSearch(q.Text)
	if err != nil {
		return fail(err)
	}
	var only *searchGroup
	for n := range searchGroups {
		if searchGroups[n].name == q.Kind {
			only = &searchGroups[n]
		}
	}
	if q.Kind != "" && only == nil {
		return fail(invalid("unknown kind %q", q.Kind))
	}
	if q.Sort == "" {
		q.Sort = SearchBest
	}
	switch {
	case q.Sort == SearchBest:
	case only == nil:
		return fail(invalid("results of every kind come best match first; choose a kind to sort them"))
	case q.Sort == SortTitle, (q.Sort == SortYear || q.Sort == SortAdded) && only.code != searchPeople && only.name != "artist":
	default:
		return fail(invalid("unknown sort %q for %s results", q.Sort, q.Kind))
	}
	var from searchCursor
	if q.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(q.Cursor)
		if err == nil {
			err = json.Unmarshal(raw, &from)
		}
		if err != nil || from.Kind != q.Kind || from.Sort != q.Sort || from.Desc != q.Desc || from.Tier < 0 || from.Offset < 0 {
			return fail(invalid("the cursor does not belong to this search"))
		}
	}
	limit, most := searchGroupSize, searchGroupMax
	if only != nil {
		limit, most = defaultPageSize, maxPageSize
	}
	if q.Limit > 0 {
		limit = min(q.Limit, most)
	}

	s, done, err := d.searcher(ctx, v, terms)
	if err != nil {
		return fail(err)
	}
	defer done()
	var totals map[int64]searchTotal
	if q.Cursor == "" {
		if totals, err = s.totals(); err != nil {
			return fail(err)
		}
	}
	var out []SearchGroup
	var pages [][]int64 // the rows of each group in out
	var shown []int64
	for n := range searchGroups {
		g := &searchGroups[n]
		if q.Cursor != "" && g != only {
			continue
		}
		total := totals[g.code]
		group := SearchGroup{Kind: g.name, Counted: q.Cursor == "", Total: total.visible, TotalIsMinimum: total.minimum}
		var rows []int64
		if (only == nil || only == g) && (!group.Counted || group.Total > 0) {
			// A group's cursor names the group, so the rest of it is asked for by its kind.
			start, next := searchCursor{Kind: g.name, Sort: q.Sort, Desc: q.Desc}, searchCursor{}
			if q.Cursor != "" {
				start = from
			}
			if q.Sort == SearchBest {
				known := -1
				if group.Counted && !group.TotalIsMinimum {
					known = group.Total
				}
				rows, next, err = s.best(s.match.tiers, g.code, start, limit, known)
			} else {
				rows, next, err = s.sorted(g.code, start, limit)
			}
			if err != nil {
				return fail(err)
			}
			if next.Tier >= 0 {
				group.NextCursor = next.encode()
			}
		}
		out, pages, shown = append(out, group), append(pages, rows), append(shown, rows...)
	}
	// Everything shown is loaded together: one read of items, pictures and counts, not one a group.
	items, people, err := s.load(shown)
	if err != nil {
		return fail(err)
	}
	if answer.Above, err = s.above(items); err != nil {
		return fail(err)
	}
	for n, rows := range pages {
		for _, row := range rows {
			if item, ok := items[row]; ok {
				out[n].Items = append(out[n].Items, item)
			}
			if person, ok := people[row]; ok {
				out[n].People = append(out[n].People, person)
			}
		}
	}
	answer.Groups = out
	return answer, nil
}

// candidates reads the rows of one group (or, with code 0, of every group) that match, in
// row order, after a given row.
func (s *searcher) candidates(expr string, code, after int64, limit int) ([]int64, error) {
	first, last := searchRow(code, 0), searchRow(code+1, 0)-1
	if code == 0 {
		last = searchRow(int64(len(searchGroups))+1, 0)
	}
	rows, err := s.index.QueryContext(s.ctx, `SELECT rowid FROM search_titles WHERE search_titles MATCH ? AND rowid > ? AND rowid <= ? ORDER BY rowid LIMIT ?`,
		s.over(expr, code), max(after, first), last, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var row int64
		if err := rows.Scan(&row); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// seesAll reports whether nothing is kept from the viewer, so a search has nothing to check.
func (a access) seesAll() bool { return a.visible("i") == "(1)" }

// The sources that pick, from ids bound as @ids, the items and the people the viewer may
// see. A person is seen through any title of theirs the viewer may see. CROSS JOIN keeps the
// order as written, so each statement starts from the ids and looks each one up; left to
// choose, SQLite may start from a library's whole index instead.
func (s *searcher) visibleItems() string {
	return `FROM json_each(@ids) j CROSS JOIN items i ON i.id = j.value WHERE ` + s.acc.visible("i")
}

func (s *searcher) visiblePeople() string {
	return `FROM json_each(@ids) j CROSS JOIN people p ON p.id = j.value WHERE EXISTS (SELECT 1 FROM credits c
		CROSS JOIN items i ON i.id = c.item_id WHERE c.person_id = p.id AND ` + s.acc.visible("i") + `)`
}

func (s *searcher) args(ids []int64) []any { return append(s.v.args(), sql.Named("ids", idsJSON(ids))) }

// split separates rows into the ids of items and the ids of people.
func splitRows(rows []int64) (items, people []int64) {
	for _, row := range rows {
		if row>>searchIDBits == searchPeople {
			people = append(people, searchRowID(row))
		} else {
			items = append(items, searchRowID(row))
		}
	}
	return items, people
}

// check asks the main database which of some rows the viewer may see: one statement for the
// items among them and one for the people, however many groups they come from.
func (s *searcher) check(rows []int64) (map[int64]bool, error) {
	seen := make(map[int64]bool, len(rows))
	items, people := splitRows(rows)
	itemRow := make(map[int64]int64, len(items)) // an item is in one group, so its id names its row
	for _, row := range rows {
		if row>>searchIDBits != searchPeople {
			itemRow[searchRowID(row)] = row
		}
	}
	if len(items) > 0 {
		found, err := ids(s.ctx, s.tx, `SELECT i.id `+s.visibleItems(), s.args(items)...)
		if err != nil {
			return nil, err
		}
		for _, id := range found {
			seen[itemRow[id]] = true
		}
	}
	if len(people) > 0 {
		found, err := ids(s.ctx, s.tx, `SELECT p.id `+s.visiblePeople(), s.args(people)...)
		if err != nil {
			return nil, err
		}
		for _, id := range found {
			seen[searchRow(searchPeople, id)] = true
		}
	}
	return seen, nil
}

// visible says which of some matching rows the viewer may see. With nothing kept from the
// viewer that is all of them: a row whose title was deleted a moment ago is dropped when the
// page is loaded.
func (s *searcher) visible(rows []int64) (map[int64]bool, error) {
	seen := make(map[int64]bool, len(rows))
	var ask []int64
	for _, row := range rows {
		switch {
		case s.acc.seesAll():
			seen[row] = true
		case row <= s.checkedTo[row>>searchIDBits]:
			seen[row] = s.checked[row]
		default:
			ask = append(ask, row)
		}
	}
	if len(ask) == 0 {
		return seen, nil
	}
	asked, err := s.check(ask)
	for _, row := range ask {
		seen[row] = asked[row]
	}
	return seen, err
}

type searchTotal struct {
	visible int
	minimum bool
}

// totals counts the results of every kind the viewer may see.
//
// Titles are counted exactly without reading their rows: one pass over the index counts the
// matches in the viewer's libraries and within its rating limit, by the facts each row
// carries, and a second takes away those a blocked label hides, which are found from the
// label's side in the main database. With nothing kept from the viewer the first pass is all.
//
// People have no such facts: a person is seen through any one title. For a viewer with
// restrictions the matching people are checked one by one, up to searchCheckCap of them, and
// what was learned is kept for the pages that follow.
func (s *searcher) totals() (map[int64]searchTotal, error) {
	totals := map[int64]searchTotal{}
	if !s.acc.known {
		return totals, nil
	}
	count := func(query string, sign int, args ...any) error {
		rows, err := s.index.QueryContext(s.ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var code int64
			var n int
			if err := rows.Scan(&code, &n); err != nil {
				return err
			}
			total := totals[code]
			total.visible += sign * n
			totals[code] = total
		}
		return rows.Err()
	}
	const grouped = `SELECT rowid >> ?1, count(*) FROM search_titles WHERE search_titles MATCH ?2`
	if s.acc.seesAll() {
		return totals, count(grouped+` GROUP BY 1`, 1, searchIDBits, s.match.all)
	}
	titles := s.over(s.match.all, searchGroups[0].code)
	if err := count(grouped+` AND rowid >> ?1 != ?3 GROUP BY 1`, 1, searchIDBits, titles, searchPeople); err != nil {
		return nil, err
	}
	if len(s.acc.blockedTags) > 0 {
		// Everything a blocked label hides: what carries one, and what sits under that.
		tags := intList(s.acc.blockedTags)
		hidden, err := ids(s.ctx, s.tx, `SELECT item_id FROM item_tags WHERE tag_id IN `+tags+`
			UNION SELECT c.id FROM item_tags h CROSS JOIN items c WHERE h.tag_id IN `+tags+` AND c.parent_id = h.item_id
			UNION SELECT c.id FROM item_tags h CROSS JOIN items c WHERE h.tag_id IN `+tags+` AND c.root_id = h.item_id`)
		if err != nil {
			return nil, err
		}
		if err := count(grouped+` AND rowid >> ?1 != ?3 AND (rowid & ?4) IN (SELECT value FROM json_each(?5)) GROUP BY 1`, -1,
			searchIDBits, titles, searchPeople, int64(1)<<searchIDBits-1, idsJSON(hidden)); err != nil {
			return nil, err
		}
	}
	people, err := s.candidates(s.match.all, searchPeople, 0, searchCheckCap+1)
	if err != nil {
		return nil, err
	}
	minimum := len(people) > searchCheckCap
	if minimum {
		people = people[:searchCheckCap]
	}
	if len(people) > 0 {
		s.checkedTo[searchPeople] = people[len(people)-1]
	}
	if s.checked, err = s.check(people); err != nil {
		return nil, err
	}
	totals[searchPeople] = searchTotal{visible: len(s.checked), minimum: minimum}
	return totals, nil
}

// best reads the next limit results of a group, best first, from the position in from. The
// cursor it returns is where the next page starts; its Tier is -1 when there is nothing more.
// known is how many results there are in all when that has been counted, or -1: a first page
// that holds them all need not look further.
func (s *searcher) best(tiers []string, code int64, from searchCursor, limit, known int) ([]int64, searchCursor, error) {
	next, end := from, from
	end.Tier = -1
	var hits []int64
	checked, batch := 0, max(2*(limit+1), 50)
	tier, after := from.Tier, from.After
	for tier < len(tiers) && len(hits) != known {
		candidates, err := s.candidates(tiers[tier], code, after, batch)
		if err != nil {
			return nil, end, err
		}
		seen, err := s.visible(candidates)
		if err != nil {
			return nil, end, err
		}
		for _, row := range candidates {
			if !seen[row] {
				continue
			}
			if len(hits) == limit {
				return hits, next, nil // there is more: the next page starts after the last row sent
			}
			hits = append(hits, row)
			next.Tier, next.After = tier, row
		}
		checked += len(candidates)
		switch {
		case len(candidates) < batch:
			tier, after = tier+1, 0
		case checked >= searchCheckCap:
			// Almost nothing of what matches is this viewer's to see. Stop here; the next
			// page carries on from the last match checked.
			next.Tier, next.After = tier, candidates[len(candidates)-1]
			return hits, next, nil
		default:
			after, batch = candidates[len(candidates)-1], min(batch*4, 1000)
		}
	}
	return hits, end, nil
}

// sorted reads the next limit results of a group in the order of a column of the main database.
func (s *searcher) sorted(code int64, from searchCursor, limit int) ([]int64, searchCursor, error) {
	end := from
	end.Tier = -1
	candidates, err := s.candidates(s.match.all, code, 0, searchCheckCap+1)
	if err != nil {
		return nil, end, err
	}
	if len(candidates) > searchCheckCap {
		return nil, end, invalid("too many results to sort; type more of the name first")
	}
	direction := ""
	if from.Desc {
		direction = " DESC"
	}
	items, people := splitRows(candidates)
	query, among := `SELECT i.id `+s.visibleItems()+` ORDER BY i.sort_title`+direction+`, i.id`+direction, items
	switch {
	case code == searchPeople:
		query, among = `SELECT p.id `+s.visiblePeople()+` ORDER BY p.sort_name`+direction+`, p.id`+direction, people
	case from.Sort == SortYear:
		query = `SELECT i.id ` + s.visibleItems() + ` ORDER BY i.year IS NULL, i.year` + direction + `, i.sort_title, i.id`
	case from.Sort == SortAdded:
		query = `SELECT i.id ` + s.visibleItems() + ` ORDER BY i.added_at` + direction + `, i.id` + direction
	}
	found, err := ids(s.ctx, s.tx, query+` LIMIT @limit OFFSET @offset`,
		append(s.args(among), sql.Named("limit", limit+1), sql.Named("offset", from.Offset))...)
	if err != nil {
		return nil, end, err
	}
	for n := range found {
		found[n] = searchRow(code, found[n])
	}
	if len(found) <= limit {
		return found, end, nil
	}
	from.Offset += limit
	return found[:limit], from, nil
}

// load reads the items and the people behind some rows, with the viewer's state and their
// pictures. A row the index still holds for something deleted a moment ago is left out.
func (s *searcher) load(rows []int64) (map[int64]Item, map[int64]Person, error) {
	itemIDs, personIDs := splitRows(rows)
	items, people := map[int64]Item{}, map[int64]Person{}
	if len(itemIDs) > 0 {
		found, err := s.tx.QueryContext(s.ctx, `SELECT `+itemColumns+` FROM json_each(@ids) j CROSS JOIN items i ON i.id = j.value`+stateJoin+s.acc.visible("i"),
			s.args(itemIDs)...)
		if err != nil {
			return nil, nil, err
		}
		defer found.Close()
		var list []Item
		for found.Next() {
			var it Item
			if err := scanItem(found, &it); err != nil {
				return nil, nil, err
			}
			list = append(list, it)
		}
		if err := found.Close(); err != nil {
			return nil, nil, err
		}
		if err := loadImages(s.ctx, s.tx, list); err != nil {
			return nil, nil, err
		}
		if err := loadLeafCounts(s.ctx, s.tx, s.v, s.acc, list); err != nil {
			return nil, nil, err
		}
		for _, it := range list {
			items[searchRow(searchGroupOf[it.Kind], it.ID)] = it
		}
	}
	if len(personIDs) > 0 {
		found, err := s.tx.QueryContext(s.ctx, `SELECT p.id, p.public_id, p.name, g.id, ifnull(g.role, 0), ifnull(g.file_hash, ''), ifnull(g.width, 0),
			ifnull(g.height, 0), ifnull(g.placeholder, '') FROM json_each(@ids) j CROSS JOIN people p ON p.id = j.value
			LEFT JOIN images g ON g.person_id = p.id AND g.selected = 1`, s.args(personIDs)...)
		if err != nil {
			return nil, nil, err
		}
		defer found.Close()
		for found.Next() {
			var id int64
			var p Person
			var imageID sql.NullInt64
			var image Image
			if err := found.Scan(&id, &p.PublicID, &p.Name, &imageID, &image.Role, &image.FileHash, &image.Width, &image.Height, &image.Placeholder); err != nil {
				return nil, nil, err
			}
			row := searchRow(searchPeople, id)
			if known, ok := people[row]; ok {
				p = known
			}
			if imageID.Valid {
				image.ID = imageID.Int64
				p.Images = append(p.Images, image)
			}
			people[row] = p
		}
		if err := found.Err(); err != nil {
			return nil, nil, err
		}
	}
	return items, people, nil
}

// above reads what the loaded items sit under, leaving out anything the viewer may not see.
func (s *searcher) above(items map[int64]Item) (map[string]ItemRef, error) {
	var ids []int64
	for _, item := range items {
		for _, id := range []int64{item.ParentID, item.RootID} {
			if id != 0 {
				ids = append(ids, id)
			}
		}
	}
	refs := map[string]ItemRef{}
	if len(ids) == 0 {
		return refs, nil
	}
	rows, err := s.tx.QueryContext(s.ctx, `SELECT i.public_id, i.kind, i.title, i.index_number `+s.visibleItems(), s.args(ids)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var ref ItemRef
		if err := rows.Scan(&ref.PublicID, &ref.Kind, &ref.Title, &ref.Index); err != nil {
			return nil, err
		}
		refs[ref.PublicID] = ref
	}
	return refs, rows.Err()
}

// SearchSuggestions returns the few best titles and names for what has been typed so far,
// matching in the title or name only.
func (d *DB) SearchSuggestions(ctx context.Context, v Viewer, text string, limit int) ([]Suggestion, error) {
	terms, err := parseSearch(text)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 8
	}
	limit = min(limit, 20)
	s, done, err := d.searcher(ctx, v, terms)
	if err != nil {
		return nil, err
	}
	defer done()
	rows, _, err := s.best(s.match.tiers[:3], 0, searchCursor{}, limit, -1)
	if err != nil {
		return nil, err
	}
	items, people, err := s.load(rows)
	if err != nil {
		return nil, err
	}
	var out []Suggestion
	for _, row := range rows {
		if item, ok := items[row]; ok {
			out = append(out, Suggestion{Kind: item.Kind.String(), PublicID: item.PublicID, Title: item.Title})
		}
		if person, ok := people[row]; ok {
			out = append(out, Suggestion{Kind: "person", PublicID: person.PublicID, Title: person.Name})
		}
	}
	return out, nil
}
