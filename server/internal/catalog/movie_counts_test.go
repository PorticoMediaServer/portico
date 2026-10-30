package catalog

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestRestrictedMovieClassTracksCountsAndDecades(t *testing.T) {
	c := catalogtest.Open(t)
	db := c.DB
	library := c.Library("a", "A", "movie", "/a")
	g := c.Movie(library, "/a/g.mkv", "G Movie", 2001)
	r := c.Movie(library, "/a/r.mkv", "R Movie", 2002)
	tag := c.Movie(library, "/a/tag.mkv", "Tagged", 2011)
	c.Movie(library, "/a/u.mkv", "Unrated", 2012)
	c.Attributes(g.ID, "contentRating", "G")
	c.Attributes(r.ID, "contentRating", "R")
	c.Attributes(tag.ID, "contentRating", "PG-13")
	c.Attributes(tag.ID, "tag", "Violence")
	c.Drain()
	s := New(db)
	if _, err := s.ClassifyPendingRatings(context.Background()); err != nil {
		t.Fatal(err)
	}
	viewer := Viewer{Profile: "p", Libraries: []string{"a"}, Restrictions: restrictionOf(ceiling(13), true).Narrow(13, true, []string{"violence"}, 0)}
	rq := ContentRequest{Viewer: viewer, Library: "a"}
	check := func(wantTotal int, wantCategories map[string]int) {
		t.Helper()
		c.Drain()
		if err := s.RebuildVisibilityClass(context.Background(), "a", viewer.EffectiveRestrictions()); err != nil {
			t.Fatal(err)
		}
		c.Drain()
		total, err := s.contentMovieCount(rq)
		categories, err2 := s.contentCategories(rq)
		if err != nil || err2 != nil || total != wantTotal {
			t.Fatalf("total %d want %d: %v %v", total, wantTotal, err, err2)
		}
		got := map[string]int{}
		for _, category := range categories {
			got[category.ID] = category.Count
		}
		if !reflect.DeepEqual(got, wantCategories) {
			t.Fatalf("categories %v want %v", got, wantCategories)
		}
		summary, err := s.browseSummarise("a", []string{"movie"}, viewer.EffectiveRestrictions())
		if err != nil || !summary.exact || summary.total != wantTotal || len(summary.letter) == 0 {
			t.Fatalf("browse summary %+v: %v", summary, err)
		}
	}
	check(1, map[string]int{"decade:2000": 1})
	c.Attributes(tag.ID, "tag")
	check(2, map[string]int{"decade:2000": 1, "decade:2010": 1})
	c.Attributes(r.ID, "contentRating", "G")
	if _, err := s.ClassifyPendingRatings(context.Background()); err != nil {
		t.Fatal(err)
	}
	check(3, map[string]int{"decade:2000": 2, "decade:2010": 1})
	c.Fields(g.ID, map[string]any{"year": 2021})
	check(3, map[string]int{"decade:2000": 1, "decade:2010": 1, "decade:2020": 1})
	c.Delete(g.ID)
	check(2, map[string]int{"decade:2000": 1, "decade:2010": 1})
}

func TestRestrictedMovieCategoriesUseClassGenreStudioAndPosters(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("films", "Films", "movie", "/films")
	allowed := c.Movie(library, "/films/allowed.mkv", "Alpha", 1999)
	blocked := c.Movie(library, "/films/blocked.mkv", "Beta", 1999)
	for _, row := range []struct {
		item   catalogtest.Item
		poster string
		rating string
	}{{allowed, "/allowed", "G"}, {blocked, "/blocked", "R"}} {
		c.Fields(row.item.ID, map[string]any{"poster_url": row.poster})
		c.Genres(row.item.ID, "tmdb", "Drama")
		c.Attributes(row.item.ID, "contentRating", row.rating)
		c.Attributes(row.item.ID, "studio", "Studio")
	}
	c.Drain()
	s := New(c.DB)
	if _, err := s.ClassifyPendingRatings(context.Background()); err != nil {
		t.Fatal(err)
	}
	viewer := Viewer{Profile: "p", Libraries: []string{"films"}, Restrictions: restrictionOf(ceiling(13), true)}
	c.Drain()
	if err := s.RebuildVisibilityClass(context.Background(), "films", viewer.EffectiveRestrictions()); err != nil {
		t.Fatal(err)
	}
	c.Drain()
	got, err := s.Categories(viewer, "films")
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, category := range got {
		counts[category.ID] = category.Count
		if category.ID == "genre:Drama" || category.ID == "studio:Studio" {
			if !reflect.DeepEqual(category.ArtworkPaths, []string{"/allowed"}) {
				t.Fatalf("restricted artwork: %+v", category)
			}
		}
	}
	if !reflect.DeepEqual(counts, map[string]int{"decade:1990": 1, "genre:Drama": 1, "studio:Studio": 1}) {
		t.Fatalf("restricted categories: %v", counts)
	}
	content, err := s.contentCategories(ContentRequest{Viewer: viewer, Library: "films"})
	if err != nil || !reflect.DeepEqual(content, got) {
		t.Fatalf("content categories differ: %+v / %+v: %v", content, got, err)
	}
	c.Fields(allowed.ID, map[string]any{"title": "Alpha Updated"})
	// A class one revision behind serves its last published categories.
	if _, err := s.Categories(viewer, "films"); err != nil {
		t.Fatalf("a stale class failed categories: %v", err)
	}
	if _, err := s.contentCategories(ContentRequest{Viewer: viewer, Library: "films"}); err != nil {
		t.Fatalf("a stale class failed content categories: %v", err)
	}
}

