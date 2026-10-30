package catalogtest

import "testing"

func TestFixturesDerive(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := Open(t)
	films := c.Library("films", "Films", "movie", "/films")
	heat := c.Movie(films, "/films/Heat.mkv", "Heat", 1995)
	tv := c.Library("tv", "TV", "tv", "/tv")
	show := c.Show(tv, "Lost", 2004)
	ep := c.Episode(show, c.Season(show, 1), 1, "/tv/Lost/S01E01.mkv")
	music := c.Library("music", "Music", "music", "/music")
	song := c.Song(c.Album(c.Artist(music, "Muse"), "Absolution", 2003), 1, "/music/Muse/Absolution/01.flac", "Intro")
	books := c.Library("books", "Books", "audiobook", "/books")
	part := c.BookFile(c.Book(books, "Dune", "Frank Herbert"), 1, "/books/Dune/01.m4b")
	c.Collection(films, "Favourites", heat)
	c.Genres(heat.ID, "tmdb", "Crime")
	c.Drain()
	for _, it := range []Item{heat, ep, song, part} {
		var available int
		if err := c.DB.QueryRow(`SELECT available FROM catalog_browse_rows WHERE entity_id=?`, it.ID).Scan(&available); err != nil || available != 1 {
			t.Fatalf("%s: browse row available=%d err=%v", it.Public, available, err)
		}
		if c.ID(it.Public) != it.ID {
			t.Fatal("public id does not resolve")
		}
	}
	var members int
	if err := c.DB.QueryRow(`SELECT count(*) FROM catalog_browse_memberships`).Scan(&members); err != nil || members < 8 {
		t.Fatalf("memberships: %d %v", members, err)
	}
}
