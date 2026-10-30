package catalog

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// A genre opened across libraries holds exactly the titles the genre tiles
// count (searchGenreMembership), read from the genre's postings rather than
// every title's genres (B5).
func TestGenreMembershipMatchesCountedGenres(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	films := c.Library("m", "Movies", "movie", "/m")
	tv := c.Library("tv", "Shows", "tv", "/tv")
	music := c.Library("music", "Music", "music", "/music")
	for i := 0; i < 30; i++ {
		item := c.Movie(films, fmt.Sprintf("/m/%02d.mkv", i), fmt.Sprintf("Film %02d", i), 2000+i)
		c.Genres(item.ID, "tmdb", []string{"Action", "Drama", "Comedy"}[i%3])
		if i%4 == 0 {
			c.Attributes(item.ID, "contentRating", "R")
		}
	}
	for i := 0; i < 4; i++ {
		show := c.Show(tv, fmt.Sprintf("Show %d", i), 2010)
		season := c.Season(show, 1)
		c.Genres(show.ID, "tvdb", []string{"Drama", "Animation"}[i%2])
		for j := 0; j < 3; j++ {
			episode := c.Episode(show, season, j+1, fmt.Sprintf("/tv/%d/%d.mkv", i, j))
			if j == 2 {
				c.Genres(episode.ID, "tvdb", "Mystery")
				c.Attributes(episode.ID, "contentRating", "TV-MA")
			}
		}
	}
	artist := c.Artist(music, "Band")
	album := c.Album(artist, "Record", 2020)
	song := c.Song(album, 1, "/music/1.mp3", "Song")
	c.Genres(song.ID, "local", "Drama")
	c.Exec(`INSERT INTO content_rating_ages(value_key,minimum_age) VALUES('r',17),('tv-ma',17) ON CONFLICT(value_key) DO UPDATE SET minimum_age=excluded.minimum_age`)
	c.Drain()
	// An override relabels one source without changing its term.
	c.Exec(`UPDATE catalog_term_sources SET label_override='Thriller' WHERE entity_id=(SELECT min(entity_id) FROM catalog_term_sources WHERE term_id=(SELECT id FROM catalog_terms WHERE vocab=1 AND label='Action'))`)
	s := New(c.DB)
	if _, err := s.ClassifyPendingRatings(context.Background()); err != nil {
		t.Fatal(err)
	}
	age := 12
	for _, viewer := range []Viewer{
		{Profile: "p", Fence: "open", Libraries: []string{"m", "tv", "music"}},
		{Profile: "p", Fence: "kid", Libraries: []string{"m", "tv"}, Restrictions: identity.ContentRestrictions{MaximumAge: &age}},
	} {
		scope, args, _, err := s.browseMeasureScope(viewer, FacetRequest{Pivot: "video-titles"})
		if err != nil {
			t.Fatal(err)
		}
		ids := func(query string, args ...any) []int64 {
			t.Helper()
			rows, err := c.DB.Query(query, args...)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			out := []int64{}
			for rows.Next() {
				var id, pos int64
				if err := rows.Scan(&id, &pos); err != nil {
					t.Fatal(err)
				}
				out = append(out, id)
			}
			sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
			return out
		}
		for _, genre := range []string{"Action", "Drama", "Comedy", "Animation", "Mystery", "Thriller", "Absent", ""} {
			prefix, bound := searchGenreMembership(scope, args, viewer)
			want := ids(prefix+` SELECT entity_id AS id,0 AS pos FROM genres WHERE value=?`, append(bound, genre)...)
			query, queryArgs := genreMembership(scope, args, viewer, genre)
			got := ids(query, queryArgs...)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s %q: %v want %v", viewer.Fence, genre, got, want)
			}
			if genre == "Drama" && len(want) < 5 {
				t.Fatalf("fixture too small: %v", want)
			}
		}
		query, queryArgs := genreMembership(scope, args, viewer, "Drama")
		rows, err := c.DB.Query(`EXPLAIN QUERY PLAN `+query, queryArgs...)
		if err != nil {
			t.Fatal(err)
		}
		plan := []string{}
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		rows.Close()
		joined := strings.Join(plan, "\n")
		if !strings.Contains(joined, "catalog_term_sources_term (term_id=?)") || !strings.Contains(joined, "catalog_term_sources_overrides (label_override=?)") || !strings.Contains(joined, "catalog_browse_membership_item (item_id=?)") {
			t.Fatalf("genre membership does not start from the genre's postings:\n%s", joined)
		}
	}
}
