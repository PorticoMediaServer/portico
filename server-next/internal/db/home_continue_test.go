package db

import (
	"context"
	"database/sql"
	"fmt"
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

// Home reads only as much of Continue Watching as it shows, and still shows the whole row's
// first page: here the oldest started episode is of a show whose later episode was watched
// last, so the show offers no next episode even though that started episode is not read.
func TestHomeReadsTheFirstPageOfContinueWatching(t *testing.T) {
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
	var items []ImportItem
	for show := range 50 {
		key := fmt.Sprint("show", show)
		items = append(items, ImportItem{Kind: KindShow, Title: key, Keys: []ItemKey{{Kind: KeyFolder, Key: key}}},
			ImportItem{Kind: KindSeason, Title: "Season 1", IndexNumber: &one, Keys: []ItemKey{{Kind: KeyName, Key: key + "/1"}}, Parent: &ItemKey{Kind: KeyFolder, Key: key}})
		for episode := 1; episode <= 3; episode++ {
			items = append(items, ImportItem{Kind: KindEpisode, Title: fmt.Sprint(key, "e", episode), IndexNumber: &episode, ParentIndex: &one,
				Keys: []ItemKey{{Kind: KeyFile, Key: fmt.Sprint(key, "e", episode)}}, Parent: &ItemKey{Kind: KeyName, Key: key + "/1"}})
		}
	}
	if _, err := d.Import(ctx, ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: 1, Dirs: []ImportDir{{}}, Items: items}); err != nil {
		t.Fatal(err)
	}
	episodes, err := d.Items(ctx, viewer, ItemQuery{Library: lib.PublicID, Kinds: []ItemKind{KindEpisode}, Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	id := map[string]string{}
	for _, item := range episodes.Items {
		id[item.Title] = item.PublicID
	}
	at := time.Now().Add(-24 * time.Hour).UnixMilli()
	edit := func(title string, change StateEdit) {
		t.Helper()
		at++
		if err := d.write(ctx, User, func(tx *sql.Tx) error {
			w, err := newStateWriter(ctx, tx, viewer)
			if err != nil {
				return err
			}
			w.now = at
			if _, _, err = w.apply(id[title], change); err != nil {
				return err
			}
			return settleWatched(w.batchTx, w.access, viewer.ProfileID, w.marks, at)
		}); err != nil {
			t.Fatal(err)
		}
	}
	position, yes := int64(60_000), true
	for show := range 50 { // show0 first, so its started episode is the oldest
		edit(fmt.Sprint("show", show, "e1"), StateEdit{PositionMS: &position})
	}
	edit("show0e2", StateEdit{PositionMS: &position}) // played, so it is the show's latest play
	edit("show0e2", StateEdit{Watched: &yes})
	home, err := d.HomeRowItems(ctx, viewer, "continue", ItemQuery{Limit: 40, HomeFirst: true})
	if err != nil {
		t.Fatal(err)
	}
	whole, err := d.HomeRowItems(ctx, viewer, "continue", ItemQuery{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	if len(home.Items) != 40 || home.NextCursor == "" || len(whole.Items) != 50 {
		t.Fatalf("Home %d (cursor %q), whole %d", len(home.Items), home.NextCursor, len(whole.Items))
	}
	for n, item := range home.Items {
		if item.ID != whole.Items[n].ID {
			t.Fatalf("Home's card %d is %s, the row's is %s", n, item.Title, whole.Items[n].Title)
		}
	}
	rest, err := d.HomeRowItems(ctx, viewer, "continue", ItemQuery{Limit: 200, Cursor: home.NextCursor})
	if err != nil || len(rest.Items) != 10 || rest.Items[0].ID != whole.Items[40].ID {
		t.Fatalf("See all after Home: %d, %v", len(rest.Items), err)
	}
}
