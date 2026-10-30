package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// tinyPlaylistFixture is the rehearsal shape: 100 real items, every display
// order, real progress rows and the same entry insertion the 100k fixture uses.
func tinyPlaylistFixture(t *testing.T) (dir string, db *sql.DB, service *catalog.Service) {
	t.Helper()
	dir = t.TempDir()
	opened, err := persistence.Open(filepath.Join(dir, "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { opened.Close() })
	if _, _, err = seed(opened, 100, 0, 100); err != nil {
		t.Fatal(err)
	}
	if err = compactcatalog.Drain(context.Background(), opened); err != nil {
		t.Fatal(err)
	}
	return dir, opened, catalog.New(opened)
}

func TestPlaylistPagesSortsFiltersTiesAndReorder(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	dir, db, service := tinyPlaylistFixture(t)
	playlistExercise = playlistOptions{entries: 100, limit: 7, pageBudget: 0, reorders: 6, readers: 2}
	report, err := exercisePlaylist(context.Background(), db, service, dir)
	if err != nil {
		t.Fatal(err)
	}
	if report.Entries != 100 || report.PageLimit != 7 {
		t.Fatalf("fixture shape: %+v", report)
	}
	if len(report.Sorts) != 7 {
		t.Fatalf("measured %d orders, want 7", len(report.Sorts))
	}
	pages := (100 + 7 - 1) / 7
	for _, result := range report.Sorts {
		if result.Total != 100 || result.Pages != pages || !result.ReachedLast {
			t.Fatalf("order %s walked %d/%d pages of %d", result.Route, result.Pages, pages, result.Total)
		}
		if result.FirstMS < 0 || result.DeepMS <= 0 || result.LastMS <= 0 {
			t.Fatalf("order %s did not time first/deep/last: %+v", result.Route, result)
		}
		if result.MaxStatements == 0 {
			t.Fatalf("order %s reported no statements", result.Route)
		}
	}
	if len(report.Filters) != 2 {
		t.Fatalf("measured %d filter viewers, want 2", len(report.Filters))
	}
	for _, filter := range report.Filters {
		if !filter.SumOK || !filter.ProgressSub {
			t.Fatalf("viewer %s filter counts are inconsistent: %+v", filter.Viewer, filter.Counts)
		}
	}
	open, movies := report.Filters[0], report.Filters[1]
	if open.Counts["all"] != 100 {
		t.Fatalf("open viewer sees %d occurrences, want 100", open.Counts["all"])
	}
	// Nothing is hidden from the open viewer, so the two watch states partition
	// All; a hidden occurrence would be in All only.
	if open.Counts["watched"]+open.Counts["unwatched"] != open.Counts["all"] {
		t.Fatalf("open viewer watch states do not partition All: %+v", open.Counts)
	}
	// Every occurrence is listed (hidden ones last), but only the film is
	// visible, so the watched-state filters see exactly one.
	if movies.Counts["all"] != 100 || movies.Counts["unwatched"] != 1 {
		t.Fatalf("movies-only viewer counts: %+v", movies.Counts)
	}
	for _, tie := range report.Ties {
		if !tie.OrderStable || tie.DuplicateTokens != 0 || tie.MissingTokens != 0 || tie.Walked != 100 {
			t.Fatalf("tie walk %s skipped or repeated an occurrence: %+v", tie.Sort, tie)
		}
	}
	if report.Reorder.Writes != 6 || !report.Reorder.OrderPreserved || report.Reorder.FinalEntries != 100 {
		t.Fatalf("concurrent reorder: %+v", report.Reorder)
	}
	if report.Reorder.Reads == 0 {
		t.Fatal("no reader page was attempted during reorder")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "playlist-report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var stored playlistReport
	if err = json.Unmarshal(raw, &stored); err != nil || stored.Entries != 100 {
		t.Fatalf("playlist report: %v %+v", err, stored)
	}
}

// A budget stops the walk without claiming the last page was reached.
func TestPlaylistPageBudgetIsHonest(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	dir, db, service := tinyPlaylistFixture(t)
	playlistExercise = playlistOptions{entries: 100, limit: 7, pageBudget: 3, reorders: 0, readers: 1}
	report, err := exercisePlaylist(context.Background(), db, service, dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range report.Sorts {
		if result.Pages != 3 || result.ReachedLast {
			t.Fatalf("bounded walk %s: pages=%d reachedLast=%v", result.Route, result.Pages, result.ReachedLast)
		}
	}
}

// Reuse must not duplicate entries: a second exercise reads the same fixture.
func TestPlaylistReuseIsIdempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	dir, db, service := tinyPlaylistFixture(t)
	playlistExercise = playlistOptions{entries: 100, limit: 10, pageBudget: 2, reorders: 0, readers: 1}
	if _, err := exercisePlaylist(context.Background(), db, service, dir); err != nil {
		t.Fatal(err)
	}
	report, err := exercisePlaylist(context.Background(), db, service, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Reused {
		t.Fatal("second run did not reuse the playlist")
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM catalog_playlist_entries`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 100 {
		t.Fatalf("reuse duplicated entries: %d", count)
	}
}

func TestPlaylistReorderReadersCancelAndJoinBlockedPages(t *testing.T) {
	entered, finished := make(chan struct{}, 2), make(chan struct{}, 2)
	stop := startPlaylistReorderReaders(context.Background(), 2, func(ctx context.Context) error {
		entered <- struct{}{}
		<-ctx.Done()
		finished <- struct{}{}
		return ctx.Err()
	})
	t.Cleanup(func() { _, _ = stop() })
	for range 2 {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("reader never started")
		}
	}
	counts, err := stop()
	if err != nil || counts != (playlistReadCount{n: 2}) {
		t.Fatal(counts, err)
	}
	if len(finished) != 2 {
		t.Fatal("stop returned before all reads retired")
	}
	again, err := stop()
	if err != nil || again != counts {
		t.Fatal("join is not idempotent", again, err)
	}
}

func TestPlaylistReorderReaderPanicFailsWithoutHangingJoin(t *testing.T) {
	started := make(chan struct{})
	stop := startPlaylistReorderReaders(context.Background(), 1, func(context.Context) error {
		close(started)
		panic("controlled playlist reader failure")
	})
	t.Cleanup(func() { _, _ = stop() })
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("reader never started")
	}
	if _, err := stop(); err == nil {
		t.Fatal("contained reader panic qualified the exercise")
	}
}

func TestPlaylistReorderReadersHonorParentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stop := startPlaylistReorderReaders(ctx, 2, func(context.Context) error {
		panic("cancelled reader started another page")
	})
	if counts, err := stop(); err != nil || counts != (playlistReadCount{}) {
		t.Fatal(counts, err)
	}
}
