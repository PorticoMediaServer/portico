package catalog

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// The batched per-kind reads are an optimisation, and the per-item reads remain
// the definition of what the answer is. The only way to keep that true is to
// check it: this builds a page with episodes, songs and book files on it, loads
// the detail both ways, and asserts they agree field for field.
func TestPageEnrichmentMatchesPerItemReads(t *testing.T) {
	db, e := persistence.Open(filepath.Join(t.TempDir(), "enrich.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s := New(db)
	c := catalogtest.New(t, db)
	tv := c.Library("tv", "Shows", "tv", "/tv")
	music := c.Library("music", "Music", "music", "/music")
	books := c.Library("books", "Books", "audiobook", "/books")

	// Two episodes across two seasons of one show, so the season number, the
	// numbering and the ordering basis all have something to say.
	show := c.Show(tv, "Harbour", 2001)
	season1, season2 := c.Season(show, 1), c.Season(show, 2)
	e1 := c.Episode(show, season1, 1, "/tv/Harbour/Season 1/Episode e1.mkv")
	e2 := c.Episode(show, season2, 3, "/tv/Harbour/Season 2/Episode e2.mkv")
	// One episode with an unknown source boundary, which is its own branch.
	c.Exec(`INSERT INTO episode_asset_boundaries(item_id,asset_id,status) VALUES(?,?,'unknown_multi_episode')`, e2.ID, e2.Token)

	// Two songs on one album, one of them with a second credited artist so the
	// concatenated artist list is not trivial.
	artist := c.Artist(music, "Aurora")
	guest := c.Artist(music, "Beacon")
	album := c.Album(artist, "Tides", 1999)
	s1 := c.Song(album, 1, "/music/Tides/Track s1.flac", "Track s1")
	s2 := c.Song(album, 2, "/music/Tides/Track s2.flac", "Track s2")
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetSongArtistsTx(ctx, tx, s2.ID, []int64{artist.ID, guest.ID})
	})

	// Two book files of one book, with a local metadata policy and payload so the
	// policy and metadata branches are both exercised.
	bookKey := "harbour lights|author"
	book := c.Entity(compactcatalog.Entity{Library: books, Kind: compactcatalog.Book, Key: compactcatalog.BookKey(bookKey), Title: "Harbour Lights"}, map[string]any{"library_id": books, "local_key": bookKey, "author": "Author", "narrator": "Narrator", "local_metadata_payload": `{"title":"Harbour Lights"}`})
	b1 := c.BookFile(book, 1, "/books/Harbour Lights/Part b1.m4b")
	b2 := c.BookFile(book, 2, "/books/Harbour Lights/Part b2.m4b")
	if _, err := db.Exec(`INSERT INTO audio_metadata_policies(library_id,revision,local_mode) VALUES('books',3,'prefer')
 ON CONFLICT(library_id) DO UPDATE SET revision=3,local_mode='prefer'`); err != nil {
		t.Fatal(err)
	}

	ids := []string{e1.Public, e2.Public, s1.Public, s2.Public, b1.Public, b2.Public}
	c.Drain()
	page, err := s.mediaPage("profile", ids, false)
	if err != nil {
		t.Fatalf("page: %v", err)
	}
	if len(page) != len(ids) {
		t.Fatalf("the page returned %d of %d items", len(page), len(ids))
	}
	for _, item := range page {
		switch item.Kind {
		case "episode":
			expected, e := s.episodeInfo(item.ID)
			if e != nil {
				t.Fatalf("%s per-item read: %v", item.ID, e)
			}
			if !reflect.DeepEqual(item.Episode, expected) {
				t.Fatalf("%s: batched %+v, per-item %+v", item.ID, item.Episode, expected)
			}
		case "song":
			expected, e := s.songInfo(item.ID)
			if e != nil && e != sql.ErrNoRows {
				t.Fatalf("%s per-item read: %v", item.ID, e)
			}
			if !reflect.DeepEqual(item.Song, expected) {
				t.Fatalf("%s: batched %+v, per-item %+v", item.ID, item.Song, expected)
			}
		case "audiobook_file":
			expected, e := s.bookFileInfo(item.ID)
			if e != nil {
				t.Fatalf("%s per-item read: %v", item.ID, e)
			}
			if !reflect.DeepEqual(item.BookFile, expected) {
				t.Fatalf("%s: batched %+v, per-item %+v", item.ID, item.BookFile, expected)
			}
		default:
			t.Fatalf("%s came back as kind %q", item.ID, item.Kind)
		}
	}

	// And the point of it: the whole page costs a handful of statements rather
	// than one to six per item.
	ctx, cost := dbwork.Measure(context.Background())
	if _, err = s.WithContext(ctx).mediaPage("profile", ids, false); err != nil {
		t.Fatal(err)
	}
	statements := cost().Statements
	t.Logf("a six-item mixed page cost %d statements", statements)
	if statements > 10 {
		t.Fatalf("a six-item mixed page cost %d statements; the per-kind reads are not batched", statements)
	}
}
