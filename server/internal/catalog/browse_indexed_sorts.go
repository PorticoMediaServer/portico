package catalog

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

// Exact keys travel with the selected rows so a changed or removed anchor does
// not change the meaning of a continuation. SQL selects only a bounded page.
func (s *Service) queryBrowseKeyRows(query, field string, args ...any) ([]browseRow, error) {
	if err := s.reserveReadingCountRows(query, args, 1); err != nil {
		return nil, err
	}
	rows, err := s.read().Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []browseRow{}
	for rows.Next() {
		var r browseRow
		var key any
		if err = rows.Scan(&r.id, &r.entity, &r.kind, &r.title, &r.year, &r.added, &r.sortKey, &r.duration, &r.rating, &key); err != nil {
			return nil, err
		}
		r.keys = map[string]string{field: browseKeyText(key)}
		out = append(out, r)
	}
	return out, rows.Err()
}

// browseKeyText is a selected sort key as a continuation stores it.
func browseKeyText(key any) string {
	if n, ok := key.(int64); ok {
		return strconv.FormatInt(n, 10)
	} else if n, ok := key.(float64); ok {
		return strconv.FormatFloat(n, 'g', -1, 64)
	}
	return formatOrderValue(key)
}

func indexedSortValue(field, key string) (any, error) {
	switch field {
	case "criticRating", "personalRating":
		return strconv.ParseFloat(key, 64)
	case "episodeCount", "trackNumber", "fileSize", "resolution", "random", "playCount":
		return strconv.ParseInt(key, 10, 64)
	}
	return key, nil
}

func (s *Service) indexedFactPage(p pageShape, definition browseSort, desc bool) ([]browseRow, error) {
	if definition.ID == "random" && p.positional() && len(p.seekKeys) == 0 {
		return s.positionRandomPage(p, desc)
	}
	if definition.ID != "random" && p.positional() && p.summary.classKey != "" && len(p.seekKeys) == 0 {
		return s.filteredRangePage(p)
	}
	if p.start > 0 && len(p.seekKeys) == 0 {
		if definition.ID == "random" {
			return s.seededRandomRange(p, definition, desc)
		}
		// A fact order's counted blocks serve an unrestricted viewer's direct
		// position: the block holding it, then a walk inside the block.
		if definition.Blocks > 0 && p.positional() && p.summary.classKey == "" {
			return s.positionPage(p, definition.Blocks, desc, p.start, p.limit)
		}
		return s.filteredRangePage(p)
	}
	if definition.ID == "random" {
		return s.seededRandomPage(p, definition, desc)
	}
	where, args := p.where, append([]any{}, p.args...)
	direction := "ASC"
	if desc {
		direction = "DESC"
	}
	if len(p.seekKeys) > 0 {
		key, err := indexedSortValue(definition.ID, p.seekKeys[0])
		if err != nil {
			return nil, ErrCursor
		}
		id, err := strconv.ParseInt(p.seekAnchor, 10, 64)
		if err != nil {
			return nil, ErrCursor
		}
		query, bound := browseBoundedSQL(browseRowColumns+",page.order_key", "catalog_browse_rows e JOIN catalog_entities ce ON ce.id=e.entity_id", p.where, p.args, definition.Expression, "e.entity_id", desc, false, key, id, p.limit, 0)
		return s.queryBrowseKeyRows(query, definition.ID, bound...)
	}
	args = append(args, p.limit)
	return s.queryBrowseKeyRows(`SELECT `+browseRowColumns+`,`+definition.Expression+` FROM catalog_browse_rows e JOIN catalog_entities ce ON ce.id=e.entity_id WHERE `+where+` ORDER BY `+definition.Expression+` `+direction+`,e.entity_id `+direction+` LIMIT ?`, definition.ID, args...)
}

func (s *Service) walkIndexedRange(p pageShape, page func(pageShape) ([]browseRow, error)) ([]browseRow, error) {
	skip, want := p.start, p.limit
	p.start = 0
	p.limit = BrowseMaximumLimit
	for skip > 0 {
		p.limit = min(skip, BrowseMaximumLimit)
		rows, err := page(p)
		if err != nil || len(rows) == 0 {
			return rows, err
		}
		skip -= len(rows)
		last := rows[len(rows)-1]
		p.seekAnchor = strconv.FormatInt(last.entity, 10)
		p.seekKeys = []string{browseSortValue(last, p.sorts[0].Field)}
		if p.sorts[0].Field == "personalRating" || p.sorts[0].Field == "lastPlayed" || p.sorts[0].Field == "playCount" {
			p.seekKeys = append(p.seekKeys, last.keys["phase"], last.keys["restTitle"])
		}
	}
	p.limit = want
	return page(p)
}

