package catalog

import (
	"context"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

// Removing a library of any kind, with collections, people's state and a
// published visibility class, leaves no row behind: not even a hidden one.
func TestRemovedLibraryOfEveryKindLeavesNoHiddenRow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, kind := range []string{"tv", "music", "audiobook"} {
		t.Run(kind, func(t *testing.T) {
			c := catalogtest.Open(t)
			handle := c.Library("x", "X", kind, "/x")
			var played catalogtest.Item
			var container catalogtest.Item
			switch kind {
			case "tv":
				show := c.Show(handle, "Show", 2001)
				season := c.Season(show, 1)
				for n := 1; n <= 6; n++ {
					played = c.Episode(show, season, n, fmt.Sprintf("/x/S01E%02d.mkv", n))
				}
				container = show
			case "music":
				artist := c.Artist(handle, "Artist")
				album := c.Album(artist, "Album", 2020)
				for n := 1; n <= 6; n++ {
					played = c.Song(album, n, fmt.Sprintf("/x/%02d.mp3", n), fmt.Sprintf("Song %d", n))
				}
				container = album
			default:
				book := c.Book(handle, "Book", "Author")
				for n := 1; n <= 3; n++ {
					played = c.BookFile(book, n, fmt.Sprintf("/x/%02d.mp3", n))
				}
				container = book
				c.Exec(`INSERT INTO book_resume(profile_id,book_id,item_id,playback_id,position) VALUES('p',?,?,'pb',10)`, book.ID, played.ID)
			}
			c.Collection(handle, "Favourites", container)
			c.Exec(`INSERT INTO personal_items(profile_id,item_id,watchlisted,favorite,not_interested,rating,revision,watched,last_played_at) VALUES('p',?,0,1,0,8,1,1,'2026-09-01T00:00:00Z')`, played.ID)
			c.Exec(`INSERT INTO container_personal_state(profile_id,kind,container_id,watched,watermark,revision,cleared_through) VALUES('p',?,?,1,'',1,'')`, containerKind(t, c, container.ID), container.ID)
			c.Drain()
			s := New(c.DB)
			if err := s.RebuildVisibilityClass(context.Background(), "x", visibilityRestrictionOf(nil, false, "adult")); err != nil {
				t.Fatal(err)
			}
			c.Exec(`UPDATE catalog_libraries SET retired=1 WHERE library_id='x'`)
			removeLibraryFully(t, c, "x", false)
			c.Drain()
			if n := rows(t, c, `SELECT count(*) FROM libraries WHERE id='x'`); n != 0 {
				var left []string
				for _, q := range []string{`SELECT 'entities',count(*) FROM catalog_entities`, `SELECT 'catalog_libraries',count(*) FROM catalog_libraries`} {
					var name string
					var n int
					_ = c.DB.QueryRow(q).Scan(&name, &n)
					left = append(left, fmt.Sprint(name, "=", n))
				}
				t.Fatalf("the removed library stays as a hidden row (%v; handle leftovers %v)", left, libraryLeftovers(t, c, handle))
			}
		})
	}
}

func containerKind(t *testing.T, c *catalogtest.Catalog, id int64) string {
	t.Helper()
	var kind string
	if err := c.DB.QueryRow(`SELECT k.name FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind WHERE e.id=?`, id).Scan(&kind); err != nil {
		t.Fatal(err)
	}
	return kind
}
