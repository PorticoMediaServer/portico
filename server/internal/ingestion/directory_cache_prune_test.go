package ingestion

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// A rescan that finds every folder as it was marks no folder for the cache
// prune, so its sidecar phase opens no prune writer at all; a folder removed
// on disk takes its cached subtree with it, and nothing else is touched.
func TestDirectoryCachePruneVisitsOnlyChangedFolders(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newLoopFixture(t, "movie")
	for i := 0; i < 40; i++ {
		f.write(fmt.Sprintf("Shelf %02d/Film %02d (2000)/Film %02d (2000).mkv", i%4, i, i), "media")
	}
	f.write("Gone/Deep/Deeper/Film (2001).mkv", "media")
	f.finish(f.queue())
	f.exec(`CREATE TABLE prune_marks(n INTEGER NOT NULL)`)
	f.exec(`INSERT INTO prune_marks VALUES(0)`)
	f.exec(`CREATE TRIGGER prune_marked AFTER INSERT ON inventory_directory_prune BEGIN UPDATE prune_marks SET n=n+1; END`)
	cached := f.count(`SELECT count(*) FROM inventory_directory_cache`)
	f.finish(f.queue())
	if n := f.count(`SELECT n FROM prune_marks`); n != 0 {
		t.Fatalf("an unchanged rescan marked %d folders for pruning", n)
	}
	if n := f.count(`SELECT count(*) FROM inventory_directory_cache`); n != cached {
		t.Fatalf("an unchanged rescan changed the cache: %d rows, was %d", n, cached)
	}
	if err := os.RemoveAll(filepath.Join(f.root, "Gone")); err != nil {
		t.Fatal(err)
	}
	f.finish(f.queue())
	if n := f.count(`SELECT count(*) FROM inventory_directory_cache WHERE relative_path='Gone' OR relative_path LIKE 'Gone/%'`); n != 0 {
		t.Fatalf("%d cached folders of a removed subtree remain", n)
	}
	if n := f.count(`SELECT count(*) FROM inventory_directory_cache`); n != cached-3 {
		t.Fatalf("the cache holds %d rows, want %d", n, cached-3)
	}
	if n := f.count(`SELECT n FROM prune_marks`); n == 0 || n > 4 {
		t.Fatalf("removing one subtree marked %d folders", n)
	}
	if n := f.count(`SELECT count(*) FROM inventory_directory_prune`); n != 0 {
		t.Fatalf("%d prune keys outlived the run", n)
	}
}

// pruneFixture is a running scan whose folder "P" was listed done at
// revision r1, with children cached under an older revision and a
// grandchild under one of them.
type pruneFixture struct {
	*loopFixture
	job    string
	source catalog.LibrarySource
}

func newPruneFixture(t *testing.T, children int) pruneFixture {
	t.Helper()
	f := newLoopFixture(t, "movie")
	job := f.queue().ID
	f.exec(`UPDATE jobs SET status='running' WHERE id=?`, job)
	source, err := f.s.catalog.LibrarySource(context.Background(), f.source)
	if err != nil {
		t.Fatal(err)
	}
	f.exec(`INSERT INTO inventory_directory_cache(source_id,incarnation,relative_path,parent_path,revision,seen_job) VALUES(?,?,'P','.','r1',?)`, f.source, source.Incarnation, job)
	f.exec(`WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i+1 FROM n WHERE i<?-1)
 INSERT INTO inventory_directory_cache(source_id,incarnation,relative_path,parent_path,parent_revision,seen_job) SELECT ?,?,printf('P/c%05d',i),'P','r0','old' FROM n`, children, f.source, source.Incarnation)
	f.exec(`INSERT INTO inventory_directory_cache(source_id,incarnation,relative_path,parent_path,parent_revision,seen_job) VALUES(?,?,'P/c00001/g','P/c00001','r0','old')`, f.source, source.Incarnation)
	f.exec(`INSERT OR REPLACE INTO inventory_directories(job_id,relative_path,revision,state) VALUES(?,'P','r1','done')`, job)
	p := pruneFixture{f, job, source}
	p.mark("P")
	return p
}

func (p pruneFixture) mark(parent string) {
	p.t.Helper()
	if err := dbwork.WithWriteTx(context.Background(), p.db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		return catalog.MarkDirectoryPruneTx(context.Background(), tx, p.source.ID, p.source.Incarnation, parent, p.job)
	}); err != nil {
		p.t.Fatal(err)
	}
}

