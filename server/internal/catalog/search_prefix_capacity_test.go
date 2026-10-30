package catalog

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestBroadPrefixesSelectBoundedScopedPages(t *testing.T) {
	sizes := []int{256}
	if os.Getenv("PORTICO_PERFORMANCE_TIER") == "release" {
		sizes = []int{10000, 200000}
	}
	if os.Getenv("PORTICO_PERFORMANCE_TIER") == "deep" {
		sizes = []int{10000, 200000, 1000000}
	}
	for _, n := range sizes {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			c := catalogtest.Open(t)
			lib := c.Library("m", "Movies", "movie", "/m")
			initial := c.Movie(lib, "/m/initial.mkv", "Title Initial", 2000)
			c.Attributes(initial.ID, "contentRating", "PG", "R")
			c.Drain()
			svc := New(c.DB)
			if _, err := svc.ClassifyPendingRatings(context.Background()); err != nil {
				t.Fatal(err)
			}
			// Use real identities and permission edges, but seed only the read model
			// under test instead of deriving the unrelated Home and recommendation tables.
			for off := 0; off < n; off += 10000 {
				c.Exec(`WITH RECURSIVE ids(i) AS(SELECT ? UNION ALL SELECT i+1 FROM ids WHERE i<?) INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,year) SELECT i+1000000,randomblob(16),?,1,printf('Title Longprefix %012d',i),'Title','T',2000 FROM ids`, off+1, min(n, off+10000), lib)
				c.Exec(`INSERT INTO catalog_item_attribute_edges(item_id,term_id,source_value,source_rowid) SELECT id,(SELECT t.id FROM catalog_attribute_terms t JOIN catalog_attribute_fields f ON f.id=t.field_id WHERE f.field='contentRating' AND t.value_key=CASE WHEN e.id%2=0 THEN 'pg' ELSE 'r' END),'fixture',e.id FROM catalog_entities e WHERE id>? AND id<=?`, 1000000+off, 1000000+min(n, off+10000))
				c.Exec(`INSERT INTO catalog_browse_rows(entity_id,library_id,kind,title,sort_key,head,year,item_id,rating_key,label_key,available,recent,backdrop,random_key) SELECT id,library_id,kind,title,sort_key,sort_head,year,id,CASE WHEN id%2=0 THEN 'pg' ELSE 'r' END,'',0,0,0,id FROM catalog_entities WHERE id>? AND id<=?`, 1000000+off, 1000000+min(n, off+10000))
				c.Exec(`INSERT INTO catalog_browse_memberships(entity_id,item_id,source) SELECT id,id,0 FROM catalog_entities WHERE id>? AND id<=?`, 1000000+off, 1000000+min(n, off+10000))

				c.Exec(`INSERT INTO catalog_search_documents(entity_id,title,title_fold,entity_pid,search,normalized,kind,library_id,entity_kind,rating_key) SELECT id,title,lower(title),pid(public_id),lower(title),lower(title),'k1 l'||library_id,library_id,kind,CASE WHEN id%2=0 THEN 'pg' ELSE 'r' END FROM catalog_entities WHERE id>? AND id<=?`, 1000000+off, 1000000+min(n, off+10000))
				c.Exec(`INSERT INTO catalog_search_prefix_terms(prefix) VALUES('ti'),('tit'),('title'),('longpre'),('longprefix'),('0'),('000000') ON CONFLICT(prefix) DO NOTHING`)
				c.Exec(`INSERT INTO catalog_search_prefix_rows(term_id,entity_id,kind,library_id,tier,container,rating_key,label_key) SELECT p.id,d.entity_id,1,d.library_id,CASE WHEN p.prefix GLOB '[0-9]*' OR p.prefix LIKE 'long%' THEN 1 ELSE 2 END,0,d.rating_key,'' FROM catalog_search_documents d CROSS JOIN catalog_search_prefix_terms p WHERE p.prefix IN('ti','tit','title','longpre','longprefix','0','000000') AND d.entity_id>? AND d.entity_id<=?`, 1000000+off, 1000000+min(n, off+10000))
			}
			// This fixture has published its narrow search model synchronously.
			c.Exec(`DELETE FROM catalog_dirty`)
			c.Exec(`ANALYZE`)
			age := 13
			for _, restricted := range []bool{false, true} {
				for _, q := range []string{"Ti", "Tit", "Title", "Title 0", "Title 000000", "Longpre", "Longprefix"} {
					viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"m"}}
					if restricted {
						viewer.Restrictions = identity.ContentRestrictions{MaximumAge: &age, BlockUnrated: true}
					}
					durations := []time.Duration{}
					for sample := 0; sample < 3; sample++ {
						ctx, trace := dbwork.TraceQueryPlans(context.Background())
						started := time.Now()
						out, err := svc.WithContext(ctx).Search(ctx, SearchRequest{Viewer: viewer, Q: q, Group: "movies", Limit: 12})
						durations = append(durations, time.Since(started))
						if err != nil || len(out.Groups) != 1 || out.Groups[0].Status != "success" || len(out.Groups[0].Items) != 12 || !out.Groups[0].HasMore {
							t.Fatalf("q=%s restricted=%v groups=%+v err=%v", q, restricted, out.Groups, err)
						}
						group := out.Groups[0]
						if numeric, ok := strings.CutPrefix(q, "Title "); ok {
							for _, item := range group.Items {
								words := strings.Fields(item.Title)
								if len(words) == 0 || !strings.HasPrefix(words[len(words)-1], numeric) {
									t.Fatalf("numeric prefix %q matched %q", numeric, item.Title)
								}
							}
						}
						if strings.Contains(q, " ") && group.TotalCountExact {
							t.Fatal("intersection claimed an exact count")
						}
						if !strings.Contains(q, " ") && (!group.TotalCountExact || group.TotalCount < n/2) {
							t.Fatalf("maintained count=%+v", group)
						}
						if restricted {
							for _, item := range group.Items {
								var id int64
								c.DB.QueryRow(`SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)`, item.ID).Scan(&id)
								if id%2 != 0 {
									t.Fatalf("hidden row %d", id)
								}
							}
						}
						for _, sql := range trace() {
							if strings.Contains(sql, "WITH page AS MATERIALIZED") {
								if !strings.Contains(sql, "catalog_search_prefix_rows") || strings.Contains(sql, "rank AS relevance") {
									t.Fatal("broad FTS ranking returned")
								}
							}
						}
						// Continue from the signed key; a tie run cannot repeat or disappear.
						next, err := svc.Search(context.Background(), SearchRequest{Viewer: viewer, Q: q, Group: "movies", Limit: 12, Cursor: group.NextCursor})
						if err != nil || next.Groups[0].Status != "success" || len(next.Groups[0].Items) != 12 {
							t.Fatalf("next=%+v err=%v", next.Groups, err)
						}
						seen := map[string]bool{}
						for _, item := range group.Items {
							seen[item.ID] = true
						}
						for _, item := range next.Groups[0].Items {
							if seen[item.ID] {
								t.Fatal("cursor repeated an identity")
							}
						}
					}
					sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
					t.Logf("rows=%d q=%q restricted=%v median=%s worst=%s", n, q, restricted, durations[1], durations[2])
					if durations[1] > 100*time.Millisecond || durations[2] > time.Second {
						t.Fatal(durations)
					}
				}
			}
			zeroAge := 0
			ctx, trace := dbwork.TraceQueryPlans(context.Background())
			started := time.Now()
			out, err := svc.WithContext(ctx).Search(ctx, SearchRequest{Viewer: Viewer{Profile: "blocked", Fence: "blocked", Libraries: []string{"m"}, Restrictions: identity.ContentRestrictions{MaximumAge: &zeroAge, BlockUnrated: true}}, Q: "Ti", Group: "movies", Limit: 12})
			elapsed := time.Since(started)
			if err != nil || len(out.Groups) != 1 || out.Groups[0].Status != "success" || len(out.Groups[0].Items) != 0 || !out.Groups[0].TotalCountExact || out.Groups[0].TotalCount != 0 || elapsed > 100*time.Millisecond {
				t.Fatalf("zero visible matches: elapsed=%s out=%+v err=%v", elapsed, out, err)
			}
			for _, query := range trace() {
				if strings.Contains(query, "WITH page AS MATERIALIZED") {
					t.Fatal("exact zero permission count still walked candidates")
				}
			}
			t.Logf("rows=%d zero-visible-prefix elapsed=%s", n, elapsed)
		})
	}
}
