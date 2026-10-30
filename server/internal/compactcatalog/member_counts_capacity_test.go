package compactcatalog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestMemberTraitTopReadCapacity(t *testing.T) {
	sizes := []int{256}
	switch os.Getenv("PORTICO_PERFORMANCE_TIER") {
	case "release":
		sizes = []int{10000, 200000}
	case "deep":
		sizes = []int{10000, 200000, 2000000}
	}
	for _, n := range sizes {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			db, err := dbwork.OpenHandle(filepath.Join(t.TempDir(), "counts.sqlite"), dbwork.DefaultPolicy())
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
			exec(`CREATE TABLE catalog_rec_member_counts(work_id INTEGER NOT NULL,facet TEXT NOT NULL,total INTEGER NOT NULL,eligible INTEGER NOT NULL,PRIMARY KEY(work_id,facet)) WITHOUT ROWID,STRICT`)
			exec(`CREATE INDEX catalog_rec_member_best ON catalog_rec_member_counts(work_id,total DESC,facet) WHERE eligible=1`)
			for after := 0; after < n; after += 10000 {
				exec(`WITH RECURSIVE ids(i) AS(SELECT ? UNION ALL SELECT i+1 FROM ids WHERE i<?) INSERT INTO catalog_rec_member_counts SELECT 1,printf('g:%09d',i),i%5+1,1 FROM ids`, after+1, min(n, after+10000))
			}
			exec(`ANALYZE`)
			plans, err := db.Query(`EXPLAIN QUERY PLAN `+recMemberFacets, `[1]`)
			if err != nil {
				t.Fatal(err)
			}
			parts := []string{}
			for plans.Next() {
				var id, parent, unused int
				var detail string
				if err := plans.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				parts = append(parts, detail)
			}
			plans.Close()
			plan := strings.Join(parts, "; ")
			if !strings.Contains(plan, "INDEX catalog_rec_member_best (work_id=?)") {
				t.Fatalf("not an indexed work seek: %s", plan)
			}
			start := time.Now()
			rows, err := db.Query(recMemberFacets, `[1]`)
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for rows.Next() {
				var work int64
				var facet string
				if err := rows.Scan(&work, &facet); err != nil {
					t.Fatal(err)
				}
				if work != 1 || facet == "" {
					t.Fatal("wrong trait")
				}
				count++
			}
			err = rows.Err()
			rows.Close()
			elapsed := time.Since(start)
			if err != nil || count != recMemberFacetLimit {
				t.Fatalf("traits=%d error=%v", count, err)
			}
			if elapsed > 100*time.Millisecond {
				t.Fatalf("bounded traits read took %s", elapsed)
			}
			t.Logf("rows=%d top=%s traits=%d plan=%s", n, elapsed, count, plan)
		})
	}
}
