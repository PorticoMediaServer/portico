package catalog

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func commitBookFile(t *testing.T, s *Service, db *sql.DB, tags map[string]string, inventoryOnly bool) {
	t.Helper()
	path := "/books/Listener/Listener.m4b"
	c := catalogtest.New(t, db)
	var assetToken string
	c.Write(func(ctx context.Context, writeTx *sql.Tx) error {
		_, token, err := compactcatalog.UpsertAssetTx(ctx, writeTx, compactcatalog.Asset{Path: path, Size: 1, ModifiedNS: 1, Container: "mp4", AudioCodec: "aac", Duration: 90})
		assetToken = token
		return err
	})
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	f := assets.Facts{Container: "mp4", Duration: 90, InventoryOnly: inventoryOnly}
	if !inventoryOnly {
		f.AudioCodec = "aac"
	}
	if tags != nil {
		assets.CopyEmbeddedAudioTags(&f, tags)
	}
	if err = s.commitAudio(context.Background(), tx, "books", "audiobook", "/books", assetToken, path, f); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	c.Drain()
}

// A first scan reads no tags, so the book is keyed without its author; the
// probe's tags then give it a new identity and the file moves. The book it
// left is emptied and must not stay behind as a second, fileless book.
func TestTaglessFirstImportThenTagsLeavesOneBook(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	catalogtest.New(t, db).Library("books", "Audiobooks", "audiobook", "/books")
	s := New(db)
	commitBookFile(t, s, db, nil, true)
	var first int64
	if err = db.QueryRow(`SELECT entity_id FROM catalog_books`).Scan(&first); err != nil {
		t.Fatal(err)
	}
	firstPublic, err := entityid.Public(context.Background(), db, first)
	if err != nil {
		t.Fatal(err)
	}
	commitBookFile(t, s, db, map[string]string{"title": "Listener", "album": "Listener", "artist": "Test Author", "narrator": "Test Narrator"}, false)
	var live int
	var author string
	if err = db.QueryRow(`SELECT count(*),COALESCE(max(b.author),'') FROM catalog_books b JOIN catalog_entities e ON e.id=b.entity_id AND e.retired=0`).Scan(&live, &author); err != nil {
		t.Fatal(err)
	}
	if live != 1 || author != "Test Author" {
		t.Fatalf("live books %d, author %q; want one book by Test Author", live, author)
	}
	// A page opened on the book while the scan was reading tags still reaches it.
	var book int64
	if err = db.QueryRow(`SELECT entity_id FROM catalog_books`).Scan(&book); err != nil {
		t.Fatal(err)
	}
	if got, err := entityid.Resolve(context.Background(), db, firstPublic); err != nil || got != book {
		t.Fatalf("the first book id leads to %d (%v), want %d", got, err, book)
	}
}
