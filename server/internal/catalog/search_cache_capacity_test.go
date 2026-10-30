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

func TestSearchUsesPublishedRankInputsAtCapacity(t *testing.T) {
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
			item := c.Movie(lib, "/m/a.mkv", "Amélie", 2000)
			c.Drain()
			var fold, pid string
			if err := c.DB.QueryRow(`SELECT title_fold,entity_pid FROM catalog_search_documents WHERE entity_id=?`, item.ID).Scan(&fold, &pid); err != nil || fold != "amelie" || pid != item.Public {
				t.Fatalf("fold=%s pid=%s err=%v", fold, pid, err)
			}
			// Unrelated indexed documents need no entity fixture: the kind/library
			// tokens reject them before live-entity checks. This isolates FTS selection.
			ctx, conn, release, err := dbwork.ReserveWriterConn(context.Background(), c.DB, dbwork.ClassMaintenance)
			if err != nil {
				t.Fatal(err)
			}
			_, err = conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`)
			for off := 0; err == nil && off < n; off += 10000 {
				_, err = conn.ExecContext(ctx, `WITH RECURSIVE ids(i) AS(SELECT ? UNION ALL SELECT i+1 FROM ids WHERE i<?) INSERT INTO catalog_search_documents(entity_id,title,title_fold,entity_pid,search,normalized,kind) SELECT i+1000000000,'Noise','noise','fixture','noise','noise','k7 lnone' FROM ids`, off+1, min(n, off+10000))
			}
			_, restore := conn.ExecContext(ctx, `PRAGMA foreign_keys=ON`)
			release()
			if err != nil || restore != nil {
				t.Fatalf("seed=%v restore=%v", err, restore)
			}
			c.Exec(`ANALYZE`)
			ctx, trace := dbwork.TraceQueryPlans(context.Background())
			start := time.Now()
			out, err := New(c.DB).WithContext(ctx).Search(ctx, SearchRequest{Viewer: Viewer{Profile: "p", Fence: "f", Libraries: []string{"m"}}, Q: "AMELIE", Group: "movies", Limit: 5})
			if err != nil || len(out.Groups) != 1 || out.Groups[0].ErrorCode != "" || len(out.Groups[0].Items) != 1 || out.Groups[0].Items[0].ID != item.Public {
				t.Fatalf("out=%+v err=%v", out, err)
			}
			elapsed := time.Since(start)
			ranked := 0
			for _, q := range trace() {
				if strings.Contains(q, "WITH page AS MATERIALIZED") {
					ranked++
					if !strings.Contains(q, "d.title_fold") || !strings.Contains(q, "d.entity_pid") || strings.Contains(q, "portico_search_fold(d.title)") {
						t.Fatalf("uncached rank inputs: %s", q)
					}
				}
			}
			if ranked != 1 {
				t.Fatalf("ranked statements=%d", ranked)
			}
			t.Logf("unrelated_documents=%d elapsed=%s", n, elapsed)
			if elapsed > 250*time.Millisecond {
				t.Fatalf("selective search took %s", elapsed)
			}
		})
	}
}
