package ingestion

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// The measured operation is the real first presence quantum: source readiness,
// captured EOF, one 16-object page and its writes. Only those 16 paths need real
// files; the unseen tail isolates catalogue-size cost from fixture I/O cost.
func TestSamePathPresenceWindowGrowth(t *testing.T) {
	sizes := []int{64, 640}
	if os.Getenv("PORTICO_PATH_PRESENCE_SCALE") != "" {
		sizes = []int{10_000, 30_000, 100_000}
	}
	var firstRead, firstWrite, firstPages int64
	var firstScanRead, firstScanWrite, firstScanPages int64
	for _, size := range sizes {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			f := presenceGrowthFixture(t, size)
			for i := 1; i <= pathPresenceWindow; i++ {
				name := filepath.Join(f.root, fmt.Sprintf("File-%09d.mkv", i))
				if err := os.Link(filepath.Join(f.root, "Film (2000).mkv"), name); err != nil {
					t.Fatal(err)
				}
			}
			f.exec(`UPDATE library_sources SET health='root_changed',root_identity='previous-machine' WHERE id=?`, f.source)
			p := &pathPresence{s: f.s}
			dbwork.ResetPlans()
			ctx, cost := dbwork.Measure(dbwork.WithClass(context.Background(), dbwork.ClassMaintenance))
			if err := p.advance(ctx); err != nil {
				t.Fatal(err)
			}
			read, plans := cost(), dbwork.Plans()
			if p.current == nil || p.current.examined != pathPresenceWindow {
				t.Fatal("presence did not stop at its raw object window")
			}
			if plans.Overruns != 0 {
				t.Fatalf("presence exceeded an unchanged write plan: %+v", plans.Recent)
			}
			write, pages := plans.Largest.VMOps, plans.Largest.Pages
			t.Logf("presence objects=%d window=%d readVM=%d maxWriteVM=%d maxRows=%d maxPages=%d units=%d", size+1, pathPresenceWindow, read.VMSteps, write, plans.Largest.Rows, pages, plans.Measured)
			if firstRead == 0 {
				firstRead, firstWrite, firstPages = read.VMSteps, write, pages
			} else if read.VMSteps*2 > firstRead*3 || write*2 > firstWrite*3 || pages > firstPages*2 {
				t.Fatalf("fixed presence work grows with the unseen tail: read %d/%d, write %d/%d, pages %d/%d", read.VMSteps, firstRead, write, firstWrite, pages, firstPages)
			}
			// A queued scan preempts the background walk. Its readiness check
			// must advance only one raw stat window before any new publication.
			for i := 1; i <= pathPresenceWindow; i++ {
				if err := os.Remove(filepath.Join(f.root, fmt.Sprintf("File-%09d.mkv", i))); err != nil {
					t.Fatal(err)
				}
			}
			job := f.queue()
			f.exec(`UPDATE jobs SET status='running' WHERE id=?`, job.ID)
			source := f.librarySource()
			dbwork.ResetPlans()
			ctx, scanCost := dbwork.Measure(dbwork.WithClass(context.Background(), dbwork.ClassBackgroundMedia))
			if err := f.s.readySource(ctx, job.ID, &source); !errors.Is(err, errScanPresenceAdvanced) {
				t.Fatal("scan did not stop at its readiness window", err)
			}
			scanRead, scanPlans := scanCost(), dbwork.Plans()
			if scanPlans.Overruns != 0 || f.count(`SELECT count(*) FROM inventory_runs WHERE job_id=? AND root_rebound=1 AND discovered=0 AND reconcile_cursor=?`, job.ID, fmt.Sprintf("obj-%09d", pathPresenceWindow)) != 1 {
				t.Fatal("scan preflight exceeded its window or published new media", scanPlans)
			}
			scanWrite, scanPages := scanPlans.Largest.VMOps, scanPlans.Largest.Pages
			t.Logf("scan-presence objects=%d window=%d readVM=%d maxWriteVM=%d maxRows=%d maxPages=%d", size+1, pathPresenceWindow, scanRead.VMSteps, scanWrite, scanPlans.Largest.Rows, scanPages)
			if firstScanRead == 0 {
				firstScanRead, firstScanWrite, firstScanPages = scanRead.VMSteps, scanWrite, scanPages
			} else if scanRead.VMSteps*2 > firstScanRead*3 || scanWrite*2 > firstScanWrite*3 || scanPages > firstScanPages*2 {
				t.Fatalf("scan readiness grows with the unseen tail: read %d/%d, write %d/%d, pages %d/%d", scanRead.VMSteps, firstScanRead, scanWrite, firstScanWrite, scanPages, firstScanPages)
			}
		})
	}
}

// Hosted runs cache immutable seeded databases under the generator/schema hash.
// Every measurement uses its own copy and fresh native paths, never the cache.
func presenceGrowthFixture(t *testing.T, size int) *loopFixture {
	t.Helper()
	f := presenceFixture(t)
	seedDir := os.Getenv("PORTICO_PATH_PRESENCE_SEED_DIR")
	seed := filepath.Join(seedDir, fmt.Sprintf("presence-%d.sqlite", size))
	if seedDir != "" {
		if _, err := os.Stat(seed); err == nil {
			path := filepath.Join(t.TempDir(), "restored.sqlite")
			copyPresenceSeed(t, seed, path)
			db, err := persistence.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			if err := f.db.Close(); err != nil {
				t.Fatal(err)
			}
			f.db, f.s = db, New(db, catalog.New(db), assets.Probe{})
			if err := db.QueryRow(`SELECT s.library_id,s.id FROM library_sources s JOIN inventory_objects o ON o.source_id=s.id WHERE o.id='presence-object'`).Scan(&f.library, &f.source); err != nil {
				t.Fatal(err)
			}
			var modified int64
			if err := db.QueryRow(`SELECT modified_ns FROM inventory_objects WHERE id='presence-object'`).Scan(&modified); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(filepath.Join(f.root, "Film (2000).mkv"), time.Unix(0, modified), time.Unix(0, modified)); err != nil {
				t.Fatal(err)
			}
			f.exec(`UPDATE libraries SET root=? WHERE id=?`, f.root, f.library)
			f.exec(`UPDATE library_sources SET root=?,configured_root=? WHERE id=?`, f.root, f.root, f.source)
			f.exec(`UPDATE catalog_assets SET path=? WHERE token='presence-asset'`, filepath.Join(f.root, "Film (2000).mkv"))
			return f
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	f.exec(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<?)
 INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,analysis_state,analysis_revision)
 SELECT printf('obj-%09d',i),o.source_id,o.asset_id,o.root_incarnation,printf('File-%09d.mkv',i),'old-object','old-revision','{}',o.size,o.modified_ns,'done','old-revision'
 FROM n,inventory_objects o WHERE o.id='presence-object'`, size)
	if seedDir != "" {
		if err := os.MkdirAll(seedDir, 0700); err != nil {
			t.Fatal(err)
		}
		var busy, pages, done int
		if err := f.db.QueryRow(`PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &pages, &done); err != nil || busy != 0 {
			t.Fatal("seed checkpoint", busy, err)
		}
		var sequence int
		var name, path string
		if err := f.db.QueryRow(`PRAGMA database_list`).Scan(&sequence, &name, &path); err != nil {
			t.Fatal(err)
		}
		copyPresenceSeed(t, path, seed)
	}
	return f
}

func copyPresenceSeed(t *testing.T, source, target string) {
	t.Helper()
	in, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(out, in)
	closed := out.Close()
	if err != nil || closed != nil {
		t.Fatal("seed copy", err, closed)
	}
}
