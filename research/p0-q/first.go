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
	trialCandidates int
	spill           *sql.DB
	spillPath       string
	spillPeakBytes  int64
	spillCalls      int
	spillMaxMS      float64
	roots           map[int]int
	streams         contextHeap
	frontierReady   bool
	frontierPeak    int

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
			e := l.reader.read(ctx, exactTitleSQL(), []any{l.term, c.ID}, func(r *sql.Rows) error {
				var id, retired, kind int
				var fold string
				if e := r.Scan(&id, &fold, &retired, &kind); e != nil {
					return e
				}
				if retired == 0 && (kind == 2 || kind == 5 || kind == 6) {
					l.potentialParent = true
					if l.roots == nil {
						l.roots = map[int]int{}
					}
					l.roots[id] = kind
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
			e := l.reader.read(ctx, `WITH p AS MATERIALIZED(SELECT rowid FROM search_own_prefix WHERE search_own_prefix MATCH ? AND rowid>? ORDER BY rowid LIMIT 64) SELECT p.rowid,e.title_fold,e.retired,e.kind FROM p CROSS JOIN catalog_entities e ON e.id=p.rowid ORDER BY p.rowid`, []any{lazyTitleMatch(l.term, c.Tier), c.ID}, func(r *sql.Rows) error {
				var id, retired, kind int
				var fold string
				if e := r.Scan(&id, &fold, &retired, &kind); e != nil {
					return e
				}
				n++
				t := ownTier(fold, l.term)
				if retired == 0 && t >= 0 && (kind == 2 || kind == 5 || kind == 6) {
					l.potentialParent = true
					if l.roots == nil {
						l.roots = map[int]int{}
					}
					l.roots[id] = kind
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
			if !l.frontierReady {
				trial, last, scanned, done, e := l.contextTrial(ctx, c.ID, 20-len(ids), 4096-examined)
				if e != nil {
					return nil, c, e
				}
				examined += scanned
				ids = append(ids, trial...)
				c.ID = last
				if len(ids) == 20 {
					return ids, c, nil
				}
				if done {
					c.Tier = -1
					break
				}
			}
			if examined >= 4096 {
				return nil, c, fmt.Errorf("context trial exceeded page candidate budget")
			}
			if e := l.initFrontier(ctx, c.ID); e != nil {
				return nil, c, e
			}
			beforeCandidates := l.candidates
			next, done, e := l.contextNext(ctx, 4096-examined)
			if e != nil {
				return nil, c, e
			}
			if done {
				c.Tier = -1
				break
			}
			n = l.candidates - beforeCandidates
			batch = append(batch, candidate{next, 0})

		}
		examined += n
		if c.Tier > 0 {
			l.candidates += n
		}
		for _, x := range batch {
			c.ID = x.id
			if x.tier == c.Tier && (!l.restricted || x.id%100 == 0) {
				ids = append(ids, x.id)
				if len(ids) == 20 {
					return ids, c, nil
				}
			}
		}
		if n < 64 && c.Tier > 0 {
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
	shortOnlyBench(db, 128)
	ctx := context.Background()
	var rows *sql.Rows
	var e error
	if publishedSchema {
		rows, e = db.Query(`EXPLAIN QUERY PLAN SELECT entity FROM edges WHERE fragment=? AND entity>? ORDER BY entity LIMIT 1`, 1, -1)
	} else {
		rows, e = db.Query(`EXPLAIN QUERY PLAN SELECT x.entity,f.normalized FROM json_each(?) j CROSS JOIN edges x INDEXED BY edge_entity ON x.entity=j.value CROSS JOIN search_fragment_prefix z ON z.rowid=x.fragment CROSS JOIN search_fragments f ON f.id=z.rowid WHERE search_fragment_prefix MATCH ?`, "[0,1,2]", encode("王菲"))
	}
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
			l.close()
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
		spillMax := int64(0)
		spillNativeMax := 0.0
		spillCalls := 0
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
			spillMax = max(spillMax, l.spillPeakBytes)
			spillNativeMax = max(spillNativeMax, l.spillMaxMS)
			spillCalls += l.spillCalls
			l.close()
		}
		pageStats := make([]Stat, len(allPageSamples))
		for i, pages := range allPageSamples {
			pageStats[i] = stats(pages)
		}
		emit("first_bounded", map[string]any{"query": q, "latency": stats(times), "raw_ms": times, "snapshots": stats(snaps), "candidates": candidates, "first_ids": first, "result_count": len(all), "membership_digest": idsDigest(all), "page_raw_ms": allPageSamples, "per_page": pageStats, "page_sample_n": 3, "all_page_snapshots": stats(allSnaps), "budget_p95_ms": 50, "formal_1m_p95_ms": 200, "oracle": "exact old single-term tier/id order and complete membership", "lead": "single token; no population count required", "context_candidate_limit_per_page": 4096, "read_chunk": 64, "context_ram_head_limit": frontierRAMHeads, "spill_peak_bytes": spillMax, "spill_native_max_ms": spillNativeMax, "spill_calls": spillCalls, "spill_pager_mib": 2, "spill_disk_ceiling_mib": 16, "first_budget_scope": "first ready result;warm DB;no wire count"})
	}
}

func shortOnlyBench(db *sql.DB, samples int) {
	for _, q := range []string{"a", "al", "jo", "東", "王"} {
		times := []float64{}
		snaps := []float64{}
		var ids []int
		for i := 0; i < samples+1; i++ {
			t := time.Now()
			r, s, e := shortSuggestion(context.Background(), db, q, false)
			must(e)
			if i > 0 {
				times = append(times, float64(time.Since(t).Microseconds())/1000)
				snaps = append(snaps, s.Max)
			}
			ids = r
		}
		emit("policy_short", map[string]any{"query": q, "latency": stats(times), "raw_ms": times, "snapshot_maxima": stats(snaps), "ids": ids, "ranking": "catalogue insertion sequence/entityID DESC", "raw_candidate_limit": 256, "dispatch": "Latin1–2 orCJK1:titles only"})
	}
}
