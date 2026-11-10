package db

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Item is one row of a list, with the viewer's own state.
type Item struct {
	ID              int64
	PublicID        string
	Kind            ItemKind
	Title           string
	Year            int
	ReleaseDate     string
	AddedAt         int64
	DurationMS      int64
	IndexNumber     *int
	ParentIndex     *int
	ChildCount      int
	ContentRating   string
	CommunityRating float64
	ParentID        int64
	RootID          int64
	ParentPublicID  string
	RootPublicID    string
	LibraryID       string // the library's public id, so a card can build the address of its page
	Images          []Image
	State           State
	// For an item that holds others: how many playable items under it the viewer may see, and
	// how many of those are watched. A show is watched when the two are equal and not zero.
	Leaves        int
	WatchedLeaves int

	sortTitle string
}

// Image is one chosen picture of an item or a person.
type Image struct {
	ID          int64
	Role        ImageRole
	FileHash    string
	Width       int
	Height      int
	Placeholder string
}

// Sorts of the item list.
const (
	SortTitle   = "title"
	SortAdded   = "added"
	SortYear    = "year"
	SortRelease = "release"
	SortOrder   = "order" // season, disc and track or episode number: the default under a parent
)

// ItemQuery says which items to list. Library, Parent, Person, Tag and Collection are public ids. At
// least one of them, or one of the state filters, must be set.
type ItemQuery struct {
	Library    string
	Parent     string
	Person     string
	Tag        string
	Collection string // a library's collection: the titles it holds
	Genre      string // a genre by name, as people type it
	Kinds      []ItemKind
	YearFrom   int
	YearTo     int
	Watched    *bool
	Favorite   *bool
	InList     *bool
	Resolution string // "sd", "720p", "1080p" or "4k"
	Sort       string
	Desc       bool
	Cursor     string
	// At opens the list at a position instead of a cursor: 0 is the first item. It is how a
	// screen jumps to wherever a scroll bar or a letter points.
	At        int
	Limit     int
	WithTotal bool
}

// ItemPage is one page of a list. NextCursor is empty at the end. Total is the whole list's
// size, counted when WithTotal was set.
type ItemPage struct {
	Items      []Item
	NextCursor string
	Total      int
}

const (
	defaultPageSize = 50
	maxPageSize     = 200
)

// sortSpec is the columns a sort orders by, before the id that makes every position unique.
// When nullable, rows without a value come last in either direction, as a second run.
type sortSpec struct {
	columns  []string
	nullable bool
}

var sortSpecs = map[string]sortSpec{
	SortTitle:   {columns: []string{"i.sort_title"}},
	SortAdded:   {columns: []string{"i.added_at"}},
	SortYear:    {columns: []string{"i.year"}, nullable: true},
	SortRelease: {columns: []string{"i.release_date"}, nullable: true},
	SortOrder:   {columns: []string{"ifnull(i.parent_index, 0)", "ifnull(i.index_number, 0)", "i.sort_title"}},
}

// listCursor is where the next page starts: the sort it belongs to and the position of the
// last row sent. Clients treat the encoded form as opaque.
type listCursor struct {
	Sort   string `json:"s"`
	Desc   bool   `json:"d,omitempty"`
	Nulls  bool   `json:"n,omitempty"` // the run of rows without a sort value has begun
	Values []any  `json:"v,omitempty"`
	ID     int64  `json:"i"`
}

func (c listCursor) encode() string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeCursor(text, sort string, desc bool, columns int) (*listCursor, error) {
	if text == "" {
		return nil, nil
	}
	var c listCursor
	raw, err := base64.RawURLEncoding.DecodeString(text)
	if err == nil {
		// Numbers stay whole: a time in milliseconds must not pass through a float.
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		err = decoder.Decode(&c)
	}
	for n, value := range c.Values {
		if number, ok := value.(json.Number); ok {
			if c.Values[n], err = number.Int64(); err != nil {
				break
			}
		}
	}
	wantValues := columns
	if c.Nulls {
		wantValues = 0
	}
	if err != nil || c.Sort != sort || c.Desc != desc || len(c.Values) != wantValues {
		return nil, invalid("the cursor does not belong to this list")
	}
	return &c, nil
}