func TestFilteredMovieCategoriesUseCompactMembership(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("films", "Films", "movie", "/films")
	a := c.Movie(library, "/films/a.mkv", "Alpha", 1999)
	b := c.Movie(library, "/films/b.mkv", "Beta", 2001)
	for _, item := range []catalogtest.Item{a, b} {
		c.Fields(item.ID, map[string]any{"poster_url": "/" + item.Public})
		c.Genres(item.ID, "tmdb", "Drama")
		c.Attributes(item.ID, "studio", "Studio")
	}
	collection := c.Collection(library, "Set", a)
	c.Drain()
	s := New(c.DB)
	r := ContentRequest{Viewer: Viewer{Libraries: []string{"films"}}, Library: "films", Q: "Al", EntityID: collection.Public}
	got, err := s.contentCategories(r)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, category := range got {
		counts[category.ID] = category.Count
		if !reflect.DeepEqual(category.ArtworkPaths, []string{"/" + a.Public}) {
			t.Fatalf("filtered poster: %+v", category)
		}
	}
	if !reflect.DeepEqual(counts, map[string]int{"decade:1990": 1, "genre:Drama": 1, "studio:Studio": 1}) {
		t.Fatalf("filtered categories: %v", counts)
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetCollectionMemberTx(ctx, tx, collection.ID, b.ID, "", true)
	})
	c.Drain()
}

func TestMovieCountProjectionTracksMutationsAndRollback(t *testing.T) {
	c := catalogtest.Open(t)
	a := c.Library("a", "A", "movie", "/a")
	b := c.Library("b", "B", "movie", "/b")
	i := c.Movie(a, "/a/i.mkv", "I", 1999)
	j := c.Movie(a, "/a/j.mkv", "J", 0)
	c.Fields(i.ID, map[string]any{"added_text": nil})
	c.Fields(j.ID, map[string]any{"added_text": "2026-01-01T00:00:00.000Z"})
	c.Drain()
	s := New(c.DB)
	check := func(lib string, total, recent int) {
		t.Helper()
		c.Drain()
		n, err := s.contentMovieCount(ContentRequest{Library: lib})
		r, err2 := s.contentDiscoveryCount(lib, "", "recently_added")
		if err != nil || err2 != nil || n != total || r != recent {
			t.Fatal(lib, n, r, err, err2)
		}
	}
	check("a", 2, 1)
	c.Fields(i.ID, map[string]any{"year": 2000, "added_text": "2026-01-02T00:00:00.000Z"})
	c.Drain()
	categories, err := s.Categories(Viewer{Libraries: []string{"a"}}, "a")
	if err != nil || len(categories) != 1 || categories[0].ID != "decade:2000" || categories[0].Count != 1 {
		t.Fatal(categories, err)
	}
	check("a", 2, 2)
	sentinel := errors.New("rollback")
	err = dbwork.WithWriteTx(context.Background(), c.DB, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		if err := compactcatalog.DeleteEntityTx(context.Background(), tx, i.ID); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatal("rollback transaction", err)
	}
	check("a", 2, 2)
	c.Delete(i.ID)
	moved := c.Movie(b, "/b/i.mkv", "I", 2000)
	artist := c.Artist(a, "Artist")
	album := c.Album(artist, "Release", 2020)
	c.Song(album, 1, "/a/j.flac", "J")
	c.Delete(j.ID)
	check("a", 0, 0)
	check("b", 1, 1)
	c.Delete(moved.ID)
	check("b", 0, 0)
}

func TestContinueCountCacheFollowsProgressAndSourceRevisions(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("a", "A", "movie", "/a")
	item := c.Movie(library, "/a/i.mkv", "I", 2020)
	c.Drain()
	db := c.DB
	s := New(db)
	insertProgress := func() {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES('p',?,30000,0,'session')`, item.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO progress_activity(profile_id,library_id,item_id,updated_at,state) VALUES('p','a',?,'now','paused')`, item.ID); err != nil {
			t.Fatal(err)
		}
	}
	insertProgress()
	check := func(profile string, want int) {
		t.Helper()
		settleCompact(t, db)
		for n := 0; n < 2; n++ {
			count, err := s.continueCount("a", profile)
			if err != nil || count != want {
				t.Fatal(count, want, err)
			}
		}
	}
	check("p", 1)
	check("other", 0)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetAvailableTx(ctx, tx, item.Asset, false)
	})
	check("p", 0)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetAvailableTx(ctx, tx, item.Asset, true)
	})
	check("p", 1)
	if _, err := db.Exec(`UPDATE progress_activity SET state='ended' WHERE item_id=?`, item.ID); err != nil {
		t.Fatal(err)
	}
	check("p", 0)
}