// A direct random range counts the first indexed half of the rotation and
// skips narrow index entries in each half. Only the selected page is hydrated.
func (s *Service) seededRandomRange(p pageShape, definition browseSort, desc bool) ([]browseRow, error) {
	seed := int64(p.sorts[0].Seed)
	direction, first, second := "ASC", "e.random_key>=?", "e.random_key<?"
	if desc {
		direction, first, second = "DESC", "e.random_key<=?", "e.random_key>?"
	}
	var firstCount int
	args := append(append([]any{}, p.args...), seed)
	if err := s.read().QueryRow(`SELECT count(*) FROM catalog_browse_rows e WHERE `+p.where+` AND `+first, args...).Scan(&firstCount); err != nil {
		return nil, err
	}
	out := []browseRow{}
	skip := p.start
	for phase, bound := range []string{first, second} {
		if phase == 0 && skip >= firstCount {
			skip -= firstCount
			continue
		}
		args := append(append([]any{}, p.args...), seed, p.limit-len(out), skip)
		query := `WITH page AS MATERIALIZED (SELECT e.entity_id,e.random_key FROM catalog_browse_rows e WHERE ` + p.where + ` AND ` + bound + ` ORDER BY e.random_key ` + direction + `,e.entity_id ` + direction + ` LIMIT ? OFFSET ?)
 SELECT ` + browseRowColumns + `,page.random_key FROM page CROSS JOIN catalog_browse_rows e ON e.entity_id=page.entity_id CROSS JOIN catalog_entities ce ON ce.id=e.entity_id ORDER BY page.random_key ` + direction + `,page.entity_id ` + direction
		rows, err := s.queryBrowseKeyRows(query, definition.ID, args...)
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
		if len(out) == p.limit {
			break
		}
		skip = 0
	}
	return out, nil
}

// A seed rotates a maintained pseudorandom index. Both halves are index walks;
// hashing or sorting the whole library per request is unnecessary.
func (s *Service) seededRandomPage(p pageShape, definition browseSort, desc bool) ([]browseRow, error) {
	seed := int64(p.sorts[0].Seed)
	phase := 0
	direction, firstBound, secondBound := "ASC", `e.random_key>=?`, `e.random_key<?`
	if desc {
		direction, firstBound, secondBound = "DESC", `e.random_key<=?`, `e.random_key>?`
	}
	var last any
	var anchor int64
	if len(p.seekKeys) > 0 {
		value, err := strconv.ParseInt(p.seekKeys[0], 10, 64)
		if err != nil {
			return nil, ErrCursor
		}
		last = value
		anchor, err = strconv.ParseInt(p.seekAnchor, 10, 64)
		if err != nil {
			return nil, ErrCursor
		}
		if !desc && value < seed || desc && value > seed {
			phase = 1
		}
	}
	out := []browseRow{}
	for ; phase < 2 && len(out) < p.limit; phase++ {
		bound := firstBound
		if phase == 1 {
			bound = secondBound
		}
		args := append(append([]any{}, p.args...), seed)
		query := `SELECT ` + browseRowColumns + `,e.random_key FROM catalog_browse_rows e JOIN catalog_entities ce ON ce.id=e.entity_id WHERE ` + p.where + ` AND ` + bound + ` ORDER BY e.random_key ` + direction + `,e.entity_id ` + direction + ` LIMIT ?`
		if last != nil {
			// Inside the cursor's phase, seek its key's later ids and then the
			// later keys, each run limited, instead of filtering the phase
			// from its seed bound up to the cursor.
			query, args = browseBoundedSQL(browseRowColumns+",page.order_key", "catalog_browse_rows e JOIN catalog_entities ce ON ce.id=e.entity_id", p.where+` AND `+bound, args, "e.random_key", "e.entity_id", desc, false, last, anchor, p.limit-len(out), 0)
		} else {
			args = append(args, p.limit-len(out))
		}
		rows, err := s.queryBrowseKeyRows(query, definition.ID, args...)
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
		last = nil
	}
	return out, nil
}

// Sparse maintained profile keys serve valued rows. Rows with no value use the
// maintained title order. A cursor says which stream it is in; neither history
// nor the library is collected and sorted in application memory.
func personalSortColumn(field string) (column, zero, index string) {
	switch field {
	case "lastPlayed":
		return "last_played_at", "''", "catalog_personal_order_last"
	case "playCount":
		return "play_count", "0", "catalog_personal_order_plays"
	}
	return "rating", "0", "catalog_personal_order_rating"
}

