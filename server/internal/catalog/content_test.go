package catalog

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"testing"
)

func TestContentOwnsSemanticsAndFencesContinuations(t *testing.T) {
	db, e := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s := New(db)
	c := catalogtest.New(t, db)
	a := c.Library("a", "Movies", "movie", "/a")
	c.Library("b", "Other", "movie", "/b")
	c.Library("tv", "TV", "tv", "/tv")
	for n := 0; n < 23; n++ {
		path := fmt.Sprintf("/a/%d.mkv", n)
		c.Entity(compactcatalog.Entity{Library: a, Kind: compactcatalog.Movie, Key: compactcatalog.ItemKey("/a", path, 0), Title: fmt.Sprintf("Film %02d", n), Year: 1990 + n, Added: fmt.Sprintf("2026-09-04T01:00:%02d.000Z", n)}, nil)
	}
	c.Drain()
	r := ContentRequest{Viewer: Viewer{Profile: "p", Fence: "f", Libraries: []string{"a", "b", "tv"}}, ServerID: "server", Library: "a", Profile: "p", ViewerFence: "f", View: "browse", Limit: 5}
	page, e := s.Content(r)
	if e != nil {
		t.Fatal(e)
	}
	if page.Heading.Fallback != "Movies" || page.Scope.ServerID != "server" || page.Navigation[1].LabelKey != "library.movies" || len(page.Sections) != 1 || page.Sections[0].TotalCount != 23 || len(page.Sections[0].Entries) != 5 {
		t.Fatalf("bad semantics %+v", page)
	}
	firstCursor := page.Sections[0].NextCursor
	seen := map[string]bool{}
	for {
		for _, entry := range page.Sections[0].Entries {
			if seen[entry.ID] {
				t.Fatal("duplicate", entry.ID)
			}
			seen[entry.ID] = true
		}
		if page.Sections[0].NextCursor == "" {
			break
		}
		r.Cursor = page.Sections[0].NextCursor
		page, e = s.Content(r)
		if e != nil {
			t.Fatal(e)
		}
	}
	if len(seen) != 23 {
		t.Fatal("missing items", len(seen))
	}
	r.Cursor = firstCursor
	for _, change := range []func(*ContentRequest){func(r *ContentRequest) { r.ViewerFence = "other" }, func(r *ContentRequest) { r.Library = "b" }, func(r *ContentRequest) { r.Q = "Film" }, func(r *ContentRequest) { r.Limit = 6 }, func(r *ContentRequest) { r.View = "collections" }} {
		bad := r
		change(&bad)
		if _, e = s.Content(bad); !errors.Is(e, ErrCursor) {
			t.Fatal("scope accepted", e)
		}
	}
	var firstID int64
	if e = db.QueryRow(`SELECT id FROM catalog_entities WHERE title='Film 00'`).Scan(&firstID); e != nil {
		t.Fatal(e)
	}
	c.Fields(firstID, map[string]any{"title": "Changed"})
	if _, e = s.Content(r); e != nil {
		t.Fatal("catalogue publication failed continuation", e)
	}
	r.Cursor = ""
	c.Drain()
	page, e = s.Content(r)
	if e != nil {
		t.Fatal(e)
	}
	r.Cursor = page.Sections[0].NextCursor
	if _, e = db.Exec(`INSERT INTO viewer_revisions(profile_id,library_id,revision) VALUES('p','a',1)`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Content(r); !errors.Is(e, ErrStaleContinuation) {
		t.Fatal("personal publication did not require a typed restart", e)
	}
	r.Cursor = ""
	page, e = s.Content(r)
	if e != nil || len(page.Sections) != 1 || page.Sections[0].TotalCount != 23 || len(page.Sections[0].Entries) != 5 {
		t.Fatal("fresh browse after personal publication", page, e)
	}
	r.View = "discover"
	page, e = s.Content(r)
	if e != nil || page.Query.Sort != "server" || page.Query.SearchMode != "none" || len(page.Sorts) != 0 || len(page.Filters) != 0 || len(page.Sections) != 0 {
		t.Fatal("discovery must omit unavailable works while retaining server-owned query semantics", page, e)
	}
	r.View = "browse"
	r.Q = "Film 1"
	page, e = s.Content(r)
	if e != nil || page.Sections[0].TotalCount != 10 || len(page.Filters[0].Options) != 1 || page.Filters[0].Options[0].Count != 10 {
		t.Fatal("prefix facets", page, e)
	}
	r.Q = "Film %"
	page, e = s.Content(r)
	if e != nil || len(page.Sections) != 0 || page.Empty == nil {
		t.Fatal("wildcard unescaped", page, e)
	}
	r.Q = ""
	r.View = "categories"
	page, e = s.Content(r)
	if e != nil || page.Query.Direction != "desc" || page.Sections[0].Entries[0].ID != "decade:2010" {
		t.Fatal("category order", page, e)
	}
	r.Library = "b"
	r.View = "discover"
	page, e = s.Content(r)
	if e != nil || len(page.Sections) != 0 || page.Empty == nil {
		t.Fatal("empty discovery fabricated rails", page, e)
	}
	r.Library = "tv"
	r.View = ""
	page, e = s.Content(r)
	if e != nil || page.Scope.View != "browse" || page.Empty == nil {
		t.Fatal("default view", page, e)
	}
	libs, e := s.Libraries()
	if e != nil {
		t.Fatal(e)
	}
	for _, lib := range libs {
		if lib.DefaultView != DefaultLibraryView(lib.Kind) {
			t.Fatal("missing default", lib)
		}
	}
}

func TestContentWatchedBadgeIsExplicitAndProfileScoped(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "content-personal.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c := catalogtest.New(t, db)
	library := c.Library("library", "Library", "movie", "/library")
	item := c.Movie(library, "/library/Item.mkv", "Item", 2020)
	c.Drain()
	s := New(db)
	if _, err := db.Exec(`INSERT INTO personal_items(profile_id,item_id,watchlisted,favorite,revision,watched) VALUES('first',?,0,0,1,1); INSERT INTO personal_watched_intents VALUES('first',?,1,'2026-09-10T00:00:00.000000000Z'); INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES('second',?,9999000,0,'fixture')`, item.ID, item.ID, item.ID); err != nil {
		t.Fatal(err)
	}
	for _, profile := range []string{"first", "second"} {
		page, err := s.Content(ContentRequest{Viewer: Viewer{Profile: profile, Fence: profile, Libraries: []string{"library"}}, ServerID: "server", Library: "library", Profile: profile, ViewerFence: profile, View: "browse", Limit: 5})
		if err != nil {
			t.Fatal(err)
		}
		entry := page.Sections[0].Entries[0]
		if entry.Watched == nil || *entry.Watched != (profile == "first") {
			t.Fatalf("incorrect watched state for %s: %+v", profile, entry)
		}
		item, err := s.Get(profile, item.Public)
		if err != nil {
			t.Fatal(err)
		}
		if item.Watched == nil || *item.Watched != (profile == "first") {
			t.Fatalf("incorrect detail state for %s", profile)
		}
	}
}

func TestMovieCategoriesCarryGenresStudiosAndArtwork(t *testing.T) {
	db, e := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s := New(db)
	c := catalogtest.New(t, db)
	movies := c.Library("m", "Movies", "movie", "/m")
	plainLibrary := c.Library("plain", "Plain", "movie", "/plain")
	items := []struct {
		id, title string
		year      int
		poster    string
	}{
		{"m1", "Alpha", 1995, "/p-alpha"},
		{"m2", "Beta", 1999, "/p-beta"},
		{"m3", "Gamma", 2001, "/p-gamma"},
		{"m4", "Delta", 2005, ""},
		{"m5", "Epsilon", 2010, "/p-epsilon"},
		{"m6", "Zeta", 2015, "/p-zeta"},
		{"p1", "Solo", 2020, ""},
	}
	names := catalogtest.Names{}
	for _, item := range items {
		library := movies
		if item.id == "p1" {
			library = plainLibrary
		}
		fixture := c.Movie(library, "/"+map[bool]string{true: "plain", false: "m"}[item.id == "p1"]+"/"+item.id+".mkv", item.title, item.year)
		names[item.id] = fixture
		if item.poster != "" {
			c.Fields(fixture.ID, map[string]any{"poster_url": item.poster})
		}
	}
	for _, name := range []string{"m1", "m2", "m5"} {
		c.Genres(names[name].ID, "tmdb", "Drama")
	}
	c.Genres(names["m3"].ID, "tmdb", "Drama", "Comedy")
	c.Genres(names["m3"].ID, "imdb", "Drama")
	c.Genres(names["m4"].ID, "tmdb", "Comedy")
	c.Genres(names["m6"].ID, "tmdb", "Comedy")
	for _, id := range []string{"m1", "m2", "m5"} {
		c.Attributes(names[id].ID, "studio", "Pixar")
	}
	c.Attributes(names["m3"].ID, "studio", "A24")
	c.Drain()
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"m", "plain"}}
	categories, e := s.Categories(viewer, "m")
	if e != nil {
		t.Fatal(e)
	}
	byID := map[string]Category{}
	for _, category := range categories {
		byID[category.ID] = category
	}
	for id, count := range map[string]int{"decade:1990": 2, "decade:2000": 2, "decade:2010": 2, "genre:Drama": 4, "genre:Comedy": 3, "studio:Pixar": 3, "studio:A24": 1} {
		category, ok := byID[id]
		if !ok || category.Count != count {
			t.Fatalf("missing category %s: %+v", id, byID)
		}
	}
	if got := byID["genre:Drama"].ArtworkPaths; len(got) != 4 || got[0] != "/p-alpha" || got[1] != "/p-beta" || got[2] != "/p-epsilon" || got[3] != "/p-gamma" {
		t.Fatalf("drama artwork is not the top titles: %+v", got)
	}
	if got := byID["genre:Comedy"].ArtworkPaths; len(got) != 2 || got[0] != "/p-gamma" || got[1] != "/p-zeta" {
		t.Fatalf("comedy artwork wrong: %+v", got)
	}
	if got := byID["studio:Pixar"].ArtworkPaths; len(got) != 3 {
		t.Fatalf("studio artwork wrong: %+v", got)
	}
	plain, e := s.Categories(viewer, "plain")
	if e != nil {
		t.Fatal(e)
	}
	for _, category := range plain {
		if field, _, _ := strings.Cut(category.ID, ":"); field == "studio" {
			t.Fatalf("studio category without studio metadata: %+v", plain)
		}
	}
	request := func(category string) ContentRequest {
		return ContentRequest{Viewer: viewer, ServerID: "server", Library: "m", Profile: "p", ViewerFence: "f", View: "browse", Sort: "title", Direction: "asc", Category: category, Limit: 10}
	}
	drama, e := s.Content(request("genre:Drama"))
	if e != nil || drama.Sections[0].TotalCount != 4 || len(drama.Sections[0].Entries) != 4 {
		t.Fatal("genre filter", drama, e)
	}
	pixar, e := s.Content(request("studio:Pixar"))
	if e != nil || pixar.Sections[0].TotalCount != 3 {
		t.Fatal("studio filter", pixar, e)
	}
	if _, e = s.Content(request("format:VHS")); e == nil {
		t.Fatal("unknown category accepted")
	}
	// The categories view carries the same artwork the endpoint publishes.
	view, e := s.Content(ContentRequest{Viewer: viewer, ServerID: "server", Library: "m", Profile: "p", ViewerFence: "f", View: "categories", Limit: 100})
	if e != nil {
		t.Fatal(e)
	}
	entries := map[string]ContentEntry{}
	for _, entry := range view.Sections[0].Entries {
		entries[entry.ID] = entry
	}
	if len(entries["genre:Drama"].ArtworkPaths) != 4 || entries["decade:1990"].ArtworkPaths == nil {
		t.Fatalf("categories view lost artwork: %+v", entries)
	}
}
