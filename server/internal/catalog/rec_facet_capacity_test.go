package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestAbandonedFacetJoinCapacity(t *testing.T) {
	sizes := []int{256}
	switch os.Getenv("PORTICO_PERFORMANCE_TIER") {
	case "release":
		sizes = []int{10000, 200000}
	case "deep":
		sizes = []int{10000, 200000, 2000000}
	}
	for _, n := range sizes {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			db, err := dbwork.OpenHandle(filepath.Join(t.TempDir(), "facets.sqlite"), dbwork.DefaultPolicy())
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			exec := func(q string, args ...any) {
				t.Helper()
				if _, err := db.Exec(q, args...); err != nil {
					t.Fatal(err)
				}
			}
			exec(`CREATE TABLE catalog_rec_facets(entity_id INTEGER NOT NULL,facet TEXT NOT NULL,PRIMARY KEY(entity_id,facet)) WITHOUT ROWID, STRICT`)
			exec(`CREATE INDEX catalog_rec_facet_titles ON catalog_rec_facets(facet,entity_id)`)
			for after := 0; after < n; after += 10000 {
				exec(`WITH RECURSIVE ids(i) AS(SELECT ? UNION ALL SELECT i+1 FROM ids WHERE i<?) INSERT INTO catalog_rec_facets SELECT i,'g:one' FROM ids`, after+1, min(n, after+10000))
			}
			q := `SELECT f.facet,json_extract(j.value,'$[1]') FROM json_each(?) j CROSS JOIN catalog_rec_facets f ON f.entity_id=CAST(json_extract(j.value,'$[0]') AS INTEGER)`
			args := []any{fmt.Sprintf("[[%d,-0.5],[%d,-0.25]]", n-1, n)}
			plan := seekCapacityPlan(t, db, q, args...)
			if !strings.Contains(plan, "SEARCH f USING PRIMARY KEY (entity_id=?)") {
				t.Fatalf("not a facet seek: %s", plan)
			}
			start := time.Now()
			rows, err := db.Query(q, args...)
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for rows.Next() {
				var facet string
				var weight float64
				if err := rows.Scan(&facet, &weight); err != nil {
					t.Fatal(err)
				}
				if facet != "g:one" {
					t.Fatal(facet)
				}
				count++
			}
			err = rows.Err()
			rows.Close()
			if err != nil || count != 2 {
				t.Fatalf("rows=%d error=%v", count, err)
			}
			elapsed := time.Since(start)
			t.Logf("rows=%d join=%s plan=%s", n, elapsed, plan)
			if elapsed > 100*time.Millisecond {
				t.Fatalf("bounded facet join took %s", elapsed)
			}
		})
	}
}
