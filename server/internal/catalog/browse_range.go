package catalog

import (
	"sort"
	"strconv"
	"strings"
)

// An arbitrary filtered range has no counted library positions. Resolve its
// ordering once in SQL and hydrate just the page. Continuations keep seeking
// their signed keys, including the personal-valued and random-rotation phase.
func (s *Service) filteredRangePage(p pageShape) ([]browseRow, error) {
	primary, desc := p.primary()
	if primary.ID == "forYou" {
		return s.personalPage(p, primary, desc)
	}
	direction := "ASC"
	if desc {
		direction = "DESC"
	}
	expression := primary.Expression
	args := []any{}
	order := `order_key ` + direction + `,entity_id ` + direction
	phase, rest := "", ""
	if primary.Profile {
		column, zero, _ := personalSortColumn(primary.ID)
		expression = `COALESCE((SELECT personal.` + column + ` FROM catalog_personal_sort_rows personal WHERE personal.profile_id=? AND personal.entity_id=e.entity_id),` + zero + `)`
		args = append(args, p.request.Profile)
		phase = `CASE WHEN order_key>` + zero + ` THEN 1 ELSE 0 END`
		if desc {
			phase = `CASE WHEN order_key>` + zero + ` THEN 0 ELSE 1 END`
		}
		rest = `CASE WHEN order_key<=` + zero + ` THEN sort_key END COLLATE NOCASE`
		order = phase + ` ASC,order_key ` + direction + `,` + rest + ` ASC,CASE WHEN order_key>` + zero + ` THEN entity_id END ` + direction + `,CASE WHEN order_key<=` + zero + ` THEN entity_id END ASC`
	} else if primary.ID == "random" {
		op := ">="
		if desc {
			op = "<="
		}
		order = `CASE WHEN order_key` + op + `? THEN 0 ELSE 1 END ASC,` + order
	}
	from, where, scopeArgs := "catalog_browse_rows e", p.where, p.args
	live, liveArgs := "1", []any(nil)
	// An availability filter alone is answered by the published class too:
	// its rows carry their availability, and its counts gave the total.
	availability := p.request.Query != nil && p.request.Query.Field == "availability" && p.kind != 0
	class := (p.request.Query == nil || availability) && p.summary.classKey != "" && !primary.Profile
	row := "e"
	if class {
		from = `compact_visibility_rows v CROSS JOIN catalog_browse_rows e ON e.entity_id=v.entity_id`
		if p.unrestricted != "" {
			// The published class is the restricted viewer's row set and its
			// positions; the class count is the total. The live restriction is
			// checked on the selected rows only, so a deep position does not
			// test every row before it.
			where, scopeArgs = p.unrestricted, p.unrestrictedArgs
			live, liveArgs = p.live, p.liveArgs
		}
		if availability && primary.ClassKey != "" && p.unrestricted != "" {
			// The class's own order and availability: a deep position walks
			// the class's index alone, never a browse row or a live test
			// before the page.
			compiler := browseCompiler{}
			field, _ := browseFieldByID("availability")
			field.Entity = "v.availability_state"
			predicate, err := compiler.entityPredicate(field, *p.request.Query)
			if err != nil {
				return nil, err
			}
			from, row, where, scopeArgs = `compact_visibility_rows v`, "v", predicate, compiler.args
			expression = primary.ClassKey
		}
		where = `v.class_id=? AND v.generation=? AND v.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND v.kind=? AND (` + where + `)`
		scopeArgs = append([]any{p.summary.classID, p.summary.generation, p.request.Library, p.kind}, scopeArgs...)
	}
	// A deep range under one fact sort, for a viewer whose rows the browse
	// revision fully decides, seeks the result's nearest sparse anchor.
	if !class && !p.reading && !primary.Profile && primary.ID != "random" && (p.live == "" || p.live == "1") && len(p.sorts) == 1 && p.revision != "" && p.start >= rangeAnchorEvery {
		if rows, ok, err := s.anchoredRangePage(p, primary, desc); ok || err != nil {
			return rows, err
		}
	}
	query := `WITH page AS MATERIALIZED (SELECT ` + row + `.entity_id AS entity_id,` + row + `.sort_key AS sort_key,` + expression + ` AS order_key FROM ` + from + ` WHERE ` + where + ` ORDER BY ` + order + ` LIMIT ? OFFSET ?)
 SELECT ` + browseRowColumns + `,page.order_key FROM page CROSS JOIN catalog_browse_rows e ON e.entity_id=page.entity_id
 CROSS JOIN catalog_entities ce ON ce.id=e.entity_id WHERE ` + live + ` ORDER BY ` + strings.NewReplacer("order_key", "page.order_key", "entity_id", "page.entity_id", "sort_key", "page.sort_key").Replace(order)
	selectRows := func(limit, offset int) ([]browseRow, error) {
		bound := append(append([]any{}, args...), scopeArgs...)
		if primary.ID == "random" {
			bound = append(bound, int64(p.sorts[0].Seed))
		}
		bound = append(bound, limit, offset)
		bound = append(bound, liveArgs...)
		if primary.ID == "random" {
			bound = append(bound, int64(p.sorts[0].Seed))
		}
		return s.queryBrowseKeyRows(query, primary.ID, bound...)
	}
	rows, err := selectRows(p.limit, p.start)
	if err != nil {
		return nil, err
	}
	// A title restricted since the class published is dropped from its page;
	// the page is refilled from the rows after it, in growing steps.
	for chunk, offset, attempt := p.limit, p.start+p.limit, 0; live != "1" && len(rows) < p.limit && offset < p.total && attempt < 8; chunk, offset, attempt = chunk*2, offset+chunk*2, attempt+1 {
		more, err := selectRows(chunk*2, offset)
		if err != nil {
			return nil, err
		}
		rows = append(rows, more...)
	}
	if len(rows) > p.limit {
		rows = rows[:p.limit]
	}
	if primary.Profile {
		_, zero, _ := personalSortColumn(primary.ID)
		zeroValue := "0"
		if zero == "''" {
			zeroValue = ""
		}
		for i := range rows {
			state := "valued"
			if rows[i].keys[primary.ID] == zeroValue {
				state = "rest"
			}
			rows[i].keys["phase"] = state
			rows[i].keys["restTitle"] = rows[i].sortKey
		}
	}
	return rows, nil
}

