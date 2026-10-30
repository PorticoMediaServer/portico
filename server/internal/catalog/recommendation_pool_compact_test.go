package catalog

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func compactRecommendationFixture(t *testing.T) (*sql.DB, catalogtest.Names) {
	t.Helper()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	c := catalogtest.New(t, db)
	names := catalogtest.Names{}
	movies := c.Library("movies", "Movies", "movie", "/movies")
	tv := c.Library("tv", "TV", "tv", "/tv")
	music := c.Library("music", "Music", "music", "/music")
	books := c.Library("books", "Books", "audiobook", "/books")
	names["m1"] = c.Movie(movies, "/movies/one.mkv", "One", 2001)
	names["m2"] = c.Movie(movies, "/movies/two.mkv", "Two", 2002)
	c.Genres(names["m1"].ID, "tmdb", "Animation")
	c.Genres(names["m2"].ID, "tmdb", "Animation")
	credit := compactcatalog.Credit{PersonKey: "tmdb:42", PersonName: "Casey Example", PersonSortName: "Casey Example", ProviderPersonID: "42", CreditID: "c1", CreditedName: "Casey Example", Role: "Character", Department: "Acting"}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		if err := compactcatalog.SetCreditsTx(ctx, tx, names["m1"].ID, "tmdb", []compactcatalog.Credit{credit}); err != nil {
			return err
		}
		return compactcatalog.SetCreditsTx(ctx, tx, names["m2"].ID, "tmdb", []compactcatalog.Credit{credit})
	})
	c.Collection(movies, "Related Set", names["m1"], names["m2"])
	show := c.Show(tv, "Shared Show", 2020)
	season := c.Season(show, 1)
	names["show"] = show
	names["e101"] = c.Episode(show, season, 1, "/tv/Shared Show/ep101.mkv")
	names["e102"] = c.Episode(show, season, 2, "/tv/Shared Show/ep102.mkv")
	artist := c.Artist(music, "Artist")
	album := c.Album(artist, "Album", 2020)
	names["album"] = album
	names["t1"] = c.Song(album, 1, "/music/Album/track1.flac", "Track 1")
	names["t2"] = c.Song(album, 2, "/music/Album/track2.flac", "Track 2")
	book1 := c.Book(books, "Book One", "Author")
	c.Book(books, "Book Two", "Author")
	names["book"] = book1
	names["p1"] = c.BookFile(book1, 1, "/books/Book One/part1.m4b")
	names["p2"] = c.BookFile(book1, 2, "/books/Book One/part2.m4b")
	c.Drain()
	return db, names
}

func TestCompactRelatedPoolSources(t *testing.T) {
	db, names := compactRecommendationFixture(t)
	tests := []struct {
		name, seed, want string
		query            int
	}{
		{"genre", "m1", "m2", 0},
		{"credited person", "m1", "m2", 1},
		{"collection", "m1", "m2", 2},
		{"show", "e101", "e102", 3},
		{"album artist", "t1", "t2", 4},
		{"song artist", "t1", "t2", 5},
		{"book author", "p1", "p2", 6},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := db.Query(relatedPoolQueries[tc.query], names[tc.seed].Public)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			if !queryContainsID(t, rows, names[tc.want].Public) {
				t.Fatalf("related source %q omitted %s for %s", tc.name, tc.want, tc.seed)
			}
		})
	}
}

func queryContainsID(t *testing.T, rows *sql.Rows, want string) bool {
	t.Helper()
	found := false
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		found = found || id == want
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return found
}

func TestCompactRecommendationRowMembers(t *testing.T) {
	db, names := compactRecommendationFixture(t)
	rows := []HomeRow{{Entries: []ContentEntry{{ID: names["m1"].Public}, {ID: names["show"].Public}, {ID: names["album"].Public}, {ID: names["book"].Public}}}}
	got, err := New(db).recommendationRowMembers(rows)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool, len(got))
	for _, id := range got {
		seen[id] = true
	}
	for _, alias := range []string{"m1", "e101", "e102", "t1", "t2", "p1", "p2"} {
		if !seen[names[alias].Public] {
			t.Fatalf("missing %s from compact members %v", alias, got)
		}
	}
}
