package db

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