func TestMovieCountProjectionPersistsAcrossReopens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	db, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	c := catalogtest.New(t, db)
	library := c.Library("a", "A", "movie", "/a")
	historical := c.Movie(library, "/a/i.mkv", "Historical", 2000)
	known := c.Movie(library, "/a/j.mkv", "Known", 2010)
	c.Fields(historical.ID, map[string]any{"added_text": nil})
	c.Fields(known.ID, map[string]any{"added_text": "2026-01-01T00:00:00.000Z"})
	c.Drain()
	db.Close()
	// Reopening the numbered baseline preserves exact counts without rebuilding.
	for n := 0; n < 2; n++ {
		db, err = persistence.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		s := New(db)
		total, err := s.contentMovieCount(ContentRequest{Library: "a"})
		recent, err2 := s.contentDiscoveryCount("a", "", "recently_added")
		if err != nil || err2 != nil || total != 2 || recent != 1 {
			t.Fatal("migration duplicated or fabricated", total, recent, err, err2)
		}
		db.Close()
	}
}

func TestMovieSeekPreservesCaseInsensitiveTiesBothDirections(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("a", "A", "movie", "/a")
	for i, title := range []string{"same", "SAME", "Same", "Zed"} {
		c.Movie(library, "/a/"+string(rune('1'+i))+".mkv", title, 0)
	}
	c.Drain()
	s := New(c.DB)
	for _, direction := range []string{"asc", "desc"} {
		r := ContentRequest{Viewer: Viewer{Fence: "f", Libraries: []string{"a"}}, Library: "a", ViewerFence: "f", View: "browse", Direction: direction, Limit: 1}
		seen := map[string]bool{}
		for {
			p, err := s.Content(r)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range p.Sections[0].Entries {
				if seen[entry.ID] {
					t.Fatal("duplicate tie", direction, entry.ID)
				}
				seen[entry.ID] = true
			}
			r.Cursor = p.Sections[0].NextCursor
			if r.Cursor == "" {
				break
			}
		}
		if len(seen) != 4 {
			t.Fatal("lost tie", direction, seen)
		}
	}
}

func TestContinueDismissalIsProfileScopedAndReturnsOnlyOnReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	db, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	c := catalogtest.New(t, db)
	library := c.Library("a", "A", "movie", "/a")
	item := c.Movie(library, "/a/i.mkv", "I", 2020)
	if _, err = db.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES('p',?,30000,0,'session'),('q',?,40000,0,'other')`, item.ID, item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO progress_activity(profile_id,library_id,item_id,updated_at,state) VALUES('p','a',?,'now','paused'),('q','a',?,'now','paused')`, item.ID, item.ID); err != nil {
		t.Fatal(err)
	}
	settleCompact(t, db)
	s := New(db)
	check := func(profile string, want int) {
		t.Helper()
		n, err := s.continueCount("a", profile)
		if err != nil || n != want {
			t.Fatal(n, want, err)
		}
	}
	check("p", 1)
	yes := true
	mutation := PersonalMutation{OperationID: "dismiss", ContinueDismissed: &yes}
	state, err := s.SetPersonal("a", "p", item.Public, mutation, nil)
	if err != nil || !state.ContinueDismissed || state.ProgressSeconds != 30 || state.Watched {
		t.Fatal(state, err)
	}
	check("p", 0)
	check("q", 1)
	if _, err = s.SetPersonal("a", "p", item.Public, mutation, nil); err != nil {
		t.Fatal("receipt", err)
	}
	if _, err = db.Exec(`UPDATE progress SET position=45000 WHERE profile_id='p' AND item_id=?`, item.ID); err != nil {
		t.Fatal(err)
	}
	check("p", 0)
	db.Close()
	db, err = persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s = New(db)
	check("p", 0)
	if _, err = db.Exec(`UPDATE progress SET position=10000,playback_id='replay' WHERE profile_id='p' AND item_id=?`, item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE progress_activity SET updated_at='replay' WHERE profile_id='p' AND item_id=?`, item.ID); err != nil {
		t.Fatal(err)
	}
	check("p", 1)
	state, err = s.Personal("p", item.Public)
	if err != nil || state.ContinueDismissed {
		t.Fatal(state, err)
	}
	db.Close()
}
