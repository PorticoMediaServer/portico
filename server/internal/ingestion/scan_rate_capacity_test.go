package ingestion

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// The cost of adding a file must not grow with the library it joins. A TV
// library grows in rounds of new show folders; each round is scanned with the
// real scanner (names only, no probing) and the derived worker drained after
// every scan quantum, as the server's own worker would run beside it. Each
// round reports the scan and derivation milliseconds per new file. The
// scanner lists every existing file on each pass by design (a timestamp alone
// does not prove a file unchanged), so the listing share is reported per
// listed file separately.
//
// PORTICO_PERFORMANCE_TIER=release|deep runs it; PORTICO_SCAN_ROUNDS and
// PORTICO_SCAN_ROUND_FILES size it (default 6 rounds of 2,000 files).
func TestScanAddRateStaysFlatAsTheLibraryGrows(t *testing.T) {
	switch os.Getenv("PORTICO_PERFORMANCE_TIER") {
	case "release", "deep":
	default:
		t.Skip("scale proof: runs with PORTICO_PERFORMANCE_TIER=release or deep")
	}
	rounds, per := envCount("PORTICO_SCAN_ROUNDS", 6), envCount("PORTICO_SCAN_ROUND_FILES", 2000)
	f := newLoopFixture(t, "tv")
	ctx := context.Background()
	worker := compactcatalog.NewWorker(f.db)
	var first, last float64
	for round := 0; round < rounds; round++ {
		for i := 0; i < per; i++ {
			n := round*per + i
			show, episode := n/20, n%20+1
			f.write(fmt.Sprintf("Show %05d (2000)/Season 01/Show %05d - S01E%02d.mkv", show, show, episode), "media")
		}
		job := f.queue()
		var scan, derive time.Duration
		quanta, units := 0, 0
		for {
			start := time.Now()
			f.s.processQuantum(ctx, job.ID, f.library)
			scan += time.Since(start)
			quanta++
			start = time.Now()
			for {
				n, err := worker.Step(ctx, compactcatalog.BatchLimit)
				if err != nil {
					t.Fatal(err)
				}
				if n == 0 {
					break
				}
				units += n
			}
			derive += time.Since(start)
			state, err := f.s.Get(job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if state.Status == "complete" || state.Status == "complete_with_warnings" {
				break
			}
			if state.Status == "failed" {
				t.Fatalf("scan failed: %+v", state)
			}
			if quanta > 100*per {
				t.Fatal("scan did not finish")
			}
		}
		total := (round + 1) * per
		if n := f.count(`SELECT count(*) FROM catalog_episodes`); n != total {
			t.Fatalf("round %d catalogued %d episodes, want %d", round, n, total)
		}
		ms := func(d time.Duration, units int) float64 {
			return float64(d) / float64(time.Millisecond) / float64(units)
		}
		perNew := ms(scan+derive, per)
		t.Logf("SCAN_ROUND library_files=%d new_files=%d scan_ms_per_new=%.3f derive_ms_per_new=%.3f total_ms_per_new=%.3f scan_ms_per_listed=%.3f quanta=%d derived_units=%d",
			total, per, ms(scan, per), ms(derive, per), perNew, ms(scan, total), quanta, units)
		if round == 0 {
			first = perNew
		}
		last = perNew
	}
	// One more file: how much derived work stands between scanning it and
	// seeing it in browse. The worker drains in id order, so anything the scan
	// queued for older titles is done first.
	f.write("Late Show (2001)/Season 01/Late Show - S01E01.mkv", "media")
	job := f.queue()
	start := time.Now()
	units, visibleAfter := 0, -1
	for {
		f.s.processQuantum(ctx, job.ID, f.library)
		for {
			n, err := worker.Step(ctx, compactcatalog.BatchLimit)
			if err != nil {
				t.Fatal(err)
			}
			if n == 0 {
				break
			}
			units += n
			if visibleAfter < 0 && f.count(`SELECT count(*) FROM catalog_browse_rows r JOIN catalog_entities e ON e.id=r.entity_id WHERE e.title='Late Show'`) > 0 {
				visibleAfter = units
			}
		}
		state, err := f.s.Get(job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if state.Status == "complete" || state.Status == "complete_with_warnings" {
			break
		}
	}
	t.Logf("SCAN_ONE_MORE library_files=%d derived_units=%d units_before_visible=%d ms=%.1f", rounds*per, units, visibleAfter, float64(time.Since(start))/float64(time.Millisecond))
	// The pass lists every existing file, so the per-new-file cost may rise
	// by the listing share; it must not rise like the library does.
	if rounds > 2 && last > 3*first {
		t.Fatalf("adding a file costs %.3f ms at %d files against %.3f ms at %d", last, rounds*per, first, per)
	}
}

func envCount(name string, fallback int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return fallback
}

// A rescan that finds every file as it was leaves the derived data alone: no
// availability, browse or library-wide work is queued. Before, each rescan
// (and each scan's running/finished health flip) re-derived every title of
// the library, so adding one file cost work in proportion to the library.
func TestUnchangedRescanQueuesNoDerivedWork(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newLoopFixture(t, "tv")
	for n := 0; n < 40; n++ {
		f.write(fmt.Sprintf("Show %05d (2000)/Season 01/Show %05d - S01E%02d.mkv", n/20, n/20, n%20+1), "media")
	}
	f.finish(f.queue())
	if err := compactcatalog.Drain(context.Background(), f.db); err != nil {
		t.Fatal(err)
	}
	f.finish(f.queue())
	if n := f.count(`SELECT count(*) FROM catalog_dirty`); n != 0 {
		rows, _ := f.db.Query(`SELECT domain,count(*) FROM catalog_dirty GROUP BY 1`)
		defer rows.Close()
		for rows.Next() {
			var domain, count int
			_ = rows.Scan(&domain, &count)
			t.Logf("domain %d: %d queued", domain, count)
		}
		t.Fatalf("an unchanged rescan queued %d derived units", n)
	}
	// A2 retains catalogue evidence across transient transport health. Neither
	// side of an offline/online flap invalidates predicates or queues item work.
	epoch := f.count(`SELECT revision FROM catalog_browse_predicate_epoch WHERE singleton=1`)
	for _, health := range []string{"offline", "healthy", "offline", "healthy"} {
		f.exec(`UPDATE library_sources SET health=? WHERE id=?`, health, f.source)
		if n := f.count(`SELECT count(*) FROM catalog_dirty`); n != 0 {
			t.Fatalf("transient health %s queued %d derived units", health, n)
		}
		if got := f.count(`SELECT revision FROM catalog_browse_predicate_epoch WHERE singleton=1`); got != epoch {
			t.Fatalf("transient health %s invalidated predicates: %d want %d", health, got, epoch)
		}
	}
	// Entering or leaving a structural refusal fences the accepted source;
	// repeating the same refusal is a no-op. The existing worker settles it.
	for _, health := range []string{"root_changed", "removing"} {
		f.exec(`UPDATE library_sources SET health=? WHERE id=?`, health, f.source)
		epoch++
		if got := f.count(`SELECT revision FROM catalog_browse_predicate_epoch WHERE singleton=1`); got != epoch {
			t.Fatalf("structural health %s did not invalidate predicates: %d want %d", health, got, epoch)
		}
		if n := f.count(`SELECT count(*) FROM catalog_dirty WHERE domain=?`, compactcatalog.DomainLibraryAvailability); n != 1 {
			t.Fatalf("structural health %s queued %d library availability units, want 1", health, n)
		}
		f.exec(`UPDATE library_sources SET health=? WHERE id=?`, health, f.source)
		if got := f.count(`SELECT revision FROM catalog_browse_predicate_epoch WHERE singleton=1`); got != epoch {
			t.Fatalf("structural no-op %s invalidated predicates", health)
		}
		f.exec(`UPDATE library_sources SET health='healthy' WHERE id=?`, f.source)
		epoch++
		if got := f.count(`SELECT revision FROM catalog_browse_predicate_epoch WHERE singleton=1`); got != epoch {
			t.Fatalf("leaving structural health %s did not invalidate predicates", health)
		}
		if err := compactcatalog.Drain(context.Background(), f.db); err != nil {
			t.Fatal(err)
		}
	}
}
