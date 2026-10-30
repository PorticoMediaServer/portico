package catalog

import (
	"database/sql"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// Page selection. A browse page is a run of the pivot's order, reached one of
// three ways, and none of them walks the rows before it:
//
//   - a cursor seeks to the key its last row carried (a keyset);
//   - a position starts at the counted block whose running total passes it
//     (catalog_browse_blocks, or compact_visibility_blocks for a restricted
//     viewer's class) and walks under 2,048 rows;
//   - a profile sort (browse_personal.go) takes the profile's valued rows from
//     its own history and the rest by title position.
//
// Every order is (key, entity_id) with the tie-break in the key's direction,
// so a descending page is the ascending run at the mirrored position, read
// backwards, and one block set serves both directions.
//
// A posted query has no position blocks: continuation pages seek to their
// saved keys. A direct filtered range selects narrow identities with a bounded
// limit before hydration; its total is carried by its cursor.

// browseRowColumns selects a browseRow over e (catalog_browse_rows) and ce.
var browseRowColumns = `pid(ce.public_id),e.entity_id,` + compactBrowseKindSQL("e.kind") + `,e.title,e.year,COALESCE(e.added_text,''),e.sort_key,COALESCE(e.duration_max,0),COALESCE(e.rating_max,0)`

func scanBrowseRows(rows *sql.Rows) ([]browseRow, error) {
	defer rows.Close()
	out := []browseRow{}
	for rows.Next() {
		var row browseRow
		if err := rows.Scan(&row.id, &row.entity, &row.kind, &row.title, &row.year, &row.added, &row.sortKey, &row.duration, &row.rating); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// pageShape is everything browseSelect resolved that choosing a page needs.
type pageShape struct {
	reading bool
	request BrowseRequest
	sorts   []BrowseSortSelection
	where   string
	args    []any
	// unrestricted is where without the viewer's restriction: a published
	// visibility class already selected those rows.
	unrestricted     string
	unrestrictedArgs []any
	// live is the viewer's restriction alone, over e.
	live       string
	liveArgs   []any
	order      string
	orderArgs  []any
	start      int
	limit      int
	total      int
	kind       int
	summary    browseSummary
	seekKeys   []string
	seekAnchor string
	// group is the group's own order as (id, pos) SQL, for groupOrder.
	group     string
	groupArgs []any
	// revision is the browse revision, which keys the result's cached rail
	// and sparse range anchors.
	revision string
}

func (p pageShape) primary() (browseSort, bool) {
	definition, ok := browseSortByID(p.sorts[0].Field)
	return definition, ok && p.sorts[0].Direction == "desc"
}

// blockScope names one block set: the table, its scope predicate and args.
type blockScope struct {
	table string
	where string
	args  []any
}

func (p pageShape) blocks() blockScope {
	if p.summary.classKey != "" {
		return blockScope{"compact_visibility_blocks", `class_id=? AND generation=? AND library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND kind=?`,
			[]any{p.summary.classID, p.summary.generation, p.request.Library, p.kind}}
	}
	return blockScope{"catalog_browse_blocks", `library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND kind=?`, []any{p.request.Library, p.kind}}
}

// positional reports whether positions in this shape come from counted blocks:
// no posted query, one sort, one kind, and either no restriction or a
// published class for it.
func (p pageShape) positional() bool {
	if p.reading || p.request.Query != nil || len(p.sorts) != 1 || p.kind == 0 {
		return false
	}
	return p.summary.classKey != "" || !p.request.Restrictions.Active()
}

// browsePage selects the page's rows.
func (s *Service) browsePage(p pageShape) ([]browseRow, error) {
	if p.summary.exact && p.total == 0 {
		return []browseRow{}, nil
	}
	primary, desc := p.primary()
	if primary.Ordering == -3 {
		return s.groupOrderPage(p)
	}
	if (p.reading || p.request.Query != nil) && p.start > 0 && len(p.seekKeys) == 0 {
		return s.filteredRangePage(p)
	}
	if primary.Ordering == -2 {
		return s.indexedFactPage(p, primary, desc)
	}
	if primary.Profile && primary.ID != "forYou" {
		return s.indexedPersonalPage(p, primary, desc)
	}
	if len(p.seekKeys) > 0 && browseKeyedSorts(p.sorts) {
		if typed, ok := browseKeyValues(p.sorts, p.seekKeys); ok {
			if anchor, err := strconv.ParseInt(p.seekAnchor, 10, 64); err == nil {
				if p.positional() && p.summary.classKey != "" {
					return s.classKeysetPage(p, primary, desc, typed[0], anchor)
				}
				definition, desc := p.primary()
				query, args := browseBoundedSQL(browseRowColumns, "catalog_browse_rows e JOIN catalog_entities ce ON ce.id=e.entity_id", p.where, p.args, definition.Expression, "e.entity_id", desc, false, typed[0], anchor, p.limit, 0)
				return s.queryBrowseRows(query, args...)
			}
		}
	}
	// An expression sort that is not the profile's (Aired) has no counted ordering: it takes the ordered query below.
	if p.positional() && (primary.Profile || primary.Ordering >= 0) {
		if primary.Profile {
			return s.personalPage(p, primary, desc)
		}
		return s.positionPage(p, primary.Ordering, desc, p.start, p.limit)
	}
	if primary.Profile && len(p.sorts) == 1 {
		return s.personalPage(p, primary, desc)
	}
	if p.start > 0 && len(p.seekKeys) == 0 {
		if p.request.Restrictions.Active() && p.summary.classKey == "" && !primary.Profile {
			return s.filteredRangePage(p)
		}
		return s.walkIndexedRange(p, func(p pageShape) ([]browseRow, error) { return s.browsePage(p) })
	}
	args := append(append(append([]any{}, p.args...), p.orderArgs...), p.limit)
	return s.queryBrowseRows(`SELECT `+browseRowColumns+` FROM catalog_browse_rows e JOIN catalog_entities ce ON ce.id=e.entity_id WHERE `+p.where+` ORDER BY `+p.order+` LIMIT ?`, args...)
}

func (s *Service) queryBrowseRows(query string, args ...any) ([]browseRow, error) {
	if err := s.reserveReadingCountRows(query, args); err != nil {
		return nil, err
	}
	rows, err := s.read().Query(query, args...)
	if err != nil {
		return nil, err
	}
	return scanBrowseRows(rows)
}

// positionPage reads limit rows from position start of one block ordering, in
// either direction.
func (s *Service) positionPage(p pageShape, ordering int, desc bool, start, limit int) ([]browseRow, error) {
	low, count := start, limit
	if desc {
		low = max(0, p.total-start-limit)
		count = p.total - start - low
	}
	if count <= 0 || low >= p.total {
		return []browseRow{}, nil
	}
	rows, err := s.ascendingFrom(p, ordering, low, count)
	if err != nil || !desc {
		return rows, err
	}
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	return rows, nil
}

// ascendingFrom reads count rows of an ordering from ascending position low:
// the block whose running total passes low, then a walk inside it.
func (s *Service) ascendingFrom(p pageShape, ordering, low, count int) ([]browseRow, error) {
	scope := p.blocks()
	value, entity, before, err := s.blockAt(scope, ordering, low)
	if errors.Is(err, sql.ErrNoRows) {
		return []browseRow{}, nil
	}
	if err != nil {
		return nil, err
	}
	definition := sortForOrdering(ordering)
	if p.summary.classKey != "" {
		return s.classPositionRows(p, definition, value, entity, count, low-before)
	}
	query, args := browseBoundedSQL(browseRowColumns, "catalog_browse_rows e JOIN catalog_entities ce ON ce.id=e.entity_id", p.where, p.args, definition.Expression, "e.entity_id", false, true, value, entity, count, low-before)
	return s.queryBrowseRows(query, args...)
}

// blockAt finds the block holding ascending position position: its first
// row's key and entity, and how many rows come before it. The running total
// reads the scope's blocks, about one per 1,500 rows.
func (s *Service) blockAt(scope blockScope, ordering, position int) (any, int64, int, error) {
	var value any
	var entity int64
	var before int
	args := append(append([]any{}, scope.args...), ordering, position)
	err := s.read().QueryRow(`SELECT sort_value,entity_id,before FROM (
	 SELECT sort_value,entity_id,sum(total) OVER w-total AS before,sum(total) OVER w AS through FROM `+scope.table+`
	 WHERE `+scope.where+` AND ordering=? WINDOW w AS (ORDER BY sort_value,entity_id ROWS UNBOUNDED PRECEDING))
	 WHERE through>? ORDER BY sort_value,entity_id LIMIT 1`, args...).Scan(&value, &entity, &before)
	if b, ok := value.([]byte); ok {
		value = string(b)
	}
	return value, entity, before, err
}

// blockRank is an entity's ascending position in one ordering of a block set:
// the rows before its block plus those before it inside the block. inside
// counts rows of the scope between two keys, given as SQL over the rows.
func (s *Service) blockRank(scope blockScope, ordering int, value any, entity int64, inside func(fromValue any, fromEntity int64) (int, error)) (int, error) {
	var start any
	var startEntity int64
	var before int
	args := append(append([]any{}, scope.args...), ordering, value, value, entity)
	err := s.read().QueryRow(`SELECT sort_value,entity_id,before FROM (
	 SELECT sort_value,entity_id,sum(total) OVER w-total AS before FROM `+scope.table+`
	 WHERE `+scope.where+` AND ordering=? WINDOW w AS (ORDER BY sort_value,entity_id ROWS UNBOUNDED PRECEDING))
	 WHERE sort_value<? OR sort_value=? AND entity_id<=? ORDER BY sort_value DESC,entity_id DESC LIMIT 1`, args...).Scan(&start, &startEntity, &before)
	if errors.Is(err, sql.ErrNoRows) {
		return inside(nil, 0)
	}
	if err != nil {
		return 0, err
	}
	if b, ok := start.([]byte); ok {
		start = string(b)
	}
	within, err := inside(start, startEntity)
	return before + within, err
}

func sortForOrdering(ordering int) browseSort {
	if ordering == compactcatalog.OrderRandom {
		d, _ := browseSortByID("random")
		return d
	}
	for _, definition := range browseSorts {
		if (definition.Ordering == ordering || definition.Blocks == ordering && ordering > compactcatalog.OrderRandom) && !definition.Profile {
			return definition
		}
	}
	return browseSorts[0]
}

// classKeysetPage continues a restricted viewer's page after its cursor's key.
func (s *Service) classKeysetPage(p pageShape, definition browseSort, desc bool, key any, anchor int64) ([]browseRow, error) {
	return s.classBoundedRows(p, definition, desc, false, key, anchor, p.limit, 0)
}

func (s *Service) classBoundedRows(p pageShape, definition browseSort, desc, inclusive bool, key any, anchor int64, limit, offset int) ([]browseRow, error) {
	// The class is the restricted viewer's row set and positions. The live
	// restriction is checked on the selected rows only: for a container it
	// walks the members, and testing every row skipped inside a block cost
	// as much as the block. A row restricted since the class published is
	// left out (classPositionRows refills a positional page).
	args := []any{p.summary.classID, p.summary.generation, p.request.Library, p.kind}
	where := `v.class_id=? AND v.generation=? AND v.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND v.kind=?`
	from := `compact_visibility_rows v CROSS JOIN catalog_browse_rows e ON e.entity_id=v.entity_id CROSS JOIN catalog_entities ce ON ce.id=e.entity_id`
	query, args := browseBoundedSQL(browseRowColumns, from, where, args, definition.ClassKey, "v.entity_id", desc, inclusive, key, anchor, limit, offset)
	live, liveArgs := EntityRestrictionSQL("selected.entity_id", p.request.Restrictions)
	if live != "1" {
		query = `SELECT * FROM (` + query + `) selected WHERE ` + live
		args = append(args, liveArgs...)
	}
	return s.queryBrowseRows(query, args...)
}

// classPositionRows reads count rows of a class ordering from inside a block.
// The class holds them all, so a short read means newer restrictions dropped
// rows; the page is refilled from the rows after it.
func (s *Service) classPositionRows(p pageShape, definition browseSort, key any, anchor int64, count, offset int) ([]browseRow, error) {
	rows, err := s.classBoundedRows(p, definition, false, true, key, anchor, count, offset)
	for want := count * 2; err == nil && len(rows) < count && want <= count*16; want *= 2 {
		rows, err = s.classBoundedRows(p, definition, false, true, key, anchor, want, offset)
	}
	if len(rows) > count {
		rows = rows[:count]
	}
	return rows, err
}

// asciiLower folds ASCII letters only, as SQLite's lower() and NOCASE do, so a
// key compared in Go orders as the blocks and indexes order it.
func asciiLower(value string) string {
	if strings.IndexFunc(value, func(r rune) bool { return r >= 'A' && r <= 'Z' }) < 0 {
		return value
	}
	b := []byte(value)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// titleKey is a row's place in title order.
type titleKey struct {
	key    string
	entity int64
}

func (a titleKey) less(b titleKey) bool {
	return a.key < b.key || a.key == b.key && a.entity < b.entity
}

func sortTitleKeys(keys []titleKey) {
	sort.Slice(keys, func(i, j int) bool { return keys[i].less(keys[j]) })
}

// browseAnchorRank is an entity's position in the page's order: its rank in
// the block ordering, mirrored for a descending sort. An entity outside the
// scope is not found.
func (s *Service) browseAnchorRank(p pageShape, anchor string) (int, bool, error) {
	definition, _ := p.primary()
	return s.browseAnchorRankIn(p, anchor, definition.Ordering)
}

// browseAnchorRankIn is browseAnchorRank in one block ordering: a column
// order's, or a fact order's own (definition.Blocks) for an unrestricted
// viewer.
func (s *Service) browseAnchorRankIn(p pageShape, anchor string, ordering int) (int, bool, error) {
	definition, desc := p.primary()
	class := p.summary.classKey != ""
	var value any
	var entity int64
	var err error
	if class {
		err = s.read().QueryRow(`SELECT `+compactcatalog.VisibilityOrderKeys[ordering]+`,v.entity_id FROM compact_visibility_rows v
		 WHERE v.class_id=? AND v.generation=? AND v.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND v.kind=?
		 AND v.entity_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`,
			p.summary.classID, p.summary.generation, p.request.Library, p.kind, anchor).Scan(&value, &entity)
	} else {
		err = s.read().QueryRow(`SELECT `+compactcatalog.BlockOrderKeys[ordering]+`,e.entity_id FROM catalog_browse_rows e WHERE `+p.where+
			` AND e.entity_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, append(append([]any{}, p.args...), anchor)...).Scan(&value, &entity)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if b, ok := value.([]byte); ok {
		value = string(b)
	}
	// inside counts the scope's rows from a block's first row up to the anchor.
	inside := func(fromValue any, fromEntity int64) (int, error) {
		key, id, from, args := definition.Expression, "e.entity_id", `catalog_browse_rows e WHERE `+p.where, append([]any{}, p.args...)
		if class {
			key, id = definition.ClassKey, "v.entity_id"
			from = `compact_visibility_rows v WHERE v.class_id=? AND v.generation=? AND v.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND v.kind=?`
			args = []any{p.summary.classID, p.summary.generation, p.request.Library, p.kind}
		}
		bounds := ` AND (` + key + `<? OR ` + key + `=? AND ` + id + `<?)`
		args = append(args, value, value, entity)
		if fromValue != nil {
			bounds += ` AND (` + key + `>? OR ` + key + `=? AND ` + id + `>=?)`
			args = append(args, fromValue, fromValue, fromEntity)
		}
		var count int
		err := s.read().QueryRow(`SELECT count(*) FROM `+from+bounds, args...).Scan(&count)
		return count, err
	}
	rank, err := s.blockRank(p.blocks(), ordering, value, entity, inside)
	if err != nil {
		return 0, false, err
	}
	if desc {
		rank = p.total - 1 - rank
	}
	return rank, true, nil
}
