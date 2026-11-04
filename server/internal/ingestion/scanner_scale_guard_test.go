//go:build linux

package ingestion

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// M5 measures real inventory and canonical name publication on File List Only.
// It deliberately does not claim analysis acceptance at 10k scale. The real
// probe process guard and the separate 2k server acceptance cover that boundary.
func TestScannerTenThousandInventoryAndNames(t *testing.T) {
	if testing.Short() {
		t.Skip("nightly scanner scale guard")
	}
	scannerInventoryGuard(t, 10000)
}

// The same guard with a small cohort is suitable for focused local iteration.
func TestScannerInventoryGuardSmall(t *testing.T) {
	scannerInventoryGuard(t, 192)
}

func scannerInventoryGuard(t *testing.T, total int) {
	t.Helper()
	var inventoryTime time.Duration
	for index, kind := range []string{"movie", "tv", "music"} {
		files := total / 3
		if index < total%3 {
			files++
		}
		t.Run(kind, func(t *testing.T) {
			f := newLoopFixture(t, kind)
			for n := range files {
				var path string
				switch kind {
				case "movie":
					path = fmt.Sprintf("Film %06d (2000)/Film %06d (2000).mkv", n, n)
				case "tv":
					show, episode := n/20, n%20+1
					path = fmt.Sprintf("Show %05d (2000)/Season 01/Show %05d - S01E%02d - Episode %06d.mkv", show, show, episode, n)
				case "music":
					artist, album, track := n/50, n/10, n%10+1
					path = fmt.Sprintf("Artist %05d/Album %05d/%02d - Track %06d.mp3", artist, album, track, n)
				}
				f.write(path, "")
			}
			helper, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			store := storage.New(helper)
			f.s.SetStorage(store)
			f.s.catalog.SetStorage(store)
			job := f.queue()
			source := startInventorySource(t, f, job)
			before := processStarts(store)
			ctx, cost := dbwork.Measure(context.Background())
			started := time.Now()
			turns := 0
			for {
				var pending bool
				if err := f.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_directories WHERE job_id=? AND state='pending')`, job.ID).Scan(&pending); err != nil {
					t.Fatal(err)
				}
				if !pending {
					break
				}
				if turns > 2*files+100 || time.Since(started) > 90*time.Second {
					t.Fatal("inventory exceeded its finite turn or time bound")
				}
				if err := f.s.inventoryPage(ctx, job.ID, source); err != nil {
					t.Fatal(err)
				}
				turns++
			}
			took, measured := time.Since(started), cost()
			inventoryTime += took
			processes := processStarts(store) - before
			t.Logf("SCANNER_INVENTORY kind=%s files=%d turns=%d transactions=%d statements=%d counted_vm=%d helpers=%d seconds=%.3f", kind, files, turns, measured.Transactions, measured.Statements, measured.VMSteps, processes, took.Seconds())
			if processes != 0 {
				t.Fatalf("local inventory started %d helper processes", processes)
			}
			// Leave headroom for folder/EOF work and adaptive pacing, while a
			// return to one transaction per file necessarily exceeds the budget.
			if budget := int64(8 + 3*files/4); measured.Transactions > budget {
				t.Fatalf("per-file transaction regression: %d transactions for %d files, budget %d", measured.Transactions, files, budget)
			}
			if n := f.count(`SELECT count(*) FROM inventory_objects WHERE source_id=? AND retired=0`, f.source); n != files {
				t.Fatalf("inventory accepted %d of %d files", n, files)
			}
			leaf := compactcatalog.Movie
			if kind == "tv" {
				leaf = compactcatalog.Episode
			} else if kind == "music" {
				leaf = compactcatalog.Track
			}
			if n := f.count(`SELECT count(*) FROM catalog_entities e JOIN catalog_libraries l ON l.id=e.library_id WHERE l.library_id=? AND e.kind=? AND e.retired=0 AND e.title<>''`, f.library, leaf); n != files {
				t.Fatalf("canonical names published %d of %d leaves", n, files)
			}
		})
	}
	t.Logf("SCANNER_INVENTORY_TOTAL files=%d inventory_seconds=%.3f", total, inventoryTime.Seconds())
	if inventoryTime > 90*time.Second {
		t.Fatalf("inventory and name publication took %s, budget 90s", inventoryTime)
	}
}
