package main

// Lazy primary-prefix pages. Single-token queries have a known lead without a
// population scan. Exact/title-start/title-word/context tiers are traversed in
// rank order; only the current bounded entity window is verified for context.
import (
	"context"
	"database/sql"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/facettext"
	"strings"
	"time"
)

type lazySearch struct {
	reader          *policyReader
	term            string
	restricted      bool
	candidates      int
	potentialParent bool
	contextChecked  bool
	contextPossible bool
}
type lazyCursor struct {
	Tier, ID      int
	Started, Done bool
}

func ownTier(fold, term string) int {
	if fold == term {
		return 3
	}
	if strings.HasPrefix(fold, term) {
		return 2
	}
	if strings.ContainsFunc(term, facettext.CJK) {
		if strings.Contains(fold, term) {
			return 1
		}
		return -1
	}
	for _, w := range strings.Fields(facettext.SearchDocument(fold)) {
		if strings.HasPrefix(w, term) {
			return 1
		}
	}
	return -1
}
func prefixMatch(term string, initial bool) string {
	col := "title_prefix_tokens:"
	if initial {
		col = "initial_prefix_tokens:"
	}
	if strings.ContainsFunc(term, facettext.CJK) && !initial {
		return col + encode(cjkSeed(term))
	}
	return col + encode(term) + "*"
}
func (l *lazySearch) page(ctx context.Context, c lazyCursor) ([]int, lazyCursor, error) {
	if c.Done {
		return nil, c, nil
	}
	if !c.Started {
		c = lazyCursor{Tier: 3, ID: -1, Started: true}
	}
	ids := []int{}
	examined := 0
	for c.Tier >= 0 && len(ids) < 20 {
		if e := ctx.Err(); e != nil {
			return nil, c, e
		}
		type candidate struct{ id, tier int }
		batch := []candidate{}
		n := 0
		if c.Tier == 3 {
			e := l.reader.read(ctx, `SELECT id,title_fold,retired,kind FROM catalog_entities INDEXED BY catalog_entities_search_exact WHERE library_id IN(0,1,2,3,4,5,6,7) AND kind IN(1,2,3,4,5,6,7,8) AND title_fold=? AND id>? ORDER BY id LIMIT 64`, []any{l.term, c.ID}, func(r *sql.Rows) error {
				var id, retired, kind int
				var fold string
				if e := r.Scan(&id, &fold, &retired, &kind); e != nil {
					return e
				}
				if retired == 0 && (kind == 2 || kind == 5 || kind == 6) {
					l.potentialParent = true
				}
				n++
				batch = append(batch, candidate{id, 3})
				if retired != 0 {
					batch[len(batch)-1].tier = -1
				}
				return nil
			})
			if e != nil {
				return nil, c, e
			}
		} else if c.Tier == 2 || c.Tier == 1 {
			e := l.reader.read(ctx, `WITH p AS MATERIALIZED(SELECT rowid FROM search_own_prefix WHERE search_own_prefix MATCH ? AND rowid>? ORDER BY rowid LIMIT 64) SELECT p.rowid,e.title_fold,e.retired,e.kind FROM p CROSS JOIN catalog_entities e ON e.id=p.rowid ORDER BY p.rowid`, []any{prefixMatch(l.term, c.Tier == 2), c.ID}, func(r *sql.Rows) error {
				var id, retired, kind int
				var fold string
				if e := r.Scan(&id, &fold, &retired, &kind); e != nil {
					return e
				}
				n++
				t := ownTier(fold, l.term)
				if retired == 0 && t >= 0 && (kind == 2 || kind == 5 || kind == 6) {
					l.potentialParent = true
				}
				if retired != 0 {
					t = -1
				}
				batch = append(batch, candidate{id, t})
				return nil
			})
			if e != nil {
				return nil, c, e
			}
		} else {
			if !l.contextChecked {
				l.contextChecked = true
				match := encode(l.term) + "*"
				if strings.ContainsFunc(l.term, facettext.CJK) {
					match = encode(cjkSeed(l.term))
				}
				e := l.reader.read(ctx, `SELECT rowid FROM search_fragment_prefix WHERE search_fragment_prefix MATCH ? LIMIT 1`, []any{match}, func(r *sql.Rows) error {
					var id int
					if e := r.Scan(&id); e != nil {
						return e
					}
					l.contextPossible = true
					return nil
				})
				if e != nil {
					return nil, c, e
				}
			}
			if !l.potentialParent && !l.contextPossible {
				c.Tier = -1
				break
			}
			folds := map[int]string{}
			entityIDs := []int{}
			e := l.reader.read(ctx, `SELECT e.id,e.title_fold,e.retired,e.kind FROM catalog_entities e CROSS JOIN q_items q ON q.id=e.id WHERE e.id>? ORDER BY e.id LIMIT 64`, []any{c.ID}, func(r *sql.Rows) error {
				var id, retired, kind int
				var fold string
				if e := r.Scan(&id, &fold, &retired, &kind); e != nil {
					return e
				}
				n++
				entityIDs = append(entityIDs, id)
				folds[id] = fold
				if retired != 0 {
					folds[id] = ""
				}
				return nil
			})
			if e != nil {
				return nil, c, e
			}
			matched := map[int]bool{}
			if len(entityIDs) > 0 {
				// Fixed parent depth. Only title matches propagate; a parent's people do not.
				e = l.reader.read(ctx, `SELECT j.value,p.title_fold,g.title_fold FROM json_each(?) j CROSS JOIN catalog_entities e ON e.id=j.value LEFT JOIN rel r ON r.child=e.id LEFT JOIN catalog_entities p ON p.id=r.parent AND p.retired=0 LEFT JOIN rel r2 ON r2.child=p.id LEFT JOIN catalog_entities g ON g.id=r2.parent AND g.retired=0`, []any{encodedJSON(entityIDs)}, func(r *sql.Rows) error {
					var id int
					var p, g sql.NullString
					if e := r.Scan(&id, &p, &g); e != nil {
						return e
					}
					if (p.Valid && ownTier(p.String, l.term) >= 0) || (g.Valid && ownTier(g.String, l.term) >= 0) {
						matched[id] = true
					}
					return nil
				})
				if e != nil {
					return nil, c, e
				}
				// The entity reverse edge is outermost: <=64*3 fixture edges, not all FTS hits.
				match := encode(l.term) + "*"
				if strings.ContainsFunc(l.term, facettext.CJK) {
					match = encode(cjkSeed(l.term))
				}
				e = l.reader.read(ctx, `SELECT x.entity,f.normalized FROM json_each(?) j CROSS JOIN edges x INDEXED BY edge_entity ON x.entity=j.value CROSS JOIN search_fragment_prefix z ON z.rowid=x.fragment CROSS JOIN search_fragments f ON f.id=z.rowid WHERE search_fragment_prefix MATCH ?`, []any{encodedJSON(entityIDs), match}, func(r *sql.Rows) error {
					var id int
					var fold string
					if e := r.Scan(&id, &fold); e != nil {
						return e
					}
					if ownTier(fold, l.term) >= 0 {
						matched[id] = true
					}
					return nil
				})
				if e != nil {
					return nil, c, e
				}
			}
			for _, id := range entityIDs {
				t := -1
				if folds[id] != "" && ownTier(folds[id], l.term) < 0 && matched[id] {
					t = 0
				}
				batch = append(batch, candidate{id, t})
			}
		}
		examined += n
		l.candidates += n
		for _, x := range batch {
			c.ID = x.id
			if x.tier == c.Tier && (!l.restricted || x.id%100 == 0) {
				ids = append(ids, x.id)
				if len(ids) == 20 {
					return ids, c, nil
				}
			}
		}
		if n < 64 {
			c.Tier--
			c.ID = -1
		}
		if examined >= 4096 && len(ids) < 20 {
			return nil, c, fmt.Errorf("bounded page candidate budget exceeded after %d candidates; no successful empty/truncated page", examined)
		}
	}
	c.Done = c.Tier < 0
	return ids, c, nil
}
func firstPageBench(db *sql.DB, n, samples int) {
	ctx := context.Background()
	rows, e := db.Query(`EXPLAIN QUERY PLAN SELECT x.entity,f.normalized FROM json_each(?) j CROSS JOIN edges x INDEXED BY edge_entity ON x.entity=j.value CROSS JOIN search_fragment_prefix z ON z.rowid=x.fragment CROSS JOIN search_fragments f ON f.id=z.rowid WHERE search_fragment_prefix MATCH ?`, "[0,1,2]", encode("王菲"))
	must(e)
	plan := []string{}
	for rows.Next() {
		var id, parent, unused int
		var detail string
		must(rows.Scan(&id, &parent, &unused, &detail))
		plan = append(plan, detail)
	}
	must(rows.Close())
	emit("first_context_plan", plan)
	for _, q := range []string{"東京", "王菲", "李华", "ada", "harbor", "al", "jo"} {
		// Historical complete preparation is diagnostic only, never on the fast path.
		old, e := preparePolicy(ctx, db, q, false)
		must(e)
		expected := []int{}
		for _, h := range old.items {
			expected = append(expected, h.id)
		}
		old.reader.close()
		emit("first_diagnosis", map[string]any{"query": q, "old": old.diagnosis, "expected_count": len(expected)})
		times := []float64{}
		snaps := []float64{}
		candidates := []int{}
		var first []int
		for i := 0; i < samples; i++ {
			l := lazySearch{reader: &policyReader{db: db}, term: facettext.SearchFold(q)}
			t := time.Now()
			ids, _, e := l.page(ctx, lazyCursor{})
			must(e)
			times = append(times, float64(time.Since(t).Microseconds())/1000)
			snaps = append(snaps, l.reader.snapshots...)
			candidates = append(candidates, l.candidates)
			l.reader.close()
			first = ids
			if len(ids) != min(20, len(expected)) {
				panic("first page size mismatch")
			}
			for j, id := range ids {
				if id != expected[j] {
					panic(fmt.Sprintf("first rank mismatch %s %d: %d != %d", q, j, id, expected[j]))
				}
			}
		}
		// Exhaustive page/rank oracle once, separately timed by ordinal. No precomputed
		// results or expected-set membership is consulted by the lazy algorithm.
		allPageSamples := [][]float64{}
		var all []int
		var allSnaps []float64
		for rep := 0; rep < 3; rep++ {
			l := lazySearch{reader: &policyReader{db: db}, term: facettext.SearchFold(q)}
			c := lazyCursor{}
			all = []int{}
			pages := []float64{}
			for {
				t := time.Now()
				ids, next, e := l.page(ctx, c)
				must(e)
				pages = append(pages, float64(time.Since(t).Microseconds())/1000)
				all = append(all, ids...)
				c = next
				if c.Done {
					break
				}
			}
			if len(all) != len(expected) {
				panic(fmt.Sprintf("membership mismatch %s %d != %d", q, len(all), len(expected)))
			}
			for j, id := range all {
				if id != expected[j] {
					panic(fmt.Sprintf("page rank mismatch %s %d: %d != %d", q, j, id, expected[j]))
				}
			}
			for i, ms := range pages {
				for len(allPageSamples) <= i {
					allPageSamples = append(allPageSamples, nil)
				}
				allPageSamples[i] = append(allPageSamples[i], ms)
			}
			allSnaps = append(allSnaps, l.reader.snapshots...)
			l.reader.close()
		}
		pageStats := make([]Stat, len(allPageSamples))
		for i, pages := range allPageSamples {
			pageStats[i] = stats(pages)
		}
		emit("first_bounded", map[string]any{"query": q, "latency": stats(times), "raw_ms": times, "snapshots": stats(snaps), "candidates": candidates, "first_ids": first, "result_count": len(all), "membership_digest": idsDigest(all), "page_raw_ms": allPageSamples, "per_page": pageStats, "page_sample_n": 3, "all_page_snapshots": stats(allSnaps), "budget_p95_ms": 50, "formal_1m_p95_ms": 200, "oracle": "exact old single-term tier/id order and complete membership", "lead": "single token; no population count required", "context_candidate_limit_per_page": 4096, "read_chunk": 64})
	}
}