// drain runs prune turns until none is due and returns the most steps one
// turn read.
func (p pruneFixture) drain() (most int64, turns int) {
	p.t.Helper()
	for {
		ctx, cost := dbwork.Measure(context.Background())
		more, err := p.s.pruneDirectoryCachePage(ctx, p.job, p.source)
		if err != nil {
			p.t.Fatal(err)
		}
		most = max(most, cost().VMSteps)
		if !more {
			return most, turns
		}
		turns++
		if turns > 10000 {
			p.t.Fatal("the prune did not finish")
		}
	}
}

// A protected child met in the first window keeps its folder's key past a
// clean last window: the key is deferred, not dropped, and the next run looks
// at the folder once more and removes the child once nothing protects it.
// Every other stale child, and the grandchild under a removed one, goes.
func TestDirectoryCachePruneKeepsProtectionSeenOnAnEarlierWindow(t *testing.T) {
	p := newPruneFixture(t, 3*directoryPruneWindow+5)
	p.exec(`INSERT INTO inventory_scan_issues(job_id,relative_path,reason,protect) VALUES(?,'P/c00000','directory_changed',1)`, p.job)
	p.drain()
	if n := p.count(`SELECT count(*) FROM inventory_directory_cache WHERE parent_path='P'`); n != 1 {
		t.Fatalf("%d children of P remain, want only the protected one", n)
	}
	if n := p.count(`SELECT count(*) FROM inventory_directory_cache WHERE relative_path='P/c00000'`); n != 1 {
		t.Fatal("the protected child was removed")
	}
	if n := p.count(`SELECT count(*) FROM inventory_directory_cache WHERE relative_path='P/c00001/g'`); n != 0 {
		t.Fatal("the grandchild under a removed folder was kept")
	}
	if n := p.count(`SELECT count(*) FROM inventory_directory_prune WHERE parent_path='P' AND state='deferred'`); n != 1 {
		t.Fatal("the folder that met a protected child was not deferred")
	}
	if n := p.count(`SELECT count(*) FROM inventory_directory_prune`); n != 1 {
		t.Fatalf("%d keys remain, want the one deferred", n)
	}
	// This run does not look at the deferred folder again.
	if more, err := p.s.pruneDirectoryCachePage(context.Background(), p.job, p.source); err != nil || more {
		t.Fatalf("a deferred key was drained again in its own run: %v %v", more, err)
	}
	// The next run lists P again with nothing protected.
	next := p.job + "-next"
	p.exec(`INSERT INTO jobs(id,library_id,status,created_at) SELECT ?,library_id,'running',created_at FROM jobs WHERE id=?`, next, p.job)
	p.exec(`INSERT INTO inventory_runs(job_id,source_id,source_generation,root_incarnation,root_identity,policy_revision,phase) SELECT ?,source_id,source_generation,root_incarnation,root_identity,policy_revision,phase FROM inventory_runs WHERE job_id=?`, next, p.job)
	p.exec(`UPDATE jobs SET status='complete' WHERE id=?`, p.job)
	p.exec(`INSERT INTO inventory_directories(job_id,relative_path,revision,state) VALUES(?,'P','r1','done')`, next)
	p.job = next
	p.drain()
	if n := p.count(`SELECT count(*) FROM inventory_directory_cache WHERE parent_path='P'`); n != 0 {
		t.Fatalf("%d children of P remain after the next run", n)
	}
	if n := p.count(`SELECT count(*) FROM inventory_directory_prune`); n != 0 {
		t.Fatalf("%d keys remain after the next run", n)
	}
}

// A folder's children are read a window at a time: one turn costs the same
// for a folder of a few hundred children as for one four times the size, and
// children the folder still holds are read once, not once per removal.
func TestDirectoryCachePruneTurnCostDoesNotGrowWithTheFolder(t *testing.T) {
	cost := func(children int) int64 {
		p := newPruneFixture(t, children)
		// Every other child was listed again in this run and stays.
		p.exec(`UPDATE inventory_directory_cache SET parent_revision='r1',seen_job=? WHERE parent_path='P' AND CAST(substr(relative_path,4) AS INTEGER)%2=0`, p.job)
		most, turns := p.drain()
		if n := p.count(`SELECT count(*) FROM inventory_directory_cache WHERE parent_path='P'`); n != (children+1)/2 {
			t.Fatalf("%d children of P remain, want %d", n, (children+1)/2)
		}
		t.Logf("%d children: %d turns, at most %d steps in one", children, turns, most)
		return most
	}
	small, large := cost(300), cost(1200)
	if large > small*6/5 {
		t.Fatalf("one prune turn grew with the folder: %d steps at 1200 children against %d at 300", large, small)
	}
}
