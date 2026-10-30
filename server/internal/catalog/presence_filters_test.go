package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// Presence filters, "has no genre" on artists and albums, and title text
// start from maintained counters, the songs' genre sources and the title
// trigrams; they must match exactly the titles the per-title test matches.
func TestPresenceGenreAndTitleFiltersMatchTheTitleTest(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	movies := c.Library("m", "Movies", "movie", "/m")
	for i := 0; i < 30; i++ {
		title := fmt.Sprintf("Film %02d", i)
		if i%7 == 0 {
			title = fmt.Sprintf("Zebra Crossing %d", i)
		}
		item := c.Movie(movies, fmt.Sprintf("/m/%d.mkv", i), title, 2000)
		if i%10 == 3 {
			// A title whose files are all gone has no duration.
			c.Exec(`DELETE FROM catalog_asset_links WHERE entity_id=?`, item.ID)
		}
		credits := []compactcatalog.Credit{}
		if i%5 != 0 {
			name := fmt.Sprintf("Actor %d", i%4)
			credits = append(credits, compactcatalog.Credit{PersonKey: "p:" + name, PersonName: name, CreditedName: name, Role: "Actor", Department: "Acting"})
		}
		if i%3 != 0 {
			credits = append(credits, compactcatalog.Credit{PersonKey: "d", PersonName: "Dir", CreditedName: "Dir", Role: "Director", Department: "Directing"})
		}
		if i%6 == 1 {
			credits = append(credits, compactcatalog.Credit{PersonKey: "w", PersonName: "Pen", CreditedName: "Pen", Role: "Screenplay", Department: "Writing"})
		}
		if len(credits) > 0 {
			c.Write(func(ctx context.Context, tx *sql.Tx) error {
				return compactcatalog.SetCreditsTx(ctx, tx, item.ID, "fixture", credits)
			})
		}
	}
	music := c.Library("a", "Music", "music", "/a")
	for a := 0; a < 3; a++ {
		artist := c.Artist(music, fmt.Sprintf("Artist %d", a))
		for b := 0; b < 3; b++ {
			album := c.Album(artist, fmt.Sprintf("Album %d-%d", a, b), 2001)
			for s := 0; s < 3; s++ {
				song := c.Song(album, s+1, fmt.Sprintf("/a/%d/%d/%d.flac", a, b, s), fmt.Sprintf("Song %d", s))
				// Every song of album 0-0 and one song of album 1-1 lack a genre.
				if !(a == 0 && b == 0) && !(a == 1 && b == 1 && s == 2) {
					c.Genres(song.ID, "tags", "Rock")
				}
			}
		}
	}
	tv := c.Library("t", "TV", "tv", "/t")
	for i := 0; i < 4; i++ {
		show := c.Show(tv, fmt.Sprintf("Show %d", i), 2002)
		season := c.Season(show, 1)
		for n := 1; n <= 3; n++ {
			episode := c.Episode(show, season, n, fmt.Sprintf("/t/%d/%d.mkv", i, n))
			// Only the second show has a director, on one episode.
			if i == 1 && n == 2 {
				c.Write(func(ctx context.Context, tx *sql.Tx) error {
					return compactcatalog.SetCreditsTx(ctx, tx, episode.ID, "fixture", []compactcatalog.Credit{{PersonKey: "d", PersonName: "Dir", CreditedName: "Dir", Role: "Director", Department: "Directing"}})
				})
			}
		}
	}
	c.Drain()
	v := Viewer{Profile: "p", Fence: "presence", Libraries: []string{"m", "a", "t"}}
	s := New(c.DB)
	cases := []struct {
		library, pivot string
		kind           int
		node           BrowseNode
	}{
		{"m", "movies", 1, BrowseNode{Field: "actor", Operator: "is-missing"}},
		{"m", "movies", 1, BrowseNode{Field: "actor", Operator: "is-present"}},
		{"m", "movies", 1, BrowseNode{Field: "director", Operator: "is-missing"}},
		{"m", "movies", 1, BrowseNode{Field: "writer", Operator: "is-missing"}},
		{"m", "movies", 1, BrowseNode{Field: "writer", Operator: "is-present"}},
		{"m", "movies", 1, BrowseNode{Field: "durationMs", Operator: "is-missing"}},
		{"m", "movies", 1, BrowseNode{Field: "durationMs", Operator: "is-present"}},
		{"m", "movies", 1, BrowseNode{Field: "versions", Operator: "is-missing"}},
		{"m", "movies", 1, BrowseNode{Field: "versions", Operator: "is-present"}},
		{"m", "movies", 1, BrowseNode{Field: "title", Operator: "contains", Value: "ebra cross"}},
		{"m", "movies", 1, BrowseNode{Field: "title", Operator: "contains", Value: "LM 1"}},
		{"m", "movies", 1, BrowseNode{Field: "title", Operator: "starts-with", Value: "zebra"}},
		{"m", "movies", 1, BrowseNode{Field: "title", Operator: "equals", Value: "film 05"}},
		{"m", "movies", 1, BrowseNode{Field: "title", Operator: "in", Value: []any{"Film 01", "Zebra Crossing 7"}}},
		{"m", "movies", 1, BrowseNode{Field: "title", Operator: "contains", Value: "qqq"}},
		{"m", "movies", 1, BrowseNode{Field: "title", Operator: "contains", Value: "m"}},
		{"t", "shows", 2, BrowseNode{Field: "director", Operator: "is-present"}},
		{"t", "shows", 2, BrowseNode{Field: "director", Operator: "is-missing"}},
		{"t", "shows", 2, BrowseNode{Field: "writer", Operator: "is-present"}},
		{"t", "episodes", 4, BrowseNode{Field: "director", Operator: "is-present"}},
		{"a", "albums", 6, BrowseNode{Field: "genre", Operator: "is-missing"}},
		{"a", "artists", 5, BrowseNode{Field: "genre", Operator: "is-missing"}},
		{"a", "songs", 7, BrowseNode{Field: "genre", Operator: "is-missing"}},
	}
	for _, tc := range cases {
		node := tc.node
		page, err := s.BrowseEntities(v, BrowseRequest{Library: tc.library, Pivot: tc.pivot, Query: &node, Limit: 100})
		if err != nil {
			t.Fatalf("%+v: %v", node, err)
		}
		got := []string{}
		for _, entry := range page.Entries {
			got = append(got, entry.ID)
		}
		where, args, err := CompileBrowseSelection(&node)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := c.DB.Query(`SELECT pid(ce.public_id) FROM catalog_browse_rows e JOIN catalog_entities ce ON ce.id=e.entity_id WHERE e.kind=? AND `+where, append([]any{tc.kind}, args...)...)
		if err != nil {
			t.Fatalf("%+v: %v", node, err)
		}
		want := []string{}
		for rows.Next() {
			var id string
			rows.Scan(&id)
			want = append(want, id)
		}
		rows.Close()
		sort.Strings(want)
		sort.Strings(got)
		if fmt.Sprint(want) != fmt.Sprint(got) {
			t.Fatalf("%s %s %s %v: selected %d titles, title test %d", tc.pivot, node.Field, node.Operator, node.Value, len(got), len(want))
		}
		t.Logf("%s %s %s %v: %d titles", tc.pivot, node.Field, node.Operator, node.Value, len(want))
	}
}

