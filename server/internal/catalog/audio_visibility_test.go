package catalog

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestLegacyAudioVisibilityBeforePaging(t *testing.T) {
	c := catalogtest.Open(t)
	music := c.Library("music", "Music", "music", "/music")
	books := c.Library("books", "Books", "audiobook", "/books")
	hiddenArtist := c.Artist(music, "Hidden artist")
	hiddenAlbum := c.Album(hiddenArtist, "Hidden album", 2020)
	hiddenSong := c.Song(hiddenAlbum, 1, "/music/hidden.mp3", "Hidden song")
	artist := c.Artist(music, "Visible artist")
	allHiddenAlbum := c.Album(artist, "All hidden album", 2020)
	allHiddenSong := c.Song(allHiddenAlbum, 1, "/music/all-hidden.mp3", "All hidden song")
	album := c.Album(artist, "Mixed album", 2020)
	emptyAlbum := c.Album(artist, "Empty album", 2020)
	guest := c.Artist(music, "Guest artist")
	guestAlbum := c.Album(guest, "Guest album", 2020)
	guestSong := c.Song(guestAlbum, 1, "/music/guest.mp3", "Guest song")
	hiddenBook := c.Book(books, "Hidden book", "Author")
	hiddenPart := c.BookFile(hiddenBook, 1, "/books/hidden.m4b")
	book := c.Book(books, "Mixed book", "Author")
	lastBook := c.Book(books, "Last book", "Author")
	lastPart := c.BookFile(lastBook, 1, "/books/last.m4b")
	songs, parts := []catalogtest.Item{}, []catalogtest.Item{}
	for i, name := range []string{"adult", "first", "labelled", "unrated", "second"} {
		song := c.Song(album, i+1, "/music/"+name+".mp3", name)
		part := c.BookFile(book, i+1, "/books/"+name+".m4b")
		songs, parts = append(songs, song), append(parts, part)
		for _, item := range []catalogtest.Item{song, part} {
			if name == "adult" {
				c.Attributes(item.ID, "contentRating", "R")
			} else if name != "unrated" {
				c.Attributes(item.ID, "contentRating", "G")
			}
			if name == "labelled" {
				c.Attributes(item.ID, "label", "Blocked")
			}
		}
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			return compactcatalog.SetBookChaptersTx(ctx, tx, part.ID, []compactcatalog.Chapter{{Title: name, Start: 0, End: 60}})
		})
	}
	for _, item := range []catalogtest.Item{hiddenSong, allHiddenSong, hiddenPart} {
		c.Attributes(item.ID, "contentRating", "R")
	}
	for _, item := range []catalogtest.Item{guestSong, lastPart} {
		c.Attributes(item.ID, "contentRating", "G")
	}
	c.Fields(songs[0].ID, map[string]any{"poster_url": "local:hidden-song-art"})
	c.Fields(parts[0].ID, map[string]any{"poster_url": "local:hidden-part-art"})
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetSongArtistsTx(ctx, tx, songs[0].ID, []int64{artist.ID, guest.ID})
	})
	c.Exec(`INSERT INTO book_resume(profile_id,book_id,item_id,playback_id,position) VALUES('p',?,?,'saved',45000)`, book.ID, parts[0].ID)
	c.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,completed,playback_id) VALUES('p',?,12000,0,0,'visible'),('p',?,45000,0,0,'hidden')`, songs[1].ID, songs[0].ID)
	c.Exec(`INSERT INTO content_rating_pending(value_key,value) VALUES('g','G'),('r','R') ON CONFLICT(value_key) DO UPDATE SET value=excluded.value`)
	c.Drain()
	s := New(c.DB)
	if _, err := s.ClassifyPendingRatings(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.Drain()
	age := 13
	viewer := Viewer{Profile: "p", Fence: "restricted", Libraries: []string{"music", "books"}, Restrictions: identity.ContentRestrictions{MaximumAge: &age, BlockUnrated: true, BlockedLabels: []string{"blocked"}}}

	artists, next, err := s.Artists(viewer, "music", "", 1)
	if err != nil || len(artists) != 1 || artists[0].ID != artist.Public || next != artist.Public {
		t.Fatalf("artists filter before paging: %+v %q %v", artists, next, err)
	}
	artists, next, err = s.Artists(viewer, "music", next, 1)
	if err != nil || len(artists) != 1 || artists[0].ID != guest.Public || next != "" {
		t.Fatalf("artists continuation: %+v %q %v", artists, next, err)
	}
	albums, next, err := s.Albums(viewer, artist.Public, "", 1)
	if err != nil || len(albums) != 1 || albums[0].ID != album.Public || albums[0].PosterURL != "" || next != album.Public {
		t.Fatalf("albums filter and artwork: %+v %q %v", albums, next, err)
	}
	albums, next, err = s.Albums(viewer, artist.Public, next, 1)
	if err != nil || len(albums) != 1 || albums[0].ID != emptyAlbum.Public || next != "" {
		t.Fatalf("truly empty album remains visible: %+v %q %v", albums, next, err)
	}
	albums, _, err = s.Albums(viewer, guest.Public, "", 10)
	if err != nil || len(albums) != 1 || albums[0].ID != guestAlbum.Public {
		t.Fatalf("hidden guest credit exposed an album: %+v %v", albums, err)
	}
	page, next, err := s.Books(viewer, "books", "", 1)
	if err != nil || len(page) != 1 || page[0].ID != book.Public || page[0].PosterURL != "" || page[0].Resume != nil || next != book.Public {
		t.Fatalf("books filter, artwork and resume: %+v %q %v", page, next, err)
	}
	page, next, err = s.Books(viewer, "books", next, 1)
	if err != nil || len(page) != 1 || page[0].ID != lastBook.Public || next != "" {
		t.Fatalf("books continuation: %+v %q %v", page, next, err)
	}
	for _, scope := range []struct {
		kind   string
		parent catalogtest.Item
		items  []catalogtest.Item
	}{{"album", album, songs}, {"book", book, parts}} {
		first, cursor, err := s.AudioItems(viewer, scope.parent.Public, scope.kind, "", 1)
		if err != nil || len(first) != 1 || first[0].ID != scope.items[1].Public || cursor != scope.items[1].Public {
			t.Fatalf("%s first visible item: %+v %q %v", scope.kind, first, cursor, err)
		}
		if scope.kind == "album" && first[0].ProgressSeconds != 12 {
			t.Fatalf("visible personal progress lost: %+v", first[0])
		}
		second, cursor, err := s.AudioItems(viewer, scope.parent.Public, scope.kind, cursor, 1)
		if err != nil || len(second) != 1 || second[0].ID != scope.items[4].Public || cursor != "" {
			t.Fatalf("%s second visible item: %+v %q %v", scope.kind, second, cursor, err)
		}
		if _, _, err := s.AudioItems(viewer, scope.parent.Public, scope.kind, scope.items[0].Public, 1); err == nil {
			t.Fatalf("%s accepted hidden cursor", scope.kind)
		}
	}
	chapters, next, err := s.BookChapters(viewer, book.Public, "", 1)
	if err != nil || len(chapters) != 1 || chapters[0].ItemID != parts[1].Public || chapters[0].Title != "first" || next != chapters[0].ID {
		t.Fatalf("chapters filter before paging: %+v %q %v", chapters, next, err)
	}
	chapters, next, err = s.BookChapters(viewer, book.Public, next, 1)
	if err != nil || len(chapters) != 1 || chapters[0].ItemID != parts[4].Public || next != "" {
		t.Fatalf("chapters continuation: %+v %q %v", chapters, next, err)
	}
	if _, _, err := s.BookChapters(viewer, book.Public, parts[0].Public+":0", 1); err == nil {
		t.Fatal("accepted hidden chapter cursor")
	}
	for _, check := range []struct {
		name string
		read func() error
	}{
		{"artist", func() error { _, _, err := s.Albums(viewer, hiddenArtist.Public, "", 10); return err }},
		{"album", func() error { _, _, err := s.AudioItems(viewer, hiddenAlbum.Public, "album", "", 10); return err }},
		{"book files", func() error { _, _, err := s.AudioItems(viewer, hiddenBook.Public, "book", "", 10); return err }},
		{"book chapters", func() error { _, _, err := s.BookChapters(viewer, hiddenBook.Public, "", 10); return err }},
	} {
		if err := check.read(); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("wholly hidden %s: %v", check.name, err)
		}
	}
	if empty, _, err := s.AudioItems(viewer, emptyAlbum.Public, "album", "", 10); err != nil || len(empty) != 0 {
		t.Fatalf("empty visible album: %+v %v", empty, err)
	}
	if empty, _, err := s.Artists(Viewer{}, "music", "", 10); err != nil || len(empty) != 0 {
		t.Fatalf("empty viewer scope: %+v %v", empty, err)
	}
	open := viewer
	open.Restrictions = identity.ContentRestrictions{}
	all, _, err := s.AudioItems(open, album.Public, "album", "", 10)
	if err != nil || len(all) != 5 {
		t.Fatalf("unrestricted songs: %+v %v", all, err)
	}
	page, _, err = s.Books(open, "books", "", 10)
	if err != nil || len(page) != 3 || page[1].Resume == nil || page[1].Resume.ItemID != parts[0].Public || page[1].Resume.PositionSeconds != 45 {
		t.Fatalf("unrestricted saved resume: %+v %v", page, err)
	}
}
