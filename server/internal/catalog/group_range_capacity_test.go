package catalog

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Unmatched facet rows left behind while a generation catches up must not
// make a grouped filtered range walk all those rows. This isolates the join
// direction; the full mixed measurements are separate evidence.
func TestFilteredGroupRangeCapacity(t *testing.T) {
	sizes := []int{256}
	switch os.Getenv("PORTICO_PERFORMANCE_TIER") {
	case "release":
		sizes = []int{10000, 200000}
	case "deep":
		sizes = []int{10000, 200000, 2000000}
	}
	for _, n := range sizes {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			c := catalogtest.Open(t)
			lib := c.Library("m", "Movies", "movie", "/m")
			for i := 0; i < 12; i++ {
				item := c.Movie(lib, fmt.Sprintf("/m/%d.mkv", i), fmt.Sprintf("Film %02d", i), 1990+i)
				c.Genres(item.ID, "fixture", fmt.Sprintf("Genre %02d", i))
			}
			c.Drain()
			// Facet rows deliberately outnumber the live browse rows. They have no FK
			// because removed titles' contributions are retired asynchronously.
			for after := 0; after < n; after += 10000 {
				c.Exec(`WITH RECURSIVE ids(i) AS(SELECT ? UNION ALL SELECT i+1 FROM ids WHERE i<?) INSERT INTO catalog_facet_members(entity_id,field,value,library_id,rating_key,label_key) SELECT i+1000000000,'genre','Noise',?,'','' FROM ids`, after+1, min(n, after+10000), lib)
			}
			c.Exec(`ANALYZE`)
			// This is the previous implementation's count query, through the same
			// serving-view rewrite it always used, captured on this identical fixture.
			beforeSQL := compactcatalog.ServingSQL(`SELECT count(*) FROM (SELECT m.value,lower(portico_sort_title(m.value,'','')) AS name_key,count(*) AS total,max(m.recent_key) AS recent_key FROM catalog_facet_members m JOIN catalog_browse_rows e ON e.entity_id=m.entity_id WHERE m.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND m.field=? AND e.year>=? GROUP BY m.value) g WHERE 1`)
			t.Logf("rows=%d BEFORE count plan=%s", n, seekCapacityPlan(t, c.DB, beforeSQL, "m", "genre", float64(1990)))
			s := New(c.DB)
			v := Viewer{Profile: "p", Fence: "capacity", Libraries: []string{"m"}}
			for _, field := range []string{"title", "count", "recentActivity"} {
				for _, direction := range []string{"asc", "desc"} {
					r := BrowseRequest{Library: "m", Pivot: "categories", Query: &BrowseNode{Field: "year", Operator: "at-least", Value: float64(1990)}, Sort: []BrowseSortSelection{{Field: field, Direction: direction}}, Limit: 1, Range: &BrowseRange{Start: 50000}}
					// Genre groups exercise the large vocabulary path using the same engine.
					pivot := browsePivot{ID: "genres", Aggregate: "genre", EntityKinds: []string{"category"}}
					r.Profile, r.ViewerFence = v.Profile, v.Fence
					ctx, trace := dbwork.TraceQueryPlans(context.Background())
					start := time.Now()
					var page BrowseResult
					err := dbwork.WithReadSnapshot(ctx, c.DB, func(ctx context.Context) error {
						var err error
						page, err = s.WithContext(ctx).browseAggregate(r, pivot)
						return err
					})
					if err != nil || page.PageInfo.Total != 12 || page.PageInfo.Start != 11 || len(page.Entries) != 1 {
						t.Fatalf("%s/%s page=%+v err=%v", field, direction, page.PageInfo, err)
					}
					elapsed := time.Since(start)
					selects := 0
					for _, q := range trace() {
						if strings.Contains(q, "WITH groups AS MATERIALIZED") {
							selects++
							plan := seekCapacityPlan(t, c.DB, q, "m", float64(1990), "genre", 50000, 50000, 2)
							if !strings.Contains(plan, "SEARCH m") {
								t.Fatalf("facet join not a seek: %s", plan)
							}
							t.Logf("rows=%d %s/%s elapsed=%s plan=%s", n, field, direction, elapsed, plan)
						}
					}
					if selects != 1 {
						t.Fatalf("evaluated grouped range %d times", selects)
					}
					if elapsed > 250*time.Millisecond {
						t.Fatalf("bounded group read took %s", elapsed)
					}
				}
			}
		})
	}
}
