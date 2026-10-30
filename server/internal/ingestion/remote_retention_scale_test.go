package ingestion

import (
	"context"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Remote listing retention keeps one current listing per cached directory
// and runs for every folder every minute. Each turn looks at a bounded window
// of the folder's listings and probes the directory cache by key, so a turn
// costs the same at twice the directories and a whole pass grows with the
// directories, not with their square.
func TestRemoteListingRetentionCostIsLinear(t *testing.T) {
	cost := func(n int) (most, total int64) {
		f := newLoopFixture(t, "movie", "Film (2000).mkv")
		f.finish(f.queue())
		old := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339Nano)
		f.exec(`INSERT INTO jobs(id,library_id,status,created_at) VALUES('remote-old',?,'complete',?)`, f.library, old)
		f.exec(`INSERT INTO inventory_runs(job_id,source_id,source_generation,root_incarnation,root_identity,policy_revision,phase,finished_at) SELECT 'remote-old',id,generation,incarnation,root_identity,1,'complete',? FROM library_sources WHERE id=?`, old, f.source)
		f.exec(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<?)
 INSERT INTO inventory_directory_cache(source_id,incarnation,relative_path,parent_path) SELECT s.id,s.incarnation,printf('d%06d',i),'.' FROM n CROSS JOIN library_sources s WHERE s.id=?`, n, f.source)
		f.exec(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<?)
 INSERT INTO remote_listing_sessions(id,job_id,source_id,generation,directory,ready,created_at,inventory_source_id,incarnation,relative_path)
 SELECT printf('list-%06d',i),'remote-old','adapter','g',s.root||'/'||printf('d%06d',i),1,?,s.id,s.incarnation,printf('d%06d',i) FROM n CROSS JOIN library_sources s WHERE s.id=?`, n, time.Now().Add(-48*time.Hour).Unix(), f.source)
		for turns := 0; ; turns++ {
			ctx, measured := dbwork.Measure(context.Background())
			more, err := f.s.PruneSource(ctx, f.source)
			if err != nil {
				t.Fatal(err)
			}
			steps := measured().VMSteps
			most, total = max(most, steps), total+steps
			if !more {
				break
			}
			if turns > n {
				t.Fatal("retention did not reach the end of the listings")
			}
		}
		if kept := f.count(`SELECT count(*) FROM remote_listing_sessions`); kept != n {
			t.Fatalf("retention removed %d current listings", n-kept)
		}
		t.Logf("%d directories: %d steps, at most %d in one turn", n, total, most)
		return most, total
	}
	smallMost, smallTotal := cost(1000)
	largeMost, largeTotal := cost(2000)
	if largeMost > smallMost*6/5 {
		t.Fatalf("one retention turn grew with the directories: %d steps at 2000 against %d at 1000", largeMost, smallMost)
	}
	if largeTotal > smallTotal*5/2 {
		t.Fatalf("a retention pass is not linear: %d steps at 2000 directories against %d at 1000", largeTotal, smallTotal)
	}
}
