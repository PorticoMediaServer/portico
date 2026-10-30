package catalog

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// Untagged books arranged the Audiobookshelf way are named by their title
// folders, without the place in the series, and a "Vol. 2 - Title" folder is a
// book of its own while a bare "Disc 1" inside it is that book's disc.
func TestUntaggedSeriesFoldersNameTheirBooks(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	c := catalogtest.New(t, db)
	c.Library("books", "Audiobooks", "audiobook", "/books")
	s := New(db)
	for _, path := range []string{
		"/books/Fixture Author/Fixture Saga/Book 1 - First Book/01.mp3",
		"/books/Fixture Author/Fixture Saga/Book 1 - First Book/02.mp3",
		"/books/Fixture Author/Fixture Saga/Vol. 2 - Second Book/Disc 1/01.mp3",
		"/books/Fixture Author/Fixture Saga/Vol. 2 - Second Book/Disc 2/01.mp3",
		"/books/Fixture Author/(1999) - Standalone {Fixture Narrator}/book.mp3",
	} {
		var token string
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			var err error
			_, token, err = compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: 1, ModifiedNS: 1, Container: "mp3", AudioCodec: "mp3", Duration: 90})
			return err
		})
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			return s.commitAudio(ctx, tx, "books", "audiobook", "/books", token, path, assets.Facts{Container: "mp3", AudioCodec: "mp3", Duration: 90})
		})
	}
	c.Drain()
	rows, err := db.Query(`SELECT b.title,COUNT(*),GROUP_CONCAT(COALESCE(f.disc_number,0)) FROM catalog_book_files f JOIN catalog_entities b ON b.id=f.book_id GROUP BY b.id ORDER BY b.title`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var books []string
	for rows.Next() {
		var title, discs string
		var files int
		if err = rows.Scan(&title, &files, &discs); err != nil {
			t.Fatal(err)
		}
		books = append(books, title+":"+discs)
	}
	if want := "First Book:0,0|Second Book:1,2|Standalone:0"; strings.Join(books, "|") != want {
		t.Fatalf("books %v, want %s", books, want)
	}
}
