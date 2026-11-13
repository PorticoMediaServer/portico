package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// A long wait between releases never loses the show's place, including across seasons.
func TestContinueWatchingReturnsWhenEpisodesArrive(t *testing.T) {
	ctx := context.Background()
	d, err := Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	viewer, err := d.CreateSeedProfile(ctx, SeedProfile{Name: "viewer"})
	if err != nil {
		t.Fatal(err)
	}
	lib, err := d.CreateLibrary(ctx, "Shows", LibraryShows, "en", []string{"/shows"})
	if err != nil {
		t.Fatal(err)
	}
	one := 1
	if _, err := d.Import(ctx, ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: 1, Dirs: []ImportDir{{}}, Items: []ImportItem{
		{Kind: KindShow, Title: "Show", Keys: []ItemKey{{Kind: KeyFolder, Key: "show"}}},
		{Kind: KindSeason, Title: "Season 1", IndexNumber: &one, Keys: []ItemKey{{Kind: KeyName, Key: "show/1"}}, Parent: &ItemKey{Kind: KeyFolder, Key: "show"}},
	}}); err != nil {
		t.Fatal(err)
	}
	arrive := func(season, episode int, key string) string {
		t.Helper()
		if _, err := d.Import(ctx, ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: 2, Dirs: []ImportDir{{}},
			Items: []ImportItem{{Kind: KindEpisode, Title: key, IndexNumber: &episode, ParentIndex: &season,
				Keys: []ItemKey{{Kind: KeyFile, Key: key}}, Parent: &ItemKey{Kind: KeyName, Key: "show/1"}}}}); err != nil {
			t.Fatal(err)
		}
		page, err := d.Items(ctx, viewer, ItemQuery{Library: lib.PublicID, Kinds: []ItemKind{KindEpisode}})
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if item.Title == key {
				return item.PublicID
			}
		}
		t.Fatal("new episode missing")
		return ""
	}
	expect := func(id string, progress int64) {
		t.Helper()
		rows, err := d.ContinueWatching(ctx, viewer)
		if err != nil || (id == "" && len(rows) != 0) || (id != "" && (len(rows) != 1 || rows[0].PublicID != id || rows[0].State.PositionMS != progress)) {
			t.Fatalf("continue watching: %+v, %v; want %q at %d", rows, err, id, progress)
		}
	}
	// Write the fixture at its original date, rather than send an expired offline edit today.
	at := time.Now().Add(-365 * 24 * time.Hour).UnixMilli()
	edit := func(id string, change StateEdit) {
		t.Helper()
		at++
		if err := d.write(ctx, User, func(tx *sql.Tx) error {
			w, err := newStateWriter(ctx, tx, viewer)
			if err != nil {
				return err
			}
			w.now = at
			_, _, err = w.apply(id, change)
			if err != nil {
				return err
			}
			return settleWatched(w.batchTx, w.access, viewer.ProfileID, w.marks, at)
		}); err != nil {
			t.Fatal(err)
		}
	}
	first, second := arrive(1, 1, "First"), arrive(1, 2, "Second")
	position, yes := int64(60_000), true
	edit(first, StateEdit{PositionMS: &position})
	expect(first, position)
	edit(first, StateEdit{Watched: &yes})
	expect(second, 0)
	edit(second, StateEdit{PositionMS: &position})
	edit(second, StateEdit{Watched: &yes})
	expect("", 0)
	next := arrive(1, 3, "Later episode")
	expect(next, 0)
	edit(next, StateEdit{PositionMS: &position})
	edit(next, StateEdit{Watched: &yes})
	expect("", 0)
	expect(arrive(2, 1, "New season"), 0)
}
