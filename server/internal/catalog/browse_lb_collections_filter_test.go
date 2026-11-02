package catalog

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

// LB-5: the "collection" filter is a title's membership. On the Collections tab the
// cards are the collections themselves, and the filter used to resolve to "every
// collection sharing a film with X". It is not offered there, and the Movies tab
// keeps it, selecting the films in X.
func TestBrowseCollectionFilterIsATitleFilterNotOfferedOnTheCollectionsTab(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("a", "Movies", "movie", "/a")
	alpha := c.Movie(lib, "/a/alpha.mkv", "Alpha", 2000)
	beta := c.Movie(lib, "/a/beta.mkv", "Beta", 2001)
	gamma := c.Movie(lib, "/a/gamma.mkv", "Gamma", 2002)
	x := c.Collection(lib, "X", alpha)
	c.Collection(lib, "Y", alpha, beta)
	c.Collection(lib, "Z", gamma)
	c.Drain()

	s := New(c.DB)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	offers := func(pivot string) bool {
		caps, err := s.BrowseCapabilitiesFor("a", pivot)
		if err != nil {
			t.Fatal(err)
		}
		for _, field := range caps.Fields {
			if field.ID == "collection" {
				return true
			}
		}
		return false
	}
	if offers("collections") {
		t.Fatal("the Collections tab offers a collection filter over collections")
	}
	if !offers("movies") {
		t.Fatal("the Movies tab lost its collection filter")
	}
	query := &BrowseNode{Field: "collection", Operator: "contains-any", Value: []any{c.Public(x.ID)}}
	page, err := s.BrowseEntities(viewer, BrowseRequest{Library: "a", Pivot: "movies", Limit: 40, Query: query})
	if err != nil {
		t.Fatal(err)
	}
	titles := []string{}
	for _, entry := range page.Entries {
		titles = append(titles, entry.Title)
	}
	if len(titles) != 1 || titles[0] != "Alpha" {
		t.Fatalf("collection=X on the Movies tab: got %v, want [Alpha]", titles)
	}
}
