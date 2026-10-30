package catalog

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestGenreMembershipCapacity(t *testing.T) {
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
			item := c.Movie(lib, "/m/a.mkv", "A", 2000)
			c.Genres(item.ID, "fixture", "Comédie")
			c.Drain()
			c.Exec(`INSERT INTO personal_watched_intents(profile_id,item_id,watched,authored_at) VALUES('p',?,1,'2026-10-01T00:00:00Z')`, item.ID)
			c.Exec(`INSERT INTO personal_items(profile_id,item_id,favorite,revision) VALUES('p',?,1,1)`, item.ID)
			// Noise represents unrelated member keys; it is intentionally outside the
			// entity fixture so this test isolates the member index from worker setup.
			ctx, conn, release, err := dbwork.ReserveWriterConn(context.Background(), c.DB, dbwork.ClassMaintenance)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
				release()
				t.Fatal(err)
			}
			for off := 0; off < n; off += 10000 {
				_, err = conn.ExecContext(ctx, `WITH RECURSIVE ids(i) AS(SELECT ? UNION ALL SELECT i+1 FROM ids WHERE i<?) INSERT INTO catalog_browse_memberships(entity_id,item_id,source) SELECT i+1000000000,i+1000000000,0 FROM ids`, off+1, min(n, off+10000))
				if err != nil {
					break
				}
			}
			_, restoreErr := conn.ExecContext(ctx, `PRAGMA foreign_keys=ON`)
			release()
			if err != nil || restoreErr != nil {
				t.Fatalf("noise: %v restore: %v", err, restoreErr)
			}
			c.Exec(`ANALYZE`)
			for _, node := range []*BrowseNode{
				{Field: "genre", Operator: "contains", Value: "COMEDIE"},
				{Field: "favorite", Operator: "equals", Value: true},
				{Field: "playState", Operator: "equals", Value: "played"},
			} {
				compiler := browseCompiler{profile: "p"}
				where, err := compiler.compile(node)
				if err != nil {
					t.Fatal(err)
				}
				q := `SELECT e.entity_id FROM catalog_browse_rows e WHERE ` + where
				plan := seekCapacityPlan(t, c.DB, q, compiler.args...)
				if !strings.Contains(plan, "catalog_browse_membership_item") || strings.Contains(plan, "SCAN bei") || node.Field == "genre" && !strings.Contains(plan, "catalog_term_sources_term") {
					t.Fatalf("unbounded genre membership: %s", plan)
				}
				start := time.Now()
				rows, err := c.DB.Query(q, compiler.args...)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				var got []int64
				for rows.Next() {
					var id int64
					if err = rows.Scan(&id); err != nil {
						t.Fatal(err)
					}
					got = append(got, id)
				}
				if err = rows.Err(); err != nil || len(got) != 1 || got[0] != item.ID {
					t.Fatalf("ids=%v err=%v", got, err)
				}
				elapsed := time.Since(start)
				t.Logf("field=%s members=%d elapsed=%s plan=%s", node.Field, n, elapsed, plan)
				if elapsed > 250*time.Millisecond {
					t.Fatalf("genre seek took %s", elapsed)
				}
			}
		})
	}
}
