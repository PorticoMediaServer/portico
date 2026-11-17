package db

import "strings"

// A lagging index may have old permission facts, so pages check un-narrowed matches.
// Totals still use the copied facts, correcting only rows affected by the backlog.
func (s *searcher) candidateMatch(expr string, code int64) string {
	if s.stale {
		return expr
	}
	return s.over(expr, code)
}

// changedMatches includes descendants because a holder's rating or label reaches them.
// A pruned backlog cannot identify the affected rows; only that case needs a full recount.
func (s *searcher) changedMatches() (map[int64]bool, bool, error) {
	changed := map[int64]bool{}
	if !s.stale {
		return changed, false, nil
	}
	var first int64
	if err := s.tx.QueryRowContext(s.ctx, `SELECT ifnull(min(id), 0) FROM changes`).Scan(&first); err != nil {
		return nil, false, err
	}
	if first == 0 || s.cursor < first-1 {
		return nil, true, nil
	}
	items, err := ids(s.ctx, s.tx, `SELECT DISTINCT ref_id FROM changes WHERE id > ? AND topic = ?`, s.cursor, TopicItem)
	if err != nil {
		return nil, false, err
	}
	if len(items) > 100 {
		// For a bulk backlog, intersect the FTS postings once. Thousands of individual
		// rowid/MATCH probes would each reopen the same broad postings list.
		rows, err := s.indexRows(`SELECT rowid FROM search_titles WHERE search_titles MATCH ?
			AND (rowid >> ?) != ? AND (rowid & ?) IN (SELECT value FROM json_each(?))`,
			s.match.all, searchIDBits, searchPeople, int64(1<<searchIDBits-1), idsJSON(items))
		if err != nil {
			return nil, false, err
		}
		for _, row := range rows {
			changed[row] = true
		}
	} else {
		var direct []int64
		for _, id := range items {
			for _, g := range searchGroups {
				if g.code != searchPeople {
					direct = append(direct, searchRow(g.code, id))
				}
			}
		}
		rows, err := s.indexRows(`SELECT rowid FROM search_titles WHERE search_titles MATCH ? AND rowid IN (SELECT value FROM json_each(?))`,
			s.match.all, idsJSON(direct))
		if err != nil {
			return nil, false, err
		}
		for _, row := range rows {
			changed[row] = true
		}
	}
	holders, err := ids(s.ctx, s.tx, `SELECT DISTINCT c.ref_id FROM changes c LEFT JOIN items i ON i.id = c.ref_id
		WHERE c.id > ? AND c.topic = ? AND (i.id IS NULL OR i.kind NOT IN `+playableKindsSQL+`)`, s.cursor, TopicItem)
	if err != nil {
		return nil, false, err
	}
	for len(holders) > 0 {
		n := min(len(holders), 100)
		var under []string
		for _, id := range holders[:n] {
			under = append(under, `"`+searchUnder(id)+`"`)
		}
		rows, err := s.indexRows(`SELECT rowid FROM search_titles WHERE search_titles MATCH ?`,
			`(`+s.match.all+`) AND (`+strings.Join(under, ` OR `)+`)`)
		if err != nil {
			return nil, false, err
		}
		for _, row := range rows {
			changed[row] = true
		}
		holders = holders[n:]
	}
	return changed, false, nil
}

func (s *searcher) correctTotals(totals map[int64]searchTotal, changed map[int64]bool) error {
	var rows []int64
	for row := range changed {
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil
	}
	// A single postings walk avoids repeatedly reopening a common prefix for every
	// changed title. Main-database checks below still touch only affected matches.
	copied, err := s.indexRows(`SELECT rowid FROM search_titles WHERE search_titles MATCH ? AND (rowid + 0) IN (SELECT value FROM json_each(?))`,
		s.acc.narrowed(s.match.all), idsJSON(rows))
	if err != nil {
		return err
	}
	for _, row := range copied {
		code := row >> searchIDBits
		total := totals[code]
		total.visible--
		totals[code] = total
	}
	for len(rows) > 0 {
		n := min(len(rows), 5000)
		seen, err := s.check(rows[:n])
		if err != nil {
			return err
		}
		for row := range seen {
			code := row >> searchIDBits
			total := totals[code]
			total.visible++
			totals[code] = total
		}
		rows = rows[n:]
	}
	return nil
}

// visibleCandidates reads matching rows in batches until it has enough current visible
// matches, rather than letting hidden people use the result or sort budget.
func (s *searcher) visibleCandidates(expr string, code int64, limit int) ([]int64, error) {
	var out []int64
	var after int64
	for {
		rows, err := s.candidates(expr, code, after, 500)
		if err != nil {
			return nil, err
		}
		seen, err := s.check(rows)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if seen[row] {
				out = append(out, row)
				s.checked[row] = true
				if len(out) == limit {
					return out, nil
				}
			}
		}
		if len(rows) < 500 {
			return out, nil
		}
		after = rows[len(rows)-1]
	}
}

// currentTotals is needed only while copied search facts lag this catalogue snapshot.
// It checks batches without retaining every hidden match or blocking the follower.
func (s *searcher) currentTotals() (map[int64]searchTotal, error) {
	totals := map[int64]searchTotal{}
	for _, g := range searchGroups {
		if g.code == searchPeople && !s.acc.seesAll() {
			rows, err := s.visibleCandidates(s.match.all, g.code, searchCheckCap+1)
			if err != nil {
				return nil, err
			}
			totals[g.code] = searchTotal{visible: min(len(rows), searchCheckCap), minimum: len(rows) > searchCheckCap}
			continue
		}
		var after int64
		count := 0
		for {
			rows, err := s.candidates(s.match.all, g.code, after, 500)
			if err != nil {
				return nil, err
			}
			seen, err := s.check(rows)
			if err != nil {
				return nil, err
			}
			count += len(seen)
			if len(rows) < 500 {
				break
			}
			after = rows[len(rows)-1]
		}
		totals[g.code] = searchTotal{visible: count}
	}
	return totals, nil
}

func (s *searcher) indexRows(query string, args ...any) ([]int64, error) {
	rows, err := s.index.QueryContext(s.ctx, query, args...)
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
