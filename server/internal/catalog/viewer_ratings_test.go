package catalog

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

// NEW-24: grid and row projections carry the viewer's own rating on item rows,
// read in one batched pass per page; another profile's rating never shows.
func TestListProjectionsCarryTheViewersOwnRating(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("a", "Movies", "movie", "/a")
	names := catalogtest.Names{
		"rated":   c.Movie(library, "/a/rated.mkv", "Rated", 2020),
		"unrated": c.Movie(library, "/a/unrated.mkv", "Unrated", 2021),
		"other":   c.Movie(library, "/a/other.mkv", "Other", 2022),
	}
	c.Exec(`INSERT INTO personal_items(profile_id,item_id,rating,revision) VALUES('p',?,9,1),('q',?,2,1),('p',?,NULL,1)`, names["rated"].ID, names["other"].ID, names["unrated"].ID)
	c.Drain()
	s := New(c.DB)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	check := func(where string, entries []ContentEntry) {
		t.Helper()
		if len(entries) != 3 {
			t.Fatalf("%s: %d entries: %+v", where, len(entries), entries)
		}
		for _, entry := range entries {
			if names.Of(entry.ID) == "rated" {
				if entry.UserRating == nil || *entry.UserRating != 9 {
					t.Fatalf("%s: rated row lacks the viewer's rating: %+v", where, entry)
				}
			} else if entry.UserRating != nil {
				t.Fatalf("%s: %s carries a rating the viewer never gave: %v", where, names.Of(entry.ID), *entry.UserRating)
			}
		}
	}
	page, e := s.BrowseEntities(viewer, BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Limit: 20})
	if e != nil {
		t.Fatal(e)
	}
	check("browse", page.Entries)
	content, e := s.Content(ContentRequest{Viewer: viewer, ServerID: "server", Library: "a", Profile: "p", ViewerFence: "f", View: "browse", Limit: 20})
	if e != nil {
		t.Fatal(e)
	}
	if len(content.Sections) == 0 {
		t.Fatalf("content has no sections: %+v", content)
	}
	check("content", content.Sections[0].Entries)
}