// A grouping of songs counts the songs a presence filter keeps.
func TestGroupingPresenceFiltersCountTheirSongs(t *testing.T) {
	c := catalogtest.Open(t)
	music := c.Library("a", "Music", "music", "/a")
	album := c.Album(c.Artist(music, "Artist"), "Album", 2001)
	for s := 0; s < 4; s++ {
		song := c.Song(album, s+1, fmt.Sprintf("/a/%d.flac", s), fmt.Sprintf("Song %d", s))
		c.Genres(song.ID, "tags", []string{"Rock", "Jazz"}[s%2])
		if s == 3 {
			c.Exec(`DELETE FROM catalog_asset_links WHERE entity_id=?`, song.ID)
		}
	}
	c.Drain()
	s := New(c.DB)
	v := Viewer{Profile: "p", Fence: "grouping", Libraries: []string{"a"}}
	for _, tc := range []struct {
		node BrowseNode
		want string
	}{
		{BrowseNode{Field: "durationMs", Operator: "is-present"}, "[Jazz:1 Rock:2]"},
		{BrowseNode{Field: "durationMs", Operator: "is-missing"}, "[Jazz:1]"},
		{BrowseNode{Field: "versions", Operator: "is-present"}, "[Jazz:2 Rock:2]"},
		{BrowseNode{Field: "versions", Operator: "is-missing"}, "[]"},
		{BrowseNode{Field: "genre", Operator: "is-missing"}, "[]"},
		{BrowseNode{Field: "title", Operator: "contains", Value: "song 2"}, "[Rock:1]"},
	} {
		node := tc.node
		page, err := s.BrowseEntities(v, BrowseRequest{Library: "a", Pivot: "genres", Query: &node, Limit: 10})
		if err != nil {
			t.Fatalf("%+v: %v", node, err)
		}
		got := []string{}
		for _, entry := range page.Entries {
			n := 0
			if entry.Count != nil {
				n = *entry.Count
			}
			got = append(got, fmt.Sprintf("%s:%d", entry.Title, n))
		}
		sort.Strings(got)
		if fmt.Sprint(got) != tc.want {
			t.Fatalf("%s %s: %v, want %s", node.Field, node.Operator, got, tc.want)
		}
	}
}

