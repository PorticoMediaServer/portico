package catalog

import (
	"context"
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

func TestCompactContentContainersAndTitleRename(t *testing.T) {
	c := catalogtest.Open(t)
	tv := c.Library("tv", "TV", "tv", "/tv")
	music := c.Library("music", "Music", "music", "/music")
	books := c.Library("books", "Books", "audiobook", "/books")

	show := c.Show(tv, "Series", 2020)
	season := c.Season(show, 1)
	episode := c.Episode(show, season, 1, "/tv/Series/Season 1/Pilot.mkv")
	artist := c.Artist(music, "Artist")
	album := c.Album(artist, "Record", 2021)
	song := c.Song(album, 1, "/music/Record/Track.flac", "Track")
	book := c.Book(books, "Story", "Author")
	bookFile := c.BookFile(book, 1, "/books/Story/Part.m4b")
	c.Drain()

	s := New(c.DB)
	check := func(library, view, entity, wantID, wantSubtitle string) ContentEntry {
		t.Helper()
		entries, _, count, err := s.contentEntities(ContentRequest{Library: library, View: view, EntityID: entity, Viewer: Viewer{Libraries: []string{library}}}, "", 20)
		if err != nil || count != 1 || len(entries) != 1 || entries[0].ID != wantID || entries[0].Subtitle != wantSubtitle {
			t.Fatalf("%s/%s: count=%d entries=%+v err=%v", library, view, count, entries, err)
		}
		return entries[0]
	}
	check("tv", "browse", "", show.Public, "")
	check("music", "browse", "", artist.Public, "")
	check("music", "artist", artist.Public, album.Public, "")
	check("books", "browse", "", book.Public, "Author")

	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		_, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{
			Library: tv, Kind: compactcatalog.Show, Key: compactcatalog.ShowKey("series:2020"), Title: "Renamed Series", Year: 2020,
		})
		return err
	})
	c.Drain()
	if got := check("tv", "browse", "", show.Public, ""); got.Title != "Renamed Series" {
		t.Fatalf("renamed show title=%q, want Renamed Series", got.Title)
	}

	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.UnlinkAssetTx(ctx, tx, episode.ID, episode.Asset)
	})
	c.Drain()
	if entries, _, count, err := s.contentEntities(ContentRequest{Library: "tv", View: "browse", Viewer: Viewer{Libraries: []string{"tv"}}}, "", 20); err != nil || count != 0 || len(entries) != 0 {
		t.Fatalf("show remained after its episode file link was removed: count=%d entries=%+v err=%v", count, entries, err)
	}

	c.Delete(song.ID)
	c.Drain()
	if entries, _, count, err := s.contentEntities(ContentRequest{Library: "music", View: "browse", Viewer: Viewer{Libraries: []string{"music"}}}, "", 20); err != nil || count != 0 || len(entries) != 0 {
		t.Fatalf("artist remained after its song was removed: count=%d entries=%+v err=%v", count, entries, err)
	}

	c.Delete(bookFile.ID)
	c.Drain()
	if entries, _, count, err := s.contentEntities(ContentRequest{Library: "books", View: "browse", Viewer: Viewer{Libraries: []string{"books"}}}, "", 20); err != nil || count != 0 || len(entries) != 0 {
		t.Fatalf("book remained after its file was removed: count=%d entries=%+v err=%v", count, entries, err)
	}
}
