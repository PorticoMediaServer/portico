package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestSearchNativeProfileMeasurement(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("PORTICO_SEARCH_PROFILE_ITEMS"))
	if n == 0 {
		t.Skip("hosted native search profile")
	}
	realistic := os.Getenv("PORTICO_SEARCH_REALISTIC") == "1"
	prefix := "search-films"
	if realistic {
		prefix = "search-realistic-films"
	}
	type querySpec struct {
		Name            string `json:"name"`
		Q               string `json:"q"`
		DocumentMatches int    `json:"document_matches"`
	}
	querySpecs := []querySpec{{Name: "pathological-upper-bound", Q: "Title 000000042", DocumentMatches: 1}}
	if realistic {
		raw, err := os.ReadFile(filepath.Join(os.Getenv("PORTICO_SCALE_SEEDS"), fmt.Sprintf("%s-%d.sqlite.corpus.json", prefix, n)))
		if err != nil {
			t.Fatal(err)
		}
		var corpus struct {
			Queries []querySpec `json:"queries"`
			Items   int         `json:"items"`
		}
		if err = json.Unmarshal(raw, &corpus); err != nil || corpus.Items != n || len(corpus.Queries) != 5 {
			t.Fatalf("corpus metadata: %+v %v", corpus, err)
		}
		querySpecs = corpus.Queries
	}
	in, err := os.Open(filepath.Join(os.Getenv("PORTICO_SCALE_SEEDS"), fmt.Sprintf("%s-%d.sqlite", prefix, n)))
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.Create(filepath.Join(t.TempDir(), "search.sqlite"))
	if err != nil {
		in.Close()
		t.Fatal(err)
	}
	_, err = io.Copy(out, in)
	in.Close()
	out.Close()
	if err != nil {
		t.Fatal(err)
	}
	db, err := persistence.Open(out.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db)
	request := SearchRequest{Viewer: Viewer{Profile: "F-empty-profile", Fence: "F-search", Libraries: []string{"movies"}}, Q: "Title 000000042", Group: "movies", Limit: 40, Sort: "relevance"}
	_, match, err := NormalizeSearch(request.Q)
	if err != nil {
		t.Fatal(err)
	}
	var library int
	if err = db.QueryRow(`SELECT id FROM catalog_libraries WHERE library_id='movies'`).Scan(&library); err != nil {
		t.Fatal(err)
	}
	scoped := fmt.Sprintf("kind : k1 AND kind : l%d AND search : (%s)", library, match)
	report := os.Getenv("PORTICO_SCALE_REPORT")
	results := []map[string]any{}
	for _, spec := range querySpecs {
		request.Q = spec.Q
		_, match, err = NormalizeSearch(request.Q)
		if err != nil {
			t.Fatal(err)
		}
		scoped = fmt.Sprintf("kind : k1 AND kind : l%d AND search : (%s)", library, match)
		for _, mode := range []string{"service-relevance", "service-title", "fts-selective", "fts-selective-ranked", "fts-scoped", "fts-scoped-ranked"} {
			ranked := strings.HasSuffix(mode, "-ranked")
			query := `SELECT rowid FROM catalog_search_titles WHERE catalog_search_titles MATCH ? LIMIT 41`
			argument := "search : (" + match + ")"
			if mode == "fts-scoped" || mode == "fts-scoped-ranked" {
				argument = scoped
			}
			if ranked {
				query = `SELECT rowid,rank FROM catalog_search_titles WHERE catalog_search_titles MATCH ? ORDER BY rank LIMIT 41`
			}
			plans := []string{}
			if mode[:3] == "fts" {
				rows, e := db.Query("EXPLAIN QUERY PLAN "+query, argument)
				if e != nil {
					t.Fatal(e)
				}
				for rows.Next() {
					var id, parent, unused int
					var detail string
					if e = rows.Scan(&id, &parent, &unused, &detail); e != nil {
						t.Fatal(e)
					}
					plans = append(plans, detail)
				}
				if e = rows.Err(); e != nil {
					t.Fatal(e)
				}
				rows.Close()
			}
			profileName := mode
			if realistic {
				profileName = spec.Name + "-" + mode
			}
			profile, e := os.Create(filepath.Join(report, profileName+".pprof"))
			if e != nil {
				t.Fatal(e)
			}
			if e = pprof.StartCPUProfile(profile); e != nil {
				t.Fatal(e)
			}
			millis := []float64{}
			vms := []int64{}
			failures := []string{}
			statements := []string{}
			counts := []int{}
			for sample := 0; sample < 30; sample++ {
				ctx, cost := dbwork.Measure(context.Background())
				ctx, trace := dbwork.TraceQueryPlans(ctx)
				start := time.Now()
				count := 0
				if mode == "service-relevance" || mode == "service-title" {
					r := request
					if mode == "service-title" {
						r.Sort = "title"
					}
					var env SearchEnvelope
					env, e = s.Search(ctx, r)
					if e == nil {
						if len(env.Groups) != 1 {
							e = fmt.Errorf("groups %d", len(env.Groups))
						} else if env.Groups[0].Status != "success" {
							e = fmt.Errorf("group status %s: %s", env.Groups[0].Status, env.Groups[0].ErrorCode)
						} else {
							count = len(env.Groups[0].Items)
						}
					}
				} else {
					e = dbwork.WithReadSnapshot(ctx, db, func(snapshot context.Context) error {
						rows, qerr := New(db).WithContext(snapshot).read().QueryContext(snapshot, query, argument)
						if qerr != nil {
							return qerr
						}
						defer rows.Close()
						for rows.Next() {
							var id int64
							if ranked {
								var rank float64
								qerr = rows.Scan(&id, &rank)
							} else {
								qerr = rows.Scan(&id)
							}
							if qerr != nil {
								return qerr
							}
							count++
						}
						return rows.Err()
					})
				}
				millis = append(millis, float64(time.Since(start).Nanoseconds())/1e6)
				vms = append(vms, cost().VMSteps)
				counts = append(counts, count)
				if e != nil {
					failures = append(failures, e.Error())
				}
				if sample == 0 {
					statements = trace()
				}
			}
			pprof.StopCPUProfile()
			profile.Close()
			sort.Float64s(millis)
			sort.Slice(vms, func(i, j int) bool { return vms[i] < vms[j] })
			expected := min(41, spec.DocumentMatches)
			if mode == "service-relevance" || mode == "service-title" {
				expected = min(40, spec.DocumentMatches)
			}
			results = append(results, map[string]any{"query_class": spec.Name, "q": spec.Q, "matching_documents": spec.DocumentMatches, "expected_count": expected, "p99_ms": millis[29], "mode": mode, "p50_ms": millis[len(millis)/2], "p95_ms": millis[28], "median_vm": vms[len(vms)/2], "latencies_ms": millis, "vm_steps": vms, "errors": failures, "counts": counts, "plans": plans, "queries": statements})
		}
	}
	b, _ := json.MarshalIndent(map[string]any{"items": n, "realistic": realistic, "results": results, "boundary": "actual production search derivation on cached focused films; warm service and raw FTS isolation, no HTTP or all-kind claim"}, "", "  ")
	if err = os.WriteFile(filepath.Join(report, "search-profile.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	for _, result := range results {
		if len(result["errors"].([]string)) != 0 {
			t.Errorf("%s: %v", result["mode"], result["errors"])
		}
		for _, count := range result["counts"].([]int) {
			if count != result["expected_count"].(int) {
				t.Errorf("%s/%s: returned %d, expected %d", result["query_class"], result["mode"], count, result["expected_count"])
				break
			}
		}
	}
}
