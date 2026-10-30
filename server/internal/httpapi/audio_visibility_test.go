package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestLegacyAudioRoutesUseCurrentViewer(t *testing.T) {
	d, session := logoutHTTPFixture(t)
	c := catalogtest.New(t, d.DB)
	music := c.Library("music", "Music", "music", "/music")
	books := c.Library("books", "Books", "audiobook", "/books")
	hiddenArtist := c.Artist(music, "Hidden artist")
	hiddenAlbum := c.Album(hiddenArtist, "Hidden album", 2020)
	hiddenSong := c.Song(hiddenAlbum, 1, "/music/hidden.mp3", "Hidden song")
	artist := c.Artist(music, "Visible artist")
	otherHiddenAlbum := c.Album(artist, "Other hidden album", 2020)
	otherHiddenSong := c.Song(otherHiddenAlbum, 1, "/music/other-hidden.mp3", "Other hidden song")
	album := c.Album(artist, "Mixed album", 2020)
	blockedSong := c.Song(album, 1, "/music/blocked.mp3", "Blocked song")
	song := c.Song(album, 2, "/music/visible.mp3", "Visible song")
	hiddenBook := c.Book(books, "Hidden book", "Author")
	hiddenPart := c.BookFile(hiddenBook, 1, "/books/hidden.m4b")
	book := c.Book(books, "Mixed book", "Author")
	blockedPart := c.BookFile(book, 1, "/books/blocked.m4b")
	part := c.BookFile(book, 2, "/books/visible.m4b")
	for _, item := range []catalogtest.Item{hiddenSong, otherHiddenSong, blockedSong, hiddenPart, blockedPart} {
		c.Attributes(item.ID, "contentRating", "R")
	}
	for _, item := range []catalogtest.Item{song, part} {
		c.Attributes(item.ID, "contentRating", "G")
	}
	for _, item := range []catalogtest.Item{hiddenPart, blockedPart, part} {
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			return compactcatalog.SetBookChaptersTx(ctx, tx, item.ID, []compactcatalog.Chapter{{Title: item.Public, Start: 0, End: 30}})
		})
	}
	p, err := d.Identity.Authenticate(session.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	c.Exec(`INSERT INTO book_resume(profile_id,book_id,item_id,playback_id,position) VALUES(?,?,?,'saved',4000)`, identity.PersonalKey(p.Viewer), book.ID, blockedPart.ID)
	c.Exec(`INSERT INTO content_rating_pending(value_key,value) VALUES('g','G'),('r','R') ON CONFLICT(value_key) DO UPDATE SET value=excluded.value`)
	c.Drain()
	if _, err := d.Catalog.ClassifyPendingRatings(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.Drain()
	h := New(d)
	cached := map[string]string{}
	for _, path := range []string{
		"/v1/libraries/music/artists", "/v1/libraries/books/books",
		"/v1/artists/" + artist.Public + "/albums", "/v1/albums/" + album.Public + "/songs",
		"/v1/books/" + book.Public + "/files", "/v1/books/" + book.Public + "/chapters",
	} {
		w := editorRequest(t, h, "GET", path, session.AccessToken, "", nil)
		if w.Code != 200 || w.Header().Get("ETag") == "" {
			t.Fatalf("unrestricted page %s: %d %s", path, w.Code, w.Body.String())
		}
		cached[path] = w.Header().Get("ETag")
	}
	if _, err := dbwork.ExecWrite(context.Background(), d.DB, dbwork.ClassInteractive, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('profile',13,0)`); err != nil {
		t.Fatal(err)
	}
	hidden := []string{hiddenArtist.Public, hiddenAlbum.Public, hiddenSong.Public, otherHiddenAlbum.Public, otherHiddenSong.Public, blockedSong.Public, hiddenBook.Public, hiddenPart.Public, blockedPart.Public}
	assertHidden := func(body string) {
		t.Helper()
		for _, id := range hidden {
			if strings.Contains(body, id) {
				t.Fatalf("hidden identifier %s leaked: %s", id, body)
			}
		}
	}
	for _, route := range []struct{ path, collection, first string }{
		{"/v1/libraries/music/artists", "artists", artist.Public},
		{"/v1/libraries/books/books", "books", book.Public},
		{"/v1/artists/" + artist.Public + "/albums", "albums", album.Public},
		{"/v1/albums/" + album.Public + "/songs", "songs", song.Public},
		{"/v1/books/" + book.Public + "/files", "files", part.Public},
		{"/v1/books/" + book.Public + "/chapters", "chapters", part.Public + ":0"},
	} {
		w := editorRequest(t, h, "GET", route.path+"?limit=1", session.AccessToken, "", nil)
		if w.Code != 200 {
			t.Fatalf("restricted page %s: %d %s", route.path, w.Code, w.Body.String())
		}
		assertHidden(w.Body.String())
		var result map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		var rows []struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(result[route.collection], &rows); err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].ID != route.first {
			t.Fatalf("%s did not filter before paging: %s", route.path, w.Body.String())
		}
	}
	for _, path := range []string{
		"/v1/artists/" + hiddenArtist.Public + "/albums",
		"/v1/albums/" + hiddenAlbum.Public + "/songs",
		"/v1/books/" + hiddenBook.Public + "/files",
		"/v1/books/" + hiddenBook.Public + "/chapters",
	} {
		w := editorRequest(t, h, "GET", path, session.AccessToken, "", nil)
		if w.Code != 404 {
			t.Fatalf("hidden container %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	for path, tag := range cached {
		w := editorRequestWithHeader(t, h, "GET", path, session.AccessToken, map[string]string{"If-None-Match": tag})
		if w.Code != 200 || w.Header().Get("ETag") == "" || w.Header().Get("ETag") == tag {
			t.Fatalf("%s reused pre-restriction validator: %d %s", path, w.Code, w.Body.String())
		}
		assertHidden(w.Body.String())
		current := w.Header().Get("ETag")
		w = editorRequestWithHeader(t, h, "GET", path, session.AccessToken, map[string]string{"If-None-Match": current})
		if w.Code != 304 {
			t.Fatalf("%s current validator did not revalidate: %d %s", path, w.Code, w.Body.String())
		}
	}
}

// A cached page still has to pass current library authorization before a 304.
func TestLegacyAudioPagesReauthorizeBeforeNotModified(t *testing.T) {
	d, session := logoutHTTPFixture(t)
	c := catalogtest.New(t, d.DB)
	music := c.Library("music", "Music", "music", "/music")
	books := c.Library("books", "Books", "audiobook", "/books")
	artist := c.Artist(music, "Artist")
	album := c.Album(artist, "Album", 2020)
	c.Song(album, 1, "/music/song.mp3", "Song")
	book := c.Book(books, "Book", "Author")
	c.BookFile(book, 1, "/books/part.m4b")
	c.Drain()
	h := New(d)
	paths := []string{
		"/v1/libraries/music/artists", "/v1/libraries/books/books",
		"/v1/artists/" + artist.Public + "/albums", "/v1/albums/" + album.Public + "/songs",
		"/v1/books/" + book.Public + "/files", "/v1/books/" + book.Public + "/chapters",
	}
	cached := make(map[string]string)
	for _, path := range paths {
		w := editorRequest(t, h, "GET", path, session.AccessToken, "", nil)
		if w.Code != 200 || w.Header().Get("ETag") == "" {
			t.Fatalf("initial page %s: %d %s", path, w.Code, w.Body.String())
		}
		cached[path] = w.Header().Get("ETag")
	}
	if _, err := dbwork.ExecWrite(t.Context(), d.DB, dbwork.ClassSecurityFence, `UPDATE direct_profiles SET allowed_libraries='[]',revision=revision+1 WHERE id='profile'`); err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		w := editorRequestWithHeader(t, h, "GET", path, session.AccessToken, map[string]string{"If-None-Match": cached[path]})
		if w.Code != 404 || w.Header().Get("ETag") != "" {
			t.Fatalf("revoked library %s: %d %s", path, w.Code, w.Body.String())
		}
	}
}
