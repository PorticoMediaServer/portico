package db_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
	"github.com/PorticoMediaServer/portico/server-next/internal/seed"
)

// An episode file moved from a show rated 8 into a show rated 18 takes the new show's rating
// in the same write: a profile limited to 8 no longer lists it, opens it or counts it.
func TestAnEpisodeMovedToAnotherShowTakesItsRating(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	root := t.TempDir()
	write := func(rel string, size int) {
		t.Helper()
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Bluey/Season 1/Bluey - S01E01.mkv", 101)
	write("Bluey/Season 1/Bluey - S01E02.mkv", 102)
	write("The Wire/Season 1/The Wire - S01E01.mkv", 103)
	lib, err := d.CreateLibrary(ctx, "Shows", db.LibraryShows, "en", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	scanner := &scan.Scanner{DB: d}
	if _, err := scanner.Scan(ctx, lib); err != nil {
		t.Fatal(err)
	}
	// Two profiles: the second is limited to 13.
	viewers, err := seed.Profiles(ctx, d, 2)
	if err != nil {
		t.Fatal(err)
	}
	everyone, limited := viewers[0], viewers[1]
	// The shows are rated, as matching would rate them, and everything under each with it.
	shows, err := d.Items(ctx, everyone, db.ItemQuery{Library: lib.PublicID, Kinds: []db.ItemKind{db.KindShow}, Limit: 50})
	if err != nil || len(shows.Items) != 2 {
		t.Fatalf("shows: %+v, %v", shows.Items, err)
	}
	for _, show := range shows.Items {
		if err := db.SetRatingAge(d, show.PublicID, map[string]int{"Bluey": 8, "The Wire": 18}[show.Title]); err != nil {
			t.Fatal(err)
		}
	}
	episodes := func(v db.Viewer) int {
		t.Helper()
		page, err := d.Items(ctx, v, db.ItemQuery{Library: lib.PublicID, Kinds: []db.ItemKind{db.KindEpisode}, Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		return len(page.Items)
	}
	if episodes(everyone) != 3 || episodes(limited) != 2 {
		t.Fatalf("before the move: %d episodes for everyone, %d for the limited profile; want 3 and 2", episodes(everyone), episodes(limited))
	}

	// One of the young show's files is renamed into the grown-up show.
	if err := os.Rename(filepath.Join(root, "Bluey/Season 1/Bluey - S01E02.mkv"), filepath.Join(root, "The Wire/Season 1/The Wire - S01E02.mkv")); err != nil {
		t.Fatal(err)
	}
	libraries, err := d.AllLibraries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stats, err := scanner.Scan(ctx, libraries[0])
	if err != nil || stats.FilesMoved != 1 || stats.ItemsNew != 0 {
		t.Fatalf("the move: %+v, %v", stats, err)
	}
	if episodes(everyone) != 3 || episodes(limited) != 1 {
		t.Fatalf("after the move: %d episodes for everyone, %d for the limited profile; want 3 and 1", episodes(everyone), episodes(limited))
	}
	if err := d.Check(ctx); err != nil {
		t.Fatal(err)
	}
}