// anchoredRangePage reads a deep range from the nearest sparse anchor at or
// before it: two bounded seeks from the anchor's key, skipping at most
// rangeAnchorEvery rows. The anchors are read once per browse revision.
func (s *Service) anchoredRangePage(p pageShape, primary browseSort, desc bool) ([]browseRow, bool, error) {
	anchors, ok := s.cachedRangeAnchors(p.revision)
	if !ok {
		direction := "ASC"
		if desc {
			direction = "DESC"
		}
		rows, err := s.read().Query(`SELECT position,order_key,entity_id FROM (SELECT `+primary.Expression+` AS order_key,e.entity_id AS entity_id,ROW_NUMBER() OVER (ORDER BY `+primary.Expression+` `+direction+`,e.entity_id `+direction+`)-1 AS position FROM catalog_browse_rows e WHERE `+p.where+`) WHERE position%`+strconv.Itoa(rangeAnchorEvery)+`=0 ORDER BY position`, p.args...)
		if err != nil {
			return nil, false, err
		}
		for rows.Next() {
			var anchor rangeAnchor
			if err = rows.Scan(&anchor.position, &anchor.key, &anchor.id); err != nil {
				break
			}
			anchors = append(anchors, anchor)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return nil, false, err
		}
		s.storeRangeAnchors(p.revision, anchors)
	}
	i := sort.Search(len(anchors), func(i int) bool { return anchors[i].position > p.start }) - 1
	if i < 0 {
		return []browseRow{}, len(anchors) == 0, nil
	}
	anchor := anchors[i]
	if anchor.key == nil {
		return nil, false, nil
	}
	query, bound := browseBoundedSQL(browseRowColumns+",page.order_key", "catalog_browse_rows e JOIN catalog_entities ce ON ce.id=e.entity_id", p.where, p.args, primary.Expression, "e.entity_id", desc, true, anchor.key, anchor.id, p.limit, p.start-anchor.position)
	rows, err := s.queryBrowseKeyRows(query, primary.ID, bound...)
	return rows, err == nil, err
}
