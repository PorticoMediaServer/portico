package catalog

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func showRecDB(t *testing.T) (*sql.DB, catalogtest.Names) {
	t.Helper()
	db, e := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	c := catalogtest.New(t, db)
	tv := c.Library("tv", "TV", "tv", "/tv")
	shows := []struct{ id, title string }{{"s1", "Alpha"}, {"s2", "Beta"}, {"s3", "Gamma"}, {"s4", "Lonely"}}
	names := catalogtest.Names{}
	showItems := map[string]catalogtest.Item{}
	for _, s := range shows {
		show := c.Show(tv, s.title, 2020)
		showItems[s.id] = show
		names[s.id] = show
	}
	episodes := []struct{ item, show, genre string }{{"e1", "s1", "Drama"}, {"e2", "s2", "Drama"}, {"e3", "s3", "Comedy"}}
	for _, v := range episodes {
		season := c.Season(showItems[v.show], 1)
		episode := c.Episode(showItems[v.show], season, 1, "/"+v.item+".mkv")
		c.Genres(episode.ID, "tvdb", v.genre)
	}
	c.Drain()
	return db, names
}

// Spec — Page Content §2: a show page gets the same library-local "More like
// this" engine as movies, seeded by its own episodes.
func TestShowRecommendationsShareFacetsNotTitles(t *testing.T) {
	db, names := showRecDB(t)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"tv"}}
	rows, _, err := New(db).ShowRecommendationRows(viewer, names["s1"].Public, 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Relation != "more_like" || rows[0].Title != "More like Alpha" || rows[0].Endpoint != "/v1/shows/"+names["s1"].Public+"/recommendations/rows/"+rows[0].ID {
		t.Fatalf("show row: %+v", rows)
	}
	if len(rows[0].Entries) != 1 || rows[0].Entries[0].ID != names["s2"].Public || rows[0].Entries[0].Kind != "show" || rows[0].Entries[0].Title != "Beta" {
		t.Fatalf("related shows: %+v", rows[0].Entries)
	}
	// The show's row continues through the show's own row route.
	page, err := New(db).ShowRecommendationRow(viewer, names["s1"].Public, rows[0].ID, "", 1)
	if err != nil || page.ID != rows[0].ID || page.Title != "More like Alpha" || len(page.Entries) != 1 || page.Entries[0].ID != names["s2"].Public || page.HasMore {
		t.Fatalf("show row page: %+v %v", page, err)
	}
	if _, err = New(db).ShowRecommendationRow(viewer, names["s1"].Public, "genre:tmdb:unknown", "", 1); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("undisplayed show row admitted", err)
	}
	// Nothing visible, nothing related: no rows fabricated.
	if _, _, err = New(db).ShowRecommendationRows(viewer, names["s4"].Public, 12); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("episodeless show related", err)
	}
	if _, _, err = New(db).ShowRecommendationRows(viewer, "missing", 12); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("missing show related", err)
	}
	if _, _, err = New(db).ShowRecommendationRows(Viewer{Profile: "p", Fence: "f", Libraries: []string{"other"}}, names["s1"].Public, 12); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("foreign library related", err)
	}
}
