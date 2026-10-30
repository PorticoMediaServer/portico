package catalog

import (
	"context"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/testtier"
)

// A group matches its own kind inside the index: thousands of songs sharing a
// word do not crowd three films out of the film group, and a large group's
// total is exact.
func TestSearchGroupsRankTheirOwnKindExactly(t *testing.T) {
	testtier.Media(t, "2,100 songs sharing a word")
	c := catalogtest.Open(t)
	music := c.Library("mu", "Music", "music", "/mu")
	films := c.Library("m", "Movies", "movie", "/m")
	album := c.Album(c.Artist(music, "Artist"), "Sample Album", 2020)
	for i := 0; i < 2100; i++ {
		c.Song(album, i+1, fmt.Sprintf("/mu/%05d.flac", i), fmt.Sprintf("Sample track %05d", i))
	}
	for i := 0; i < 3; i++ {
		c.Movie(films, fmt.Sprintf("/m/%d.mkv", i), fmt.Sprintf("Sample film %d", i), 2000+i)
	}
	c.Drain()
	s := New(c.DB)
	libraries := []string{"mu", "m"}
	search := func(group string) SearchGroupResult {
		t.Helper()
		out, err := s.Search(context.Background(), SearchRequest{Viewer: Viewer{Profile: "p", Fence: "f", Libraries: libraries}, ServerID: "server", Profile: "p", ViewerFence: "f", Q: "sample", Group: group, Limit: 5, Libraries: libraries})
		if err != nil || len(out.Groups) != 1 {
			t.Fatalf("%s: %+v %v", group, out, err)
		}
		return out.Groups[0]
	}
	if g := search("movies"); len(g.Items) != 3 || g.TotalCount != 3 {
		t.Fatalf("films: %d items, total %d", len(g.Items), g.TotalCount)
	}
	if g := search("songs"); len(g.Items) != 5 || g.TotalCount != 2100 {
		t.Fatalf("songs: %d items, total %d", len(g.Items), g.TotalCount)
	}
}

// The default tier's share: a group matches only its own kind.
func TestSearchGroupsMatchTheirOwnKind(t *testing.T) {
	c := catalogtest.Open(t)
	music := c.Library("mu", "Music", "music", "/mu")
	films := c.Library("m", "Movies", "movie", "/m")
	album := c.Album(c.Artist(music, "Artist"), "Harbor Album", 2020)
	for i := 0; i < 8; i++ {
		c.Song(album, i+1, fmt.Sprintf("/mu/%d.flac", i), fmt.Sprintf("Harbor track %d", i))
	}
	c.Movie(films, "/m/1.mkv", "Harbor film", 2001)
	c.Drain()
	s := New(c.DB)
	libraries := []string{"mu", "m"}
	out, err := s.Search(context.Background(), SearchRequest{Viewer: Viewer{Profile: "p", Fence: "f", Libraries: libraries}, ServerID: "server", Profile: "p", ViewerFence: "f", Q: "harbor", Group: "movies", Limit: 5, Libraries: libraries})
	if err != nil || len(out.Groups) != 1 || len(out.Groups[0].Items) != 1 || out.Groups[0].TotalCount != 1 {
		t.Fatalf("film group: %+v %v", out.Groups, err)
	}
}

// Candidates are chosen inside the index already scoped to the viewer's
// libraries: another library's matches never take a viewer's slots.
func TestSearchCandidatesAreScopedToTheViewersLibraries(t *testing.T) {
	c := catalogtest.Open(t)
	mine := c.Library("a", "Mine", "movie", "/a")
	other := c.Library("b", "Other", "movie", "/b")
	c.Movie(mine, "/a/1.mkv", "Harbor mine", 2001)
	for i := 0; i < 6; i++ {
		c.Movie(other, fmt.Sprintf("/b/%d.mkv", i), fmt.Sprintf("Harbor other %d", i), 2001)
	}
	c.Drain()
	s := New(c.DB)
	libraries := []string{"a"}
	out, err := s.Search(context.Background(), SearchRequest{Viewer: Viewer{Profile: "p", Fence: "f", Libraries: libraries}, ServerID: "server", Profile: "p", ViewerFence: "f", Q: "harbor", Group: "movies", Limit: 5, Libraries: libraries})
	if err != nil || len(out.Groups) != 1 || len(out.Groups[0].Items) != 1 || out.Groups[0].TotalCount != 1 {
		t.Fatalf("scoped group: %+v %v", out.Groups, err)
	}
	var tokens string
	if err = c.DB.QueryRow(`SELECT d.kind FROM catalog_search_documents d JOIN catalog_entities e ON e.id=d.entity_id WHERE e.library_id=?`, mine).Scan(&tokens); err != nil || tokens != fmt.Sprintf("k1 l%d", mine) {
		t.Fatalf("document tokens %q %v", tokens, err)
	}
}