// The counters behind "is missing" follow credits and file links as they
// are written, moved and removed.
func TestFactGapCountersFollowCreditsAndLinks(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("m", "Movies", "movie", "/m")
	item := c.Movie(lib, "/m/a.mkv", "A", 2000)
	other := c.Movie(lib, "/m/b.mkv", "B", 2000)
	set := func(id int64, credits ...compactcatalog.Credit) {
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			return compactcatalog.SetCreditsTx(ctx, tx, id, "fixture", credits)
		})
	}
	counters := func(id int64) string {
		var links, acting, directing, writing int
		if err := c.DB.QueryRow(`SELECT asset_links,acting_credits,directing_credits,writing_credits FROM catalog_entities WHERE id=?`, id).Scan(&links, &acting, &directing, &writing); err != nil {
			t.Fatal(err)
		}
		return fmt.Sprint(links, acting, directing, writing)
	}
	set(item.ID,
		compactcatalog.Credit{PersonKey: "a", PersonName: "A", CreditedName: "A", Role: "Actor", Department: "Acting"},
		compactcatalog.Credit{PersonKey: "d", PersonName: "D", CreditedName: "D", Role: "Director", Department: "Directing"},
		compactcatalog.Credit{PersonKey: "w", PersonName: "W", CreditedName: "W", Role: "Writer", Department: "Writing"})
	c.File(item.ID, "/m/a-2.mkv", 100)
	if got := counters(item.ID); got != "2 1 1 1" {
		t.Fatalf("after writes: %s", got)
	}
	set(item.ID, compactcatalog.Credit{PersonKey: "d", PersonName: "D", CreditedName: "D", Role: "Director", Department: "Directing"})
	c.Exec(`UPDATE catalog_asset_links SET entity_id=? WHERE entity_id=? AND asset_id=?`, other.ID, item.ID, item.Asset)
	if got, moved := counters(item.ID), counters(other.ID); got != "1 0 1 0" || moved != "2 0 0 0" {
		t.Fatalf("after replace and move: %s / %s", got, moved)
	}
	c.Exec(`DELETE FROM catalog_asset_links WHERE entity_id=?`, item.ID)
	set(item.ID)
	if got := counters(item.ID); got != "0 0 0 0" {
		t.Fatalf("after removal: %s", got)
	}
}
