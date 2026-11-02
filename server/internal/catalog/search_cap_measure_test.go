package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/facettext"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// Engineering measurement only: choose a fixed production cap after comparing
// bounded rarest-prefix postings, full-word verification and independent title probes.
func TestSearchCapMeasurement(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("PORTICO_SEARCH_PROFILE_ITEMS"))
	if n == 0 {
		t.Skip("hosted cap comparison")
	}
	db, err := persistence.Open(filepath.Join(os.Getenv("PORTICO_SCALE_SEEDS"), fmt.Sprintf("search-realistic-films-%d.sqlite", n)))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	raw, err := os.ReadFile(filepath.Join(os.Getenv("PORTICO_SCALE_SEEDS"), fmt.Sprintf("search-realistic-films-%d.sqlite.corpus.json", n)))
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct{ Queries []struct{ Name, Q string } }
	if err = json.Unmarshal(raw, &corpus); err != nil {
		t.Fatal(err)
	}
	queries := map[string]string{}
	for _, q := range corpus.Queries {
		queries[q.Name] = q.Q
	}
	var title string
	var library int
	if err = db.QueryRow(`SELECT title,library_id FROM catalog_search_documents ORDER BY entity_id LIMIT 1`).Scan(&title, &library); err != nil {
		t.Fatal(err)
	}
	libraryName := "movies"
	if os.Getenv("PORTICO_SEARCH_SPARSE") == "1" {
		libraryName = "sparse-films"
		if err = db.QueryRow(`SELECT id FROM catalog_libraries WHERE library_id=?`, libraryName).Scan(&library); err != nil {
			t.Fatal(err)
		}
		if err = db.QueryRow(`SELECT title FROM catalog_search_documents WHERE entity_kind=1 AND library_id=? ORDER BY entity_id LIMIT 1`, library).Scan(&title); err != nil {
			t.Fatal(err)
		}
	}
	queries["exact-title"] = title
	words := strings.Fields(title)
	queries["starts-with"] = strings.Join(words[:min(2, len(words))], " ")
	s := New(db)
	results := []any{}
	type hit struct {
		ID       int64   `json:"id"`
		PID      string  `json:"pid"`
		Title    string  `json:"title"`
		Score    float64 `json:"score"`
		Priority int     `json:"priority"`
	}
	for name, q := range queries {
		_, match, err := NormalizeSearch(q)
		if err != nil {
			t.Fatal(err)
		}
		match = "search : (" + match + ")"
		var lead int64
		minimum := int64(1 << 62)
		maximum := int64(0)
		for _, word := range strings.Fields(facettext.SearchFold(q)) {
			runes := []rune(word)
			bound := facettext.IndexedSearchPrefixRunes
			if facettext.NumericSearchTerm(word) {
				bound = facettext.NumericSearchPrefixRunes
			}
			prefix := string(runes[:min(bound, len(runes))])
			var id, count, globalCount int64
			if err = db.QueryRow(`SELECT t.id,coalesce(sum(CASE WHEN c.kind=1 AND c.library_id=? THEN c.total ELSE 0 END),0),coalesce(sum(c.total),0) FROM catalog_search_prefix_terms t LEFT JOIN catalog_search_prefix_counts c ON c.term_id=t.id AND c.container=-1 WHERE t.prefix=? GROUP BY t.id`, library, prefix).Scan(&id, &count, &globalCount); err != nil {
				t.Fatal(err)
			}
			if count < minimum {
				lead, minimum = id, count
			}
			if globalCount > maximum {
				maximum = globalCount
			}
		}
		baseline, err := s.Search(context.Background(), SearchRequest{Viewer: Viewer{Profile: "F-empty-profile", Fence: "F-search", Libraries: []string{libraryName}}, Q: q, Group: "movies", Limit: 10, Sort: "relevance"})
		if err != nil || len(baseline.Groups) != 1 || baseline.Groups[0].Status != "success" {
			t.Fatalf("baseline %s: %+v %v", name, baseline, err)
		}
		for _, cap := range []int{512, 1024, 2048, 4096} {
			times := []float64{}
			vms := []int64{}
			var top []hit
			var statements []string
			bm25 := maximum <= int64(cap)
			score := `0.0`
			if bm25 {
				score = `-rank`
			}
			// MATERIALIZED bounds before MATCH verification and ranking. CROSS JOIN
			// fixes candidate-first rowid probes, so common words never drive a sort.
			query := `WITH candidates AS MATERIALIZED (SELECT entity_id,tier FROM catalog_search_prefix_rows WHERE term_id=? AND kind=1 AND library_id=? ORDER BY tier DESC,entity_id DESC LIMIT ?)
SELECT p.entity_id,d.entity_pid,d.title,p.tier,` + score + ` FROM candidates p CROSS JOIN catalog_search_titles ON catalog_search_titles.rowid=p.entity_id CROSS JOIN catalog_search_documents d ON d.entity_id=p.entity_id WHERE catalog_search_titles MATCH ?`
			for sample := 0; sample < 30; sample++ {
				ctx, cost := dbwork.Measure(context.Background())
				ctx, trace := dbwork.TraceQueryPlans(ctx)
				start := time.Now()
				hits := map[int64]hit{}
				err = dbwork.WithReadSnapshot(ctx, db, func(ctx context.Context) error {
					reader := New(db).WithContext(ctx).read()
					rows, e := reader.QueryContext(ctx, query, lead, library, cap, match)
					if e != nil {
						return e
					}
					for rows.Next() {
						var h hit
						var tier int
						var rank float64
						if e = rows.Scan(&h.ID, &h.PID, &h.Title, &tier, &rank); e != nil {
							rows.Close()
							return e
						}
						h.Score = float64(tier) + rank
						hits[h.ID] = h
					}
					e = rows.Err()
					rows.Close()
					if e != nil {
						return e
					}
					fold := facettext.SearchFold(q)
					// Both independent title probes have their own page bound; never count
					// or materialize their population. This fixture has one public scope.
					for _, exact := range []bool{true, false} {
						predicate := `d.title_fold>=? AND d.title_fold<?`
						args := []any{fold, fold + string(rune(0x10ffff)), library}
						priority := 2
						if exact {
							predicate = `d.title_fold=?`
							args = []any{fold, library}
							priority = 3
						}
						rows, e = reader.QueryContext(ctx, `SELECT d.entity_id,d.entity_pid,d.title FROM catalog_search_documents d INDEXED BY catalog_search_document_exact WHERE `+predicate+` AND d.entity_kind=1 AND d.library_id=? ORDER BY d.title_fold,d.entity_id LIMIT 11`, args...)
						if e != nil {
							return e
						}
						for rows.Next() {
							var h hit
							if e = rows.Scan(&h.ID, &h.PID, &h.Title); e != nil {
								rows.Close()
								return e
							}
							if old, ok := hits[h.ID]; ok && old.Priority > priority {
								continue
							}
							h.Priority = priority
							hits[h.ID] = h
						}
						e = rows.Err()
						rows.Close()
						if e != nil {
							return e
						}
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				ordered := []hit{}
				for _, h := range hits {
					ordered = append(ordered, h)
				}
				sort.Slice(ordered, func(i, j int) bool {
					a, b := ordered[i], ordered[j]
					if a.Priority != b.Priority {
						return a.Priority > b.Priority
					}
					if a.Score != b.Score {
						return a.Score > b.Score
					}
					return a.ID > b.ID
				})
				top = ordered[:min(10, len(ordered))]
				times = append(times, float64(time.Since(start).Nanoseconds())/1e6)
				vms = append(vms, cost().VMSteps)
				if sample == 0 {
					statements = trace()
				}
			}
			sort.Float64s(times)
			sort.Slice(vms, func(i, j int) bool { return vms[i] < vms[j] })
			before := []string{}
			after := []string{}
			retained := 0
			ids := map[string]bool{}
			for _, h := range top {
				after = append(after, h.PID)
				ids[h.PID] = true
			}
			for _, h := range baseline.Groups[0].Items {
				before = append(before, h.ID)
				if ids[h.ID] {
					retained++
				}
			}
			results = append(results, map[string]any{"query": name, "q": q, "cap": cap, "bm25": bm25, "rarest_postings": minimum, "broadest_postings": maximum, "p50_ms": times[15], "p95_ms": times[28], "p99_ms": times[29], "median_vm": vms[15], "before": before, "after": after, "retained": retained, "top": top, "queries": statements})
		}
	}
	raw, err = json.MarshalIndent(map[string]any{"items": n, "results": results, "boundary": "single film scope prototype with other-kind contamination and optional sparse library authority; Unicode and cursor contracts need production tests"}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(os.Getenv("PORTICO_SCALE_REPORT"), "cap-comparison"+os.Getenv("PORTICO_SEARCH_SPARSE")+".json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}
