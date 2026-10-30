package catalog

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// The scorer's facets come from the maintained per-title table, and they are
// what the per-request derivation produced: genres, the top of each
// provider's billing, directors and writers, collections, studio, decade,
// tags, album artist, and a book's author and series.
func TestRecommendationFacetsAreMaintainedPerTitle(t *testing.T) {
	c := catalogtest.Open(t)
	films := c.Library("m", "Movies", "movie", "/m")
	music := c.Library("mu", "Music", "music", "/mu")
	books := c.Library("b", "Books", "audiobook", "/b")
	film := c.Movie(films, "/m/f.mkv", "Film", 1994)
	c.Genres(film.ID, "tmdb", "Drama", "Sci-Fi")
	credits := []compactcatalog.Credit{}
	for i := 0; i < 20; i++ {
		credits = append(credits, personCredit(fmt.Sprintf("tmdb:c%d", i), fmt.Sprintf("c%d", i), fmt.Sprintf("Actor %d", i), "Character", "Acting", i))
	}
	credits = append(credits, personCredit("tmdb:d", "d", "Jane Director", "Director", "Directing", 0))
	compactCredits(c, film.ID, credits...)
	c.Fields(film.ID, map[string]any{"studio": "Acme Pictures"})
	c.Collection(films, "Saga", film)
	c.Exec(`INSERT INTO metadata_tags(entity_kind,entity_id,provider,id,name) VALUES('item',?,'tmdb','1','Time Travel')`, film.ID)
	artist := c.Artist(music, "The Band")
	album := c.Album(artist, "Album", 2001)
	song := c.Song(album, 1, "/mu/1.flac", "Song")
	book := c.Book(books, "Novel", "Ann Author")
	c.Fields(book.ID, map[string]any{"series": "Cycle"})
	c.BookFile(book, 1, "/b/n.m4b")
	c.Drain()
	s := New(c.DB)
	r := HomeRequest{Viewer: Viewer{Profile: "p", Fence: "f", Libraries: []string{"m", "mu", "b"}}, ServerID: "server", Profile: "p", ViewerFence: "f", Libraries: []string{"m", "mu", "b"}, Now: time.Now()}
	base, args := recBase(r, engineWholeLibrary(t, s, r))
	facetsOf := func(workPrefix string) []string {
		t.Helper()
		rows, err := s.read().Query(base+` SELECT f FROM facets WHERE work LIKE ? ORDER BY f`, append(args, workPrefix+"%")...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := []string{}
		for rows.Next() {
			var f string
			if err = rows.Scan(&f); err != nil {
				t.Fatal(err)
			}
			out = append(out, f)
		}
		return out
	}
	filmFacets := strings.Join(facetsOf("movie:"), ",")
	for _, want := range []string{"g:drama", "g:science fiction", "p:tmdb:c0", "p:tmdb:c15", "cd:directing:jane director", "c:" + c.Public(c.ID(film.Public)), "s:acme pictures", "e:199", "t:time travel"} {
		if !strings.Contains(filmFacets+",", want+",") && !strings.HasPrefix(want, "c:") {
			t.Fatalf("film facets %s lack %s", filmFacets, want)
		}
	}
	if strings.Contains(filmFacets, "p:tmdb:c16") {
		t.Fatalf("film facets reach past the top of the billing: %s", filmFacets)
	}
	if !strings.Contains(filmFacets, ",c:") && !strings.HasPrefix(filmFacets, "c:") {
		t.Fatalf("film facets lack its collection: %s", filmFacets)
	}
	albumFacets := facetsOf("album:")
	if !hasFacet(albumFacets, "a:"+artist.Public) {
		t.Fatalf("album facets %v", albumFacets)
	}
	bookFacets := facetsOf("book:")
	sort.Strings(bookFacets)
	if !hasFacet(bookFacets, "b:ann author") || !hasFacet(bookFacets, "k:cycle") {
		t.Fatalf("book facets %v", bookFacets)
	}
	_ = song
}

func hasFacet(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