const itemColumns = `i.id, i.public_id, i.kind, i.title, i.sort_title, ifnull(i.year, 0), ifnull(i.release_date, ''), i.added_at,
	ifnull(i.duration_ms, 0), i.index_number, i.parent_index, i.child_count, ifnull(i.content_rating, ''),
	ifnull(i.community_rating, 0), ifnull(i.parent_id, 0), ifnull(i.root_id, 0),
	ifnull((SELECT public_id FROM items WHERE id = i.parent_id), ''), ifnull((SELECT public_id FROM items WHERE id = i.root_id), ''),
	(SELECT public_id FROM libraries WHERE id = i.library_id), ` + stateColumns

const stateJoin = ` LEFT JOIN user_item_state s ON s.profile_id = @viewer_profile AND s.item_id = i.id WHERE `

const itemFrom = ` FROM items i` + stateJoin

// dest is where a row of itemColumns scans to.
func (it *Item) dest() []any {
	return append([]any{&it.ID, &it.PublicID, &it.Kind, &it.Title, &it.sortTitle, &it.Year, &it.ReleaseDate, &it.AddedAt,
		&it.DurationMS, &it.IndexNumber, &it.ParentIndex, &it.ChildCount, &it.ContentRating, &it.CommunityRating,
		&it.ParentID, &it.RootID, &it.ParentPublicID, &it.RootPublicID, &it.LibraryID}, it.State.dest()...)
}

func scanItem(row interface{ Scan(...any) error }, it *Item, extra ...any) error {
	return row.Scan(append(it.dest(), extra...)...)
}

// Items lists items. The viewer's restrictions, every filter and the page position are all part
// of the one statement, and each page starts after the last row of the page before, so a deep
// page costs the same as the first.
func (d *DB) Items(ctx context.Context, v Viewer, q ItemQuery) (ItemPage, error) {
	var page ItemPage
	if q.Sort == "" {
		q.Sort = SortTitle
		if q.Parent != "" {
			q.Sort = SortOrder
		}
	}
	spec, ok := sortSpecs[q.Sort]
	if !ok {
		return page, invalid("unknown sort %q", q.Sort)
	}
	if q.Sort == SortOrder && q.Parent == "" {
		// Season and episode numbers order one show's items. Over a whole library they have
		// no index and mean nothing, so the list would be sorted afresh for every page.
		return page, invalid("the order sort is for the items under a parent")
	}
	if q.Limit <= 0 {
		q.Limit = defaultPageSize
	}
	q.Limit = min(q.Limit, maxPageSize)
	cursor, err := decodeCursor(q.Cursor, q.Sort, q.Desc, len(spec.columns))
	if err != nil {
		return page, err
	}
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return page, err
	}
	where := newQuery(v)
	if found, err := q.filters(ctx, tx, where, acc); err != nil || !found {
		return page, err
	}

	tail := false
	if q.At > 0 {
		if cursor != nil {
			return page, invalid("open a list at a position or at a cursor, not both")
		}
		var past bool
		if cursor, tail, past, err = positionCursor(ctx, tx, where, spec, q); err != nil {
			return page, err
		}
		if past {
			// A position past the end is an empty page, with the total if it was asked for.
			return page, listTotal(ctx, tx, where, q, &page)
		}
	}
	// Rows with a sort value first, then the rows without one.
	nulls := tail || cursor != nil && cursor.Nulls
	if !nulls {
		if err := listRun(ctx, tx, where, spec, q, cursor, false, &page); err != nil {
			return page, err
		}
		cursor = nil
	}
	if spec.nullable && len(page.Items) <= q.Limit {
		if err := listRun(ctx, tx, where, spec, q, cursor, true, &page); err != nil {
			return page, err
		}
	}
	if len(page.Items) > q.Limit {
		page.Items = page.Items[:q.Limit]
		last := page.Items[q.Limit-1]
		next := listCursor{Sort: q.Sort, Desc: q.Desc, ID: last.ID}
		if values := last.sortValues(q.Sort); values[0] == nil {
			next.Nulls = true
		} else {
			next.Values = values
		}
		page.NextCursor = next.encode()
	}
	if err := listTotal(ctx, tx, where, q, &page); err != nil {
		return page, err
	}
	if err := loadImages(ctx, tx, page.Items); err != nil {
		return page, err
	}
	return page, loadLeafCounts(ctx, tx, v, acc, page.Items)
}