func personalKeyPageSQL(p pageShape, definition browseSort, desc bool) (string, []any, error) {
	column, zero, index := personalSortColumn(definition.ID)
	scope := `personal.profile_id=? AND personal.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND personal.kind=?`
	visible := ` AND EXISTS(SELECT 1 FROM catalog_browse_rows e WHERE e.entity_id=personal.entity_id AND ` + p.where + `)`
	where := scope + ` AND personal.` + column + `>` + zero + visible
	args := append([]any{p.request.Profile, p.request.Library, p.kind}, p.args...)
	direction, op := "ASC", ">"
	if desc {
		direction, op = "DESC", "<"
	}
	selected := `SELECT personal.entity_id AS id,personal.` + column + ` AS order_key FROM catalog_personal_sort_rows personal INDEXED BY ` + index + ` WHERE `
	if len(p.seekKeys) > 0 {
		key, err := indexedSortValue(definition.ID, p.seekKeys[0])
		if err != nil {
			return "", nil, ErrCursor
		}
		id, err := strconv.ParseInt(p.seekAnchor, 10, 64)
		if err != nil {
			return "", nil, ErrCursor
		}
		// Two limited seeks: the cursor value's later ids, then later values.
		// An ascending later run is already above the valued bound, and a
		// second lower bound could make SQLite seek from that bound instead.
		later := where
		if !desc && personalValued(key) {
			later = scope + visible
		}
		query := `SELECT id,order_key FROM (SELECT * FROM (` + selected + where + ` AND personal.` + column + `=? AND personal.entity_id` + op + `? ORDER BY personal.entity_id ` + direction + ` LIMIT ?)
 UNION ALL SELECT * FROM (` + selected + later + ` AND personal.` + column + op + `? ORDER BY personal.` + column + ` ` + direction + `,personal.entity_id ` + direction + ` LIMIT ?)) ORDER BY order_key ` + direction + `,id ` + direction + ` LIMIT ?`
		bound := append(append([]any{}, args...), key, id, p.limit)
		bound = append(append(bound, args...), key, p.limit, p.limit)
		return query, bound, nil
	}
	args = append(args, p.limit)
	return selected + where + ` ORDER BY personal.` + column + ` ` + direction + `,personal.entity_id ` + direction + ` LIMIT ?`, args, nil
}

// personalValued reports a cursor value inside the valued stream: a positive
// rating or play count, or a non-empty last-played time.
func personalValued(key any) bool {
	switch v := key.(type) {
	case float64:
		return v > 0
	case int64:
		return v > 0
	case string:
		return v > ""
	}
	return false
}

