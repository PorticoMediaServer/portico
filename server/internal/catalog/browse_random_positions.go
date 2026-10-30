package catalog

import (
	"encoding/json"
	"math"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"strconv"
)

// A seeded random order rotates the maintained order. Find the seed's rank
// from its counted block, then read at most two page-sized runs across the wrap.
func (s *Service) positionRandomPage(p pageShape, desc bool) ([]browseRow, error) {
	if p.total == 0 {
		return []browseRow{}, nil
	}
	seed := int64(p.sorts[0].Seed)
	key, id, from, args := "e.random_key", "e.entity_id", `catalog_browse_rows e WHERE `+p.where, append([]any{}, p.args...)
	if p.summary.classKey != "" {
		key, id = "v.random_key", "v.entity_id"
		from = `compact_visibility_rows v WHERE v.class_id=? AND v.generation=? AND v.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND v.kind=?`
		args = []any{p.summary.classID, p.summary.generation, p.request.Library, p.kind}
	}
	inside := func(value any, entity int64) (int, error) {
		bound, op := "", "<"
		if desc {
			op = "<="
		}
		values := append(append([]any{}, args...), seed)
		bound = ` AND ` + key + op + `?`
		if value != nil {
			bound += ` AND ` + key + `>=? AND (` + key + `>? OR ` + id + `>=?)`
			values = append(values, value, value, entity)
		}
		var n int
		err := s.read().QueryRow(`SELECT count(*) FROM `+from+bound, values...).Scan(&n)
		return n, err
	}
	anchor := int64(0)
	if desc {
		anchor = math.MaxInt64
	}
	rank, err := s.blockRank(p.blocks(), compactcatalog.OrderRandom, seed, anchor, inside)
	if err != nil {
		return nil, err
	}
	if desc {
		rank = p.total - rank
	}
	start := (rank + p.start) % p.total
	want := min(p.limit, p.total-p.start)
	first := min(want, p.total-start)
	rows, err := s.positionPage(p, compactcatalog.OrderRandom, desc, start, first)
	if err != nil {
		return nil, err
	}
	if len(rows) == first && first < want {
		tail, err := s.positionPage(p, compactcatalog.OrderRandom, desc, 0, want-first)
		if err != nil {
			return nil, err
		}
		rows = append(rows, tail...)
	}
	// Cursor keys come from the same serving snapshot as the selected rows.
	ids := make([]int64, len(rows))
	for i, r := range rows {
		ids[i] = r.entity
	}
	raw, _ := json.Marshal(ids)
	keys, err := s.read().Query(`SELECT entity_id,random_key FROM catalog_browse_rows WHERE entity_id IN(SELECT value FROM json_each(?))`, string(raw))
	if err != nil {
		return nil, err
	}
	values := map[int64]int64{}
	for keys.Next() {
		var id, k int64
		if err = keys.Scan(&id, &k); err != nil {
			break
		}
		values[id] = k
	}
	if err == nil {
		err = keys.Err()
	}
	keys.Close()
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].keys = map[string]string{"random": strconv.FormatInt(values[rows[i].entity], 10)}
	}
	return rows, nil
}