// listTotal counts the whole list when the request asked for its total.
func listTotal(ctx context.Context, tx *sql.Tx, where *query, q ItemQuery, page *ItemPage) error {
	if !q.WithTotal {
		return nil
	}
	return tx.QueryRowContext(ctx, `SELECT count(*) FROM `+where.countFrom+` WHERE `+where.countWhere(), where.args...).Scan(&page.Total)
}

// positionCursor finds the item just before position q.At and returns it as a cursor, so the
// page that follows is read exactly as any later page is. It reads only the sort's own columns
// and skips through the list's index without fetching a row it does not send, which is what
// lets a list of a million open anywhere in one step. tail reports that the position is the
// first of the rows with no sort value, which has no item before it in that run; past reports
// a position beyond the end.
func positionCursor(ctx context.Context, tx *sql.Tx, where *query, spec sortSpec, q ItemQuery) (cursor *listCursor, tail, past bool, err error) {
	direction := ""
	if q.Desc {
		direction = " DESC"
	}
	find := func(nulls bool, skip int) (*listCursor, error) {
		columns, with := append(append([]string(nil), spec.columns...), "i.id"), ""
		if nulls {
			columns, with = []string{"i.id"}, ` AND `+spec.columns[0]+` IS NULL`
		} else if spec.nullable {
			with = ` AND ` + spec.columns[0] + ` IS NOT NULL`
		}
		values := make([]any, len(columns))
		dest := make([]any, len(columns))
		for n := range values {
			dest[n] = &values[n]
		}
		err := tx.QueryRowContext(ctx, `SELECT `+strings.Join(columns, ", ")+` FROM `+where.from+` WHERE `+where.pageWhere()+with+
			` ORDER BY `+strings.Join(columns, direction+", ")+direction+` LIMIT 1 OFFSET `+fmt.Sprint(skip), where.args...).Scan(dest...)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		found := &listCursor{Sort: q.Sort, Desc: q.Desc, Nulls: nulls, ID: values[len(values)-1].(int64)}
		if !nulls {
			found.Values = values[:len(values)-1]
			for n, value := range found.Values {
				if text, ok := value.([]byte); ok {
					found.Values[n] = string(text)
				}
			}
		}
		return found, nil
	}
	if cursor, err = find(false, q.At-1); cursor != nil || err != nil || !spec.nullable {
		return cursor, false, cursor == nil, err
	}
	// The position is among the rows with no sort value, which come last: count the rows
	// that have one and skip the rest of the way there.
	var valued int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM `+where.from+` WHERE `+where.pageWhere()+` AND `+spec.columns[0]+` IS NOT NULL`,
		where.args...).Scan(&valued); err != nil {
		return nil, false, false, err
	}
	if q.At == valued {
		return nil, true, false, nil
	}
	cursor, err = find(true, q.At-valued-1)
	return cursor, false, cursor == nil, err
}

// listRun reads one run of a page: the rows that have a sort value, or the rows that do not.
func listRun(ctx context.Context, tx *sql.Tx, where *query, spec sortSpec, q ItemQuery, after *listCursor, nulls bool, page *ItemPage) error {
	run := &query{args: append([]any(nil), where.args...)}
	run.add(`SELECT `, itemColumns, ` FROM `, where.from, stateJoin, where.pageWhere())
	columns := append(append([]string(nil), spec.columns...), "i.id")
	if nulls {
		run.add(` AND `, spec.columns[0], ` IS NULL`)
		columns = []string{"i.id"}
	} else if spec.nullable {
		run.add(` AND `, spec.columns[0], ` IS NOT NULL`)
	}
	direction, compare := "", ">"
	if q.Desc {
		direction, compare = " DESC", "<"
	}
	if after != nil {
		names := make([]string, 0, len(columns))
		for _, value := range after.Values {
			names = append(names, run.arg(value))
		}
		names = append(names, run.arg(after.ID))
		run.add(` AND (`, strings.Join(columns, ", "), `) `, compare, ` (`, strings.Join(names, ", "), `)`)
	}
	run.add(` ORDER BY `, strings.Join(columns, direction+", "), direction, ` LIMIT `, fmt.Sprint(q.Limit+1-len(page.Items)))
	rows, err := tx.QueryContext(ctx, run.sql.String(), run.args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var it Item
		if err := scanItem(rows, &it); err != nil {
			return err
		}
		page.Items = append(page.Items, it)
	}
	return rows.Err()
}

// sortValues is an item's position in a sort, as the cursor carries it.
func (it *Item) sortValues(sort string) []any {
	switch sort {
	case SortAdded:
		return []any{it.AddedAt}
	case SortYear:
		if it.Year == 0 {
			return []any{nil}
		}
		return []any{it.Year}
	case SortRelease:
		if it.ReleaseDate == "" {
			return []any{nil}
		}
		return []any{it.ReleaseDate}
	case SortOrder:
		var parent, index int
		if it.ParentIndex != nil {
			parent = *it.ParentIndex
		}
		if it.IndexNumber != nil {
			index = *it.IndexNumber
		}
		return []any{parent, index, it.sortTitle}
	}
	return []any{it.sortTitle}
}

// rareTag is how many uses make a tag common. A list filtered by a rarer tag starts from the
// tag's items and sorts them; a list filtered by a common tag reads the library in order and
// skips what does not match. Each is fast where the other is slow.
var rareTag = 2000

// filters writes the condition for everything but the page position: what the viewer may see,
// and what was asked for. It reports false when a named tag does not exist, so nothing matches.
func (q ItemQuery) filters(ctx context.Context, tx *sql.Tx, w *query, acc access) (bool, error) {
	w.add(acc.visible("i"))
	scoped := false
	if q.Library != "" {
		// The library is looked up first, so a library the viewer may not see is an empty
		// list at once instead of a walk through every row of it.
		var library int64
		err := tx.QueryRowContext(ctx, `SELECT l.id FROM libraries l WHERE l.public_id = ? AND `+acc.libraryVisible("l.id"), q.Library).Scan(&library)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		w.add(` AND i.library_id = `, fmt.Sprint(library))
		scoped = true
	}
	if q.Parent != "" {
		w.add(` AND i.parent_id = (SELECT id FROM items WHERE public_id = `, w.arg(q.Parent), `)`)
		scoped = true
	}
	if len(q.Kinds) > 0 {
		kinds := make([]string, len(q.Kinds))
		for n, kind := range q.Kinds {
			if err := checkCode("kind", itemKindNames, kind); err != nil {
				return false, invalid("unknown kind")
			}
			kinds[n] = fmt.Sprint(int(kind))
		}
		if len(kinds) == 1 {
			w.add(` AND i.kind = `, kinds[0])
		} else {
			w.add(` AND i.kind IN (`, strings.Join(kinds, ", "), `)`)
		}
	}
	if q.Person != "" {
		w.add(` AND i.id IN (SELECT item_id FROM credits WHERE person_id = (SELECT id FROM people WHERE public_id = `, w.arg(q.Person), `))`)
		scoped = true
	}
	if q.Collection != "" {
		// A collection the viewer may not see is not found, as it is when opened on its own.
		var collection int64
		err := tx.QueryRowContext(ctx, `SELECT i.id FROM items i WHERE i.public_id = ? AND i.kind = `+itoa(int64(KindCollection))+` AND `+
			acc.visible("i"), q.Collection).Scan(&collection)
		if errors.Is(err, sql.ErrNoRows) {
			return false, ErrNotFound
		}
		if err != nil {
			return false, err
		}
		w.add(` AND i.id IN (SELECT related_item_id FROM item_relations WHERE kind = `, itoa(int64(RelationCollectionMember)),
			` AND item_id = `, itoa(collection), `)`)
		scoped = true
	}
	if q.Tag != "" || q.Genre != "" {
		var tag int64
		var uses int
		err := tx.QueryRowContext(ctx, `SELECT id, (SELECT count(*) FROM (SELECT 1 FROM item_tags WHERE tag_id = tags.id LIMIT ?))
			FROM tags WHERE public_id = ? OR (? = '' AND kind = 'genre' AND norm_name = ?)`, rareTag, q.Tag, q.Tag, normTag(q.Genre)).Scan(&tag, &uses)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		// An item matches a tag it carries, or one the title it belongs to carries: an episode
		// is a drama because its show is. A common tag in a library is read in the list's
		// own order, skipping what does not match. Otherwise the tag's items are gathered
		// first, and the total always is: it costs what matches, not the whole library.
		walk := fmt.Sprintf(` AND EXISTS (SELECT 1 FROM item_tags t WHERE t.tag_id = %d AND t.item_id IN (i.id, i.root_id))`, tag)
		gathered := fmt.Sprintf(` AND i.id IN (SELECT item_id FROM item_tags WHERE tag_id = %[1]d UNION
			SELECT d.id FROM item_tags h CROSS JOIN items d WHERE h.tag_id = %[1]d AND d.root_id = h.item_id)`, tag)
		w.add("{tag}")
		w.countFrom, w.tagCount = "items i NOT INDEXED", gathered
		if scoped && uses >= rareTag {
			w.tagPage = walk
		} else {
			w.from, w.tagPage = "items i NOT INDEXED", gathered
		}
		scoped = true
	}
	// A year filter narrows the total through the year index. The page is read in the list's
	// own order and skips other years, which finds a page's worth sooner than gathering
	// every title of those years and sorting them. "{+}" marks the difference.
	if q.YearFrom != 0 {
		w.add(` AND {+}i.year >= `, w.arg(q.YearFrom))
	}
	if q.YearTo != 0 {
		w.add(` AND {+}i.year <= `, w.arg(q.YearTo))
	}
	if q.Sort != SortYear {
		w.pagePlus = "+"
	}
	for _, flag := range []struct {
		want   *bool
		column string
	}{{q.Favorite, "favorite"}, {q.InList, "in_list"}} {
		if flag.want == nil {
			continue
		}
		not := ""
		if !*flag.want {
			not = " NOT"
		} else {
			scoped = true
		}
		w.add(` AND i.id`, not, ` IN (SELECT item_id FROM user_item_state WHERE profile_id = @viewer_profile AND `, flag.column, ` = 1)`)
	}
	if q.Watched != nil {
		// A playable item is watched when its own state says so. An item that holds others is
		// watched when nothing the viewer may see under it is left unwatched.
		unwatched := underHolder(`EXISTS (SELECT 1 FROM items c WHERE {under} AND `+acc.visible("c")+` AND NOT EXISTS (
			SELECT 1 FROM user_item_state w WHERE w.profile_id = @viewer_profile AND w.item_id = c.id AND w.played = 1))`, "i.id", " OR ")
		w.add(` AND CASE WHEN i.kind IN `, playableKindsSQL, ` THEN `, boolSQL(*q.Watched), ` = EXISTS (
			SELECT 1 FROM user_item_state w WHERE w.profile_id = @viewer_profile AND w.item_id = i.id AND w.played = 1)
			ELSE `, boolSQL(*q.Watched), ` = (i.leaf_count > 0 AND NOT (`, unwatched, `)) END`)
	}
	if q.Resolution != "" {
		class, ok := resolutionClasses[q.Resolution]
		if !ok {
			return false, invalid("unknown resolution %q", q.Resolution)
		}
		w.add(` AND EXISTS (SELECT 1 FROM media m WHERE m.item_id = i.id AND m.width IS NOT NULL AND `, resolutionSQL, ` = `, fmt.Sprint(class), `)`)
	}
	if !scoped {
		return false, invalid("a list needs a library, a parent, a person, a tag, favourites or My List")
	}
	return true, nil
}

func boolSQL(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// A version's resolution class goes by width or height, so a wide 1920x800 film is still 1080p.
var resolutionClasses = map[string]int{"sd": 1, "720p": 2, "1080p": 3, "4k": 4}

const resolutionSQL = `CASE WHEN m.width >= 3800 OR m.height >= 2000 THEN 4 WHEN m.width >= 1900 OR m.height >= 1000 THEN 3
	WHEN m.width >= 1200 OR m.height >= 700 THEN 2 ELSE 1 END`

// underHolder writes a query over the playable items under a holder. Titles nest at most
// three deep, so a holder is the root or the parent of its leaves, and each is found through
// its own index; asking for "root or parent" in one condition would read the whole table.
// body is a SELECT over items c with "{under}" where that condition goes, and holder is the
// SQL expression for the holder's id. The two results are joined by joiner: " UNION " to list
// each leaf once, " OR " between two EXISTS.
func underHolder(body, holder, joiner string) string {
	root := strings.ReplaceAll(body, "{under}", "c.root_id = "+holder+" AND c.kind IN "+playableKindsSQL)
	parent := strings.ReplaceAll(body, "{under}", "c.parent_id = "+holder+" AND c.kind IN "+playableKindsSQL)
	return root + joiner + parent
}

// idList is the ids of some items as an SQL list.
func idList(items []Item) string {
	ids := make([]string, len(items))
	for n := range items {
		ids[n] = fmt.Sprint(items[n].ID)
	}
	return "(" + strings.Join(ids, ", ") + ")"
}

// loadImages attaches each item's chosen pictures.
func loadImages(ctx context.Context, tx *sql.Tx, items []Item) error {
	if len(items) == 0 {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT item_id, id, role, ifnull(file_hash, ''), ifnull(width, 0), ifnull(height, 0),
		ifnull(placeholder, '') FROM images WHERE item_id IN `+idList(items)+` AND selected = 1 ORDER BY item_id, role`)
	if err != nil {
		return err
	}
	defer rows.Close()
	byID := map[int64]*Item{}
	for n := range items {
		byID[items[n].ID] = &items[n]
	}
	for rows.Next() {
		var itemID int64
		var image Image
		if err := rows.Scan(&itemID, &image.ID, &image.Role, &image.FileHash, &image.Width, &image.Height, &image.Placeholder); err != nil {
			return err
		}
		byID[itemID].Images = append(byID[itemID].Images, image)
	}
	return rows.Err()
}

