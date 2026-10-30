package catalog

import (
	"context"
	"database/sql"
	"flag"
	"sort"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

var recScale = flag.Int("recscale", 0, "build a taste library of this many films and measure the recommendation engine (runner only)")

// TestRecScale measures the engine on a realistic library: what cataloguing
// costs with every derivation (recommendation postings and rarity included),
// what Recommended and Home cost for a profile with taste (cold and memoised),
// and what a bulk removal costs. Run on the runner:
//
//	runner-test-lite.sh -run TestRecScale -timeout 60m ./internal/catalog -args -recscale=100000
func TestRecScale(t *testing.T) {
	if *recScale == 0 {
		t.Skip("set -recscale=N (runner only)")
	}
	n := *recScale
	c := catalogtest.Open(t)
	start := time.Now()
	taste := catalogtest.BuildTaste(t, c, catalogtest.TasteShape{Movies: n, Shows: max(2, n/20), EpisodesPerShow: 10, Anime: max(1, n/100), Seed: 7})
	built := time.Since(start)
	var items int
	if err := c.DB.QueryRow(`SELECT count(*) FROM catalog_entities`).Scan(&items); err != nil {
		t.Fatal(err)
	}
	start = time.Now()
	c.Drain()
	drained := time.Since(start)
	t.Logf("built %d films, %d entities in %v; derived in %v (%.2f ms per entity)", n, items, built, drained, float64(drained.Milliseconds())/float64(items))
	s := New(c.DB)
	libraries := []string{"taste-movies", "taste-shows", "taste-anime"}
	r := HomeRequest{Viewer: Viewer{Profile: taste.Profiles.SciFiFan, Fence: "f", Libraries: libraries}, ServerID: "s", Profile: taste.Profiles.SciFiFan, ViewerFence: "f", Libraries: libraries, Now: time.Now()}
	measure := func(what string, runs int, reset bool, f func() error) {
		t.Helper()
		var times []time.Duration
		for i := 0; i < runs; i++ {
			if reset {
				s.state.recMu.Lock()
				s.state.recMemo = nil
				s.state.recMu.Unlock()
			}
			start := time.Now()
			if err := f(); err != nil {
				t.Fatal(what, err)
			}
			times = append(times, time.Since(start))
		}
		sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
		t.Logf("%s: p50 %v, max %v (%d runs)", what, times[len(times)/2], times[len(times)-1], runs)
	}
	var ranked []recCandidate
	measure("Recommended, cold", 7, true, func() (err error) { ranked, err = s.recRank(r); return })
	measure("Recommended, memoised", 7, false, func() (err error) { ranked, err = s.recRank(r); return })
	measure("Picks for you (all generators), cold", 5, true, func() error { _, err := s.recPersonalRows(r); return err })
	measure("Home, cold", 5, true, func() error { _, err := s.HomeRows(r); return err })
	measure("Home, memoised", 5, false, func() error { _, err := s.HomeRows(r); return err })
	t.Logf("Recommended ranked %d works", len(ranked))
	if err := compactcatalog.CheckRecPostings(context.Background(), c.DB); err != nil {
		t.Fatal(err)
	}
	if err := compactcatalog.CheckRecTaste(context.Background(), c.DB); err != nil {
		t.Fatal(err)
	}
	// Bulk removal: a tenth of the films, deleted in scan-sized transactions.
	removed := taste.Movies[:len(taste.Movies)/10]
	start = time.Now()
	for from := 0; from < len(removed); from += 500 {
		batch := removed[from:min(from+500, len(removed))]
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			for _, it := range batch {
				if err := compactcatalog.DeleteEntityTx(ctx, tx, it.ID); err != nil {
					return err
				}
			}
			return nil
		})
	}
	deleted := time.Since(start)
	start = time.Now()
	c.Drain()
	t.Logf("removed %d films: deletes %v, derivation %v", len(removed), deleted, time.Since(start))
	if err := compactcatalog.CheckRecPostings(context.Background(), c.DB); err != nil {
		t.Fatal(err)
	}
	measure("Recommended after removal, cold", 5, true, func() (err error) { ranked, err = s.recRank(r); return })
}
