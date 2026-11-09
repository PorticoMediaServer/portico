package db

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
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

// ItemQuery says which items to list. Library, Parent, Person and Tag are public ids. At
// least one of them, or one of the state filters, must be set.
type ItemQuery struct {
	Library    string
	Parent     string
	Person     string
	Tag        string
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
	Limit      int
	WithTotal  bool
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
	ifnull(i.community_rating, 0), ifnull(i.parent_id, 0), ifnull(i.root_id, 0), ` + stateColumns

const itemFrom = ` FROM items i LEFT JOIN user_item_state s ON s.profile_id = @viewer_profile AND s.item_id = i.id WHERE `

func scanItem(row interface{ Scan(...any) error }, it *Item, extra ...any) error {
	dest := append([]any{&it.ID, &it.PublicID, &it.Kind, &it.Title, &it.sortTitle, &it.Year, &it.ReleaseDate, &it.AddedAt,
		&it.DurationMS, &it.IndexNumber, &it.ParentIndex, &it.ChildCount, &it.ContentRating, &it.CommunityRating,
		&it.ParentID, &it.RootID}, it.State.dest()...)
	return row.Scan(append(dest, extra...)...)
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
	if q.Limit <= 0 {
		q.Limit = defaultPageSize
	}
	q.Limit = min(q.Limit, maxPageSize)
	cursor, err := decodeCursor(q.Cursor, q.Sort, q.Desc, len(spec.columns))
	if err != nil {
		return page, err
	}
	where := newQuery(v)
	if err := q.filters(where); err != nil {
		return page, err
	}
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return page, err
	}
	defer tx.Rollback()

	// Rows with a sort value first, then the rows without one.
	nulls := cursor != nil && cursor.Nulls
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
	if q.WithTotal {
		count := `SELECT count(*) FROM items i WHERE ` + where.sql.String()
		if err := tx.QueryRowContext(ctx, count, where.args...).Scan(&page.Total); err != nil {
			return page, err
		}
	}
	if err := loadImages(ctx, tx, page.Items); err != nil {
		return page, err
	}
	return page, loadLeafCounts(ctx, tx, v, page.Items)
}

// listRun reads one run of a page: the rows that have a sort value, or the rows that do not.
func listRun(ctx context.Context, tx *sql.Tx, where *query, spec sortSpec, q ItemQuery, after *listCursor, nulls bool, page *ItemPage) error {
	run := &query{args: append([]any(nil), where.args...)}
	run.add(`SELECT `, itemColumns, itemFrom, where.sql.String())
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

// filters writes the condition for everything but the page position: what the viewer may see,
// and what was asked for.
func (q ItemQuery) filters(w *query) error {
	w.add(visibleTo("i"))
	scoped := false
	if q.Library != "" {
		w.add(` AND i.library_id = (SELECT id FROM libraries WHERE public_id = `, w.arg(q.Library), `)`)
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
				return invalid("unknown kind")
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
	for _, tag := range []struct{ value, find string }{
		{q.Tag, `SELECT id FROM tags WHERE public_id = `},
		{normTag(q.Genre), `SELECT id FROM tags WHERE kind = 'genre' AND norm_name = `},
	} {
		if tag.value == "" {
			continue
		}
		if q.Library != "" || q.Parent != "" {
			// Inside a library the list is read in order, and an episode or a track matches
			// through the title it belongs to.
			w.add(` AND EXISTS (SELECT 1 FROM item_tags t WHERE t.tag_id = (`, tag.find, w.arg(tag.value), `) AND t.item_id IN (i.id, i.root_id))`)
		} else {
			w.add(` AND i.id IN (SELECT item_id FROM item_tags WHERE tag_id = (`, tag.find, w.arg(tag.value), `))`)
		}
		scoped = true
	}
	if q.YearFrom != 0 {
		w.add(` AND i.year >= `, w.arg(q.YearFrom))
	}
	if q.YearTo != 0 {
		w.add(` AND i.year <= `, w.arg(q.YearTo))
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
		w.add(` AND CASE WHEN i.kind IN `, playableKindsSQL, ` THEN `, boolSQL(*q.Watched), ` = EXISTS (
			SELECT 1 FROM user_item_state w WHERE w.profile_id = @viewer_profile AND w.item_id = i.id AND w.played = 1)
			ELSE `, boolSQL(*q.Watched), ` = (i.leaf_count > 0 AND NOT EXISTS (
			SELECT 1 FROM items c WHERE (c.root_id = i.id OR c.parent_id = i.id) AND c.kind IN `, playableKindsSQL, `
			AND `, visibleTo("c"), ` AND NOT EXISTS (
			SELECT 1 FROM user_item_state w WHERE w.profile_id = @viewer_profile AND w.item_id = c.id AND w.played = 1))) END`)
	}
	if q.Resolution != "" {
		class, ok := resolutionClasses[q.Resolution]
		if !ok {
			return invalid("unknown resolution %q", q.Resolution)
		}
		w.add(` AND EXISTS (SELECT 1 FROM media m WHERE m.item_id = i.id AND `, resolutionSQL, ` = `, fmt.Sprint(class), `)`)
	}
	if !scoped {
		return invalid("a list needs a library, a parent, a person, a tag, favourites or My List")
	}
	return nil
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
func loadLeafCounts(ctx context.Context, tx *sql.Tx, v Viewer, items []Item) error {
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
			WHERE `+column+` IN `+idList(holders)+` AND c.kind IN `+playableKindsSQL+` AND `+visibleTo("c")+` GROUP BY 1`, v.args()...)
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