// loadLeafCounts fills Leaves and WatchedLeaves for the items that hold others, and from them
// whether the viewer has watched the whole of each.
func loadLeafCounts(ctx context.Context, tx *sql.Tx, v Viewer, acc access, items []Item) error {
	var holders []Item
	byID := map[int64]*Item{}
	for n := range items {
		if !items[n].Kind.Playable() && items[n].ChildCount > 0 {
			holders = append(holders, items[n])
			byID[items[n].ID] = &items[n]
		}
	}
	if len(holders) == 0 {
		return nil
	}
	// Titles nest at most three deep, so a holder is the root or the parent of its leaves.
	for _, column := range []string{"c.root_id", "c.parent_id"} {
		rows, err := tx.QueryContext(ctx, `SELECT `+column+`, count(*), count(w.item_id) FROM items c
			LEFT JOIN user_item_state w ON w.profile_id = @viewer_profile AND w.item_id = c.id AND w.played = 1
			WHERE `+column+` IN `+idList(holders)+` AND c.kind IN `+playableKindsSQL+` AND `+acc.visible("c")+` GROUP BY 1`, v.args()...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id int64
			var leaves, watched int
			if err := rows.Scan(&id, &leaves, &watched); err != nil {
				rows.Close()
				return err
			}
			// Set, not added: an album with no artist above it is found both ways.
			byID[id].Leaves, byID[id].WatchedLeaves = leaves, watched
			byID[id].State.Watched = leaves > 0 && watched == leaves
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	return nil
}
