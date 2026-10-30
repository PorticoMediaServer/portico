package compactcatalog_test

import (
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

// Rows derived from a parent's facts follow the parent: a renamed show's
// seasons carry the new title, and a song's album-artist edge moves with its
// album's artist.
func TestDerivedRowsFollowTheirParents(t *testing.T) {
	c := catalogtest.Open(t)
	tv := c.Library("tv", "TV", "tv", "/tv")
	show := c.Show(tv, "Filename Title", 2001)
	season := c.Season(show, 1)
	c.Episode(show, season, 1, "/tv/s01e01.mkv")
	music := c.Library("mu", "Music", "music", "/mu")
	first, second := c.Artist(music, "First"), c.Artist(music, "Second")
	album := c.Album(first, "Album", 2020)
	song := c.Song(album, 1, "/mu/1.flac", "Song")
	c.Drain()
	c.Fields(show.ID, map[string]any{"title": "Provider Title"})
	c.Fields(album.ID, map[string]any{"artist_id": second.ID})
	c.Drain()
	var title string
	if err := c.DB.QueryRow(`SELECT title FROM catalog_browse_rows WHERE entity_id=?`, season.ID).Scan(&title); err != nil || title != "Provider Title · Season 1" {
		t.Fatalf("season row %q %v", title, err)
	}
	var artist int64
	if err := c.DB.QueryRow(`SELECT entity_id FROM catalog_browse_memberships WHERE item_id=? AND source=4`, song.ID).Scan(&artist); err != nil || artist != second.ID {
		t.Fatalf("song's album-artist edge %d %v, want %d", artist, err, second.ID)
	}
}

// A book's files join its author whatever the author's name folds to: the
// SQL key and the identity key are the same function.
func TestBookFilesJoinAnAccentedAuthor(t *testing.T) {
	c := catalogtest.Open(t)
	books := c.Library("b", "Books", "audiobook", "/b")
	book := c.Book(books, "Germinal", "Émile Zola")
	file := c.BookFile(book, 1, "/b/germinal.m4b")
	c.Drain()
	var author string
	if err := c.DB.QueryRow(`SELECT e.title FROM catalog_browse_memberships m JOIN catalog_entities e ON e.id=m.entity_id WHERE m.item_id=? AND m.source=7`, file.ID).Scan(&author); err != nil || author != "Émile Zola" {
		t.Fatalf("author edge %q %v", author, err)
	}
}

// A show's and a season's added date is its earliest dated episode, and it
// follows the episodes: a new earlier one, a changed date, a removed one.
func TestShowAddedFollowsItsEarliestEpisode(t *testing.T) {
	c := catalogtest.Open(t)
	tv := c.Library("tv", "TV", "tv", "/tv")
	show := c.Show(tv, "Show", 2001)
	season := c.Season(show, 1)
	first := c.Episode(show, season, 1, "/tv/s01e01.mkv")
	second := c.Episode(show, season, 2, "/tv/s01e02.mkv")
	c.Fields(first.ID, map[string]any{"added_text": "2026-03-01T00:00:00.000Z"})
	c.Fields(second.ID, map[string]any{"added_text": "2026-02-01T00:00:00.000Z"})
	c.Drain()
	added := func(id int64) string {
		t.Helper()
		var v sql.NullString
		if err := c.DB.QueryRow(`SELECT added_text FROM catalog_browse_rows WHERE entity_id=?`, id).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v.String
	}
	if added(show.ID) != "2026-02-01T00:00:00.000Z" || added(season.ID) != "2026-02-01T00:00:00.000Z" {
		t.Fatalf("show %q season %q", added(show.ID), added(season.ID))
	}
	c.Fields(first.ID, map[string]any{"added_text": "2026-01-01T00:00:00.000Z"})
	c.Drain()
	if added(show.ID) != "2026-01-01T00:00:00.000Z" {
		t.Fatalf("after an earlier date: %q", added(show.ID))
	}
	c.Delete(first.ID)
	c.Drain()
	if added(show.ID) != "2026-02-01T00:00:00.000Z" || added(season.ID) != "2026-02-01T00:00:00.000Z" {
		t.Fatalf("after the earliest left: show %q season %q", added(show.ID), added(season.ID))
	}
}