func (s *Service) indexedPersonalValuedPage(p pageShape, definition browseSort, desc bool) ([]browseRow, error) {
	query, args, err := personalKeyPageSQL(p, definition, desc)
	if err != nil {
		return nil, err
	}
	rows, err := s.read().Query(query, args...)
	if err != nil {
		return nil, err
	}
	ids := []int64{}
	keys := map[int64]string{}
	for rows.Next() {
		var id int64
		var value any
		if err = rows.Scan(&id, &value); err != nil {
			break
		}
		ids = append(ids, id)
		keys[id] = formatOrderValue(value)
		if n, ok := value.(int64); ok {
			keys[id] = strconv.FormatInt(n, 10)
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []browseRow{}, nil
	}
	raw, _ := json.Marshal(ids)
	projected, err := s.queryBrowseRows(`SELECT `+browseRowColumns+` FROM catalog_browse_rows e JOIN catalog_entities ce ON ce.id=e.entity_id WHERE e.entity_id IN(SELECT value FROM json_each(?))`, string(raw))
	if err != nil {
		return nil, err
	}
	byID := map[int64]browseRow{}
	for _, row := range projected {
		byID[row.entity] = row
	}
	out := make([]browseRow, 0, len(ids))
	for _, id := range ids {
		row, ok := byID[id]
		if !ok {
			continue
		}
		row.keys = map[string]string{definition.ID: keys[id], "phase": "valued", "restTitle": row.sortKey}
		out = append(out, row)
	}
	return out, nil
}

func (s *Service) indexedPersonalPage(p pageShape, definition browseSort, desc bool) ([]browseRow, error) {
	// An untouched sort is the title stream alone. Use its maintained blocks
	// for a direct range instead of walking every earlier title.
	if p.start > 0 && len(p.seekKeys) == 0 && p.positional() {
		column, zero, _ := personalSortColumn(definition.ID)
		var valued bool
		err := s.read().QueryRow(`SELECT EXISTS(SELECT 1 FROM catalog_personal_sort_rows personal WHERE personal.profile_id=? AND personal.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND personal.kind=? AND personal.`+column+`>`+zero+`)`, p.request.Profile, p.request.Library, p.kind).Scan(&valued)
		if err != nil {
			return nil, err
		}
		if !valued {
			rows, err := s.positionPage(p, 0, false, p.start, p.limit)
			for i := range rows {
				rows[i].keys = map[string]string{definition.ID: strings.Trim(zero, "'"), "phase": "rest", "restTitle": rows[i].sortKey}
			}
			return rows, err
		}
	}
	if p.start > 0 && len(p.seekKeys) == 0 {
		return s.walkIndexedRange(p, func(p pageShape) ([]browseRow, error) { return s.indexedPersonalPage(p, definition, desc) })
	}
	column, zero, _ := personalSortColumn(definition.ID)
	phases := []string{"rest", "valued"}
	if desc {
		phases = []string{"valued", "rest"}
	}
	at := 0
	var anchor int64
	if len(p.seekKeys) > 0 {
		if len(p.seekKeys) != 3 {
			return nil, ErrCursor
		}
		if p.seekKeys[1] == phases[1] {
			at = 1
		} else if p.seekKeys[1] != phases[0] {
			return nil, ErrCursor
		}
		var err error
		anchor, err = strconv.ParseInt(p.seekAnchor, 10, 64)
		if err != nil {
			return nil, ErrCursor
		}
	}
	out := []browseRow{}
	for ; at < 2 && len(out) < p.limit; at++ {
		phase := phases[at]
		if phase == "valued" {
			shape := p
			shape.limit = p.limit - len(out)
			rows, err := s.indexedPersonalValuedPage(shape, definition, desc)
			if err != nil {
				return nil, err
			}
			out = append(out, rows...)
			p.seekKeys = nil
			continue
		}
		where := p.where + ` AND NOT EXISTS(SELECT 1 FROM catalog_personal_sort_rows personal WHERE personal.profile_id=? AND personal.entity_id=e.entity_id AND personal.` + column + `>` + zero + `)`
		args := append(append([]any{}, p.args...), p.request.Profile)
		if len(p.seekKeys) > 0 {
			query, bound := browseBoundedSQL(browseRowColumns+","+zero, "catalog_browse_rows e JOIN catalog_entities ce ON ce.id=e.entity_id", where, args, "e.sort_key COLLATE NOCASE", "e.entity_id", false, false, p.seekKeys[2], anchor, p.limit-len(out), 0)
			rows, err := s.queryBrowseKeyRows(query, definition.ID, bound...)
			if err != nil {
				return nil, err
			}
			for i := range rows {
				rows[i].keys["phase"] = "rest"
				rows[i].keys["restTitle"] = rows[i].sortKey
			}
			out = append(out, rows...)
			p.seekKeys = nil
			continue
		}
		args = append(args, p.limit-len(out))
		rows, err := s.queryBrowseKeyRows(`SELECT `+browseRowColumns+`,`+zero+` FROM catalog_browse_rows e JOIN catalog_entities ce ON ce.id=e.entity_id WHERE `+where+` ORDER BY e.sort_key COLLATE NOCASE,e.entity_id LIMIT ?`, definition.ID, args...)
		if err != nil {
			return nil, err
		}
		for i := range rows {
			rows[i].keys["phase"] = phase
			rows[i].keys["restTitle"] = rows[i].sortKey
		}
		out = append(out, rows...)
		p.seekKeys = nil
	}
	return out, nil
}

// indexedFactAnchorRank counts the indexed prefix before a surviving anchor,
// using the same scope, tie break and seeded rotation as indexedFactPage.
func (s *Service) indexedFactAnchorRank(p pageShape, anchor string) (int, bool, error) {
	definition, desc := p.primary()
	var value any
	var entity int64
	err := s.read().QueryRow(`SELECT `+definition.Expression+`,e.entity_id FROM catalog_browse_rows e WHERE `+p.where+
		` AND e.entity_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, append(append([]any{}, p.args...), anchor)...).Scan(&value, &entity)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	op := "<"
	if desc {
		op = ">"
	}
	key := definition.Expression
	bound := `(` + key + op + `? OR ` + key + `=? AND e.entity_id` + op + `?)`
	args := append(append([]any{}, p.args...), value, value, entity)
	if definition.ID == "random" {
		randomKey, ok := value.(int64)
		if !ok {
			return 0, false, ErrCursor
		}
		seed := int64(p.sorts[0].Seed)
		first := key + `>=?`
		inFirst := randomKey >= seed
		if desc {
			first = key + `<=?`
			inFirst = randomKey <= seed
		}
		join := " OR "
		if inFirst {
			join = " AND "
		}
		bound = `(` + first + join + bound + `)`
		args = append(append([]any{}, p.args...), seed, value, value, entity)
	}
	var rank int
	err = s.read().QueryRow(`SELECT count(*) FROM catalog_browse_rows e WHERE `+p.where+` AND `+bound, args...).Scan(&rank)
	return rank, err == nil, err
}
