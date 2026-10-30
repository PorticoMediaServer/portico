package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// This isolates index traversal from catalogue construction. Functional block,
// restriction and cursor tests use full catalogtest fixtures; this guard fills
// the same index keys cheaply enough to exercise two million rows routinely.
func TestBrowseCompositeSeekCapacity(t *testing.T) {
	sizes := []int{256}
	switch os.Getenv("PORTICO_PERFORMANCE_TIER") {
	case "release":
		sizes = []int{10000, 200000}
	case "deep":
		sizes = []int{10000, 200000, 2000000}
	}
	for _, n := range sizes {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			dir := t.TempDir()
			if root := os.Getenv("PORTICO_SCALE_SCRATCH"); root != "" {
				var err error
				dir, err = os.MkdirTemp(root, "seek-")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.RemoveAll(dir) })
			}
			db, err := dbwork.OpenHandle(filepath.Join(dir, "seek.sqlite"), dbwork.DefaultPolicy())
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
			exec(`CREATE TABLE catalog_browse_rows(entity_id INTEGER PRIMARY KEY,library_id INTEGER NOT NULL,kind INTEGER NOT NULL,sort_key TEXT NOT NULL,year INTEGER NOT NULL,release_key TEXT NOT NULL,added_text TEXT,duration_max REAL,rating_max REAL)`)
			exec(`CREATE INDEX catalog_browse_title ON catalog_browse_rows(library_id,kind,sort_key COLLATE NOCASE,entity_id)`)
			exec(`CREATE INDEX catalog_browse_year ON catalog_browse_rows(library_id,kind,year,entity_id)`)
			exec(`CREATE INDEX catalog_browse_release_key ON catalog_browse_rows(library_id,kind,release_key,entity_id)`)
			exec(`CREATE INDEX catalog_browse_added ON catalog_browse_rows(library_id,kind,COALESCE(added_text,''),entity_id)`)
			exec(`CREATE INDEX catalog_browse_duration ON catalog_browse_rows(library_id,kind,COALESCE(duration_max,0),entity_id)`)
			exec(`CREATE INDEX catalog_browse_rating ON catalog_browse_rows(library_id,kind,COALESCE(rating_max,0),entity_id)`)
			for after := 0; after < n; after += 10000 {
				exec(`WITH RECURSIVE ids(i) AS(SELECT ? UNION ALL SELECT i+1 FROM ids WHERE i<?) INSERT INTO catalog_browse_rows SELECT i,1,1,printf('Title %09d',i),2000,'2026-01-01'  ,NULL,NULL,NULL FROM ids`, after+1, min(n, after+10000))
			}
			exec(`ANALYZE`)
			cases := []struct {
				field string
				key   any
				old   string
			}{
				{"title", fmt.Sprintf("Title %09d", n-100), `(e.sort_key COLLATE NOCASE>? OR e.sort_key COLLATE NOCASE=? AND e.entity_id>?)`},
				{"year", 2000, `(e.year>? OR e.year=? AND e.entity_id>?)`},
				{"added", "", `(COALESCE(e.added_text,'')>? OR COALESCE(e.added_text,'')=? AND e.entity_id>?)`},
				{"duration", 0, `(COALESCE(e.duration_max,0)>? OR COALESCE(e.duration_max,0)=? AND e.entity_id>?)`},
				{"communityRating", 0, `(COALESCE(e.rating_max,0)>? OR COALESCE(e.rating_max,0)=? AND e.entity_id>?)`},
				{"releaseDate", "2026-01-01", `(e.release_key>? OR e.release_key=? AND e.entity_id>?)`},
			}
			for _, tc := range cases {
				definition, _ := browseSortByID(tc.field)
				query, args := browseBoundedSQL("e.entity_id", "catalog_browse_rows e", "library_id=1 AND kind=1", nil, definition.Expression, "e.entity_id", false, false, tc.key, int64(n-100), 40, 0)
				plan := seekCapacityPlan(t, db, query, args...)
				if !strings.Contains(plan, "sort_key>?") && !strings.Contains(plan, "year>?") && !strings.Contains(plan, "release_key>?") && !strings.Contains(plan, "entity_id)>") && !strings.Contains(plan, "<expr>>?") {
					t.Fatalf("%s lacks an index range: %s", tc.field, plan)
				}
				measure := func(query string, args ...any) time.Duration {
					t.Helper()
					start := time.Now()
					rows, err := db.Query(query, args...)
					if err != nil {
						t.Fatal(err)
					}
					count := 0
					for rows.Next() {
						var id int64
						if err := rows.Scan(&id); err != nil {
							t.Fatal(err)
						}
						if id != int64(n-99+count) {
							t.Fatalf("%s row %d = %d", tc.field, count, id)
						}
						count++
					}
					err = rows.Err()
					rows.Close()
					if err != nil || count != 40 {
						t.Fatalf("rows=%d: %v", count, err)
					}
					return time.Since(start)
				}
				oldQuery := `SELECT entity_id FROM catalog_browse_rows e WHERE library_id=1 AND kind=1 AND ` + tc.old + ` ORDER BY ` + definition.Expression + `,entity_id LIMIT 40`
				before := measure(oldQuery, tc.key, tc.key, n-100)
				after := measure(query, args...)
				t.Logf("rows=%d field=%s before=%s after=%s plan=%s", n, tc.field, before, after, plan)
				if n >= 200000 && after > 50*time.Millisecond {
					t.Fatalf("indexed deep page took %s", after)
				}
			}
			var pages, free, pageSize int64
			db.QueryRow(`PRAGMA page_count`).Scan(&pages)
			db.QueryRow(`PRAGMA page_size`).Scan(&pageSize)
			var rss string
			raw, _ := os.ReadFile("/proc/self/status")
			for _, line := range strings.Split(string(raw), "\n") {
				if strings.HasPrefix(line, "VmHWM:") {
					rss = line
				}
			}
			stats, err := db.Query(`SELECT idx,stat FROM sqlite_stat1 ORDER BY idx`)
			if err != nil {
				t.Fatal(err)
			}
			for stats.Next() {
				var idx, stat string
				stats.Scan(&idx, &stat)
				t.Logf("rows=%d statistic=%s %s", n, idx, stat)
			}
			stats.Close()
			t.Logf("rows=%d database_bytes=%d process_peak=%s", n, pages*pageSize, rss)
			exec(`DELETE FROM catalog_browse_rows WHERE entity_id>?`, n/2)
			db.QueryRow(`PRAGMA freelist_count`).Scan(&free)
			t.Logf("rows=%d delete_reusable_bytes=%d", n, free*pageSize)
			exec(`VACUUM`)
			db.QueryRow(`PRAGMA page_count`).Scan(&pages)
			t.Logf("rows=%d compacted_bytes=%d", n, pages*pageSize)
		})
	}
}

func seekCapacityPlan(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(lines, "; ")
}
