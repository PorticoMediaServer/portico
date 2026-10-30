package catalog

import (
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

// A list across libraries counts its filter choices over its own titles, not
// the libraries it spans.
func TestFacetsAcrossCountWithinTheList(t *testing.T) {
	c := catalogtest.Open(t)
	movies := c.Library("m", "Movies", "movie", "/m")
	for i := 0; i < 6; i++ {
		item := c.Movie(movies, fmt.Sprintf("/m/%d.mkv", i), fmt.Sprintf("Film %d", i), 2000)
		genre := "Drama"
		if i%2 == 0 {
			genre = "Comedy"
		}
		c.Genres(item.ID, "fixture", genre)
		if i < 3 {
			c.Exec(`INSERT INTO personal_items(profile_id,item_id,favorite,revision) VALUES('p',?,1,1)`, item.ID)
		}
	}
	c.Drain()
	s := New(c.DB)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"m"}}
	whole, err := s.FacetsAcross(viewer, FacetRequest{Field: "genre", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	inList, err := s.FacetsAcross(viewer, FacetRequest{Field: "genre", Limit: 10, Group: &BrowseNode{Field: "favorite", Operator: "equals", Value: true}})
	if err != nil {
		t.Fatal(err)
	}
	count := func(page FacetPage) map[string]int {
		out := map[string]int{}
		for _, v := range page.Values {
			out[v.Label] = v.Count
		}
		return out
	}
	if got := count(whole); got["Comedy"] != 3 || got["Drama"] != 3 {
		t.Fatalf("libraries: %v", got)
	}
	if got := count(inList); got["Comedy"] != 2 || got["Drama"] != 1 {
		t.Fatalf("favorites: %v", got)
	}
}
