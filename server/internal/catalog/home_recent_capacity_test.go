package catalog

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// A narrow serving-index fixture: retired facts can outpace the derived worker
// at any size. An empty preview must stop after its candidate budget instead of
// walking every stale row looking for a live title.
func TestRecentlyAddedBoundsExaminedCandidates(t *testing.T) {
	sizes := []int{256}
	switch os.Getenv("PORTICO_PERFORMANCE_TIER") {
	case "release":
		sizes = []int{10000, 200000}
	case "deep":
		sizes = []int{10000, 200000, 2000000}
	}
	for _, n := range sizes {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			db, err := persistence.Open(filepath.Join(t.TempDir(), "recent.sqlite"))
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
			// Install the real baseline, including retirement and policy tables.
			// Seed a deliberately stale serving projection without draining its worker.
			library := catalogtest.New(t, db).Library("m", "Movies", "movie", "/movies")
			for after := 0; after < n; after += 10000 {
				exec(`WITH RECURSIVE ids(i) AS(SELECT ? UNION ALL SELECT i+1 FROM ids WHERE i<?) INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,retired) SELECT i,randomblob(16),?,1,'Movie','Movie','M',1 FROM ids`, after+1, min(n, after+10000), library)
				exec(`INSERT INTO catalog_browse_rows(entity_id,library_id,kind,title,sort_key,head,year,recent_text,added_text,item_id,rating_key,label_key,available,recent,backdrop) SELECT id,library_id,kind,title,sort_key,sort_head,year,'2026-01-01','2026-01-01',id,'','',1,1,0 FROM catalog_entities WHERE id>? AND id<=?`, after, min(n, after+10000))
			}
			exec(`ANALYZE`)
			s := New(db)
			ctx, cost := dbwork.Measure(context.Background())
			start := time.Now()
			err = dbwork.WithReadSnapshot(ctx, db, func(ctx context.Context) error {
				found, err := s.WithContext(ctx).homeRecentWorks(HomeRequest{}, "m")
				if len(found) != 0 {
					t.Fatalf("retired titles leaked: %d", len(found))
				}
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			elapsed := time.Since(start)
			statements := cost().Statements
			if statements > 20 {
				t.Fatalf("examined the library: %d statements", statements)
			}
			if elapsed > 500*time.Millisecond {
				t.Fatalf("bounded recent preview took %s", elapsed)
			}
			t.Logf("rows=%d preview=%s statements=%d", n, elapsed, statements)
			// A live row in the same stale projection must still be found. This
			// rules out an empty fixture or a reader that always returns no works.
			exec(`UPDATE catalog_entities SET retired=0 WHERE id=?`, n)
			found, err := s.homeRecentWorks(HomeRequest{}, "m")
			if err != nil || len(found) != 1 {
				t.Fatalf("live control: %v %v", found, err)
			}
			var public string
			if err = db.QueryRow(`SELECT pid(public_id) FROM catalog_entities WHERE id=?`, n).Scan(&public); err != nil || found[0][0] != public {
				t.Fatal("wrong live work", found, public, err)
			}
		})
	}
}
