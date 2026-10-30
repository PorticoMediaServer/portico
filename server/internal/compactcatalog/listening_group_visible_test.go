package compactcatalog

import (
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestBookGroupVisibleDistinctBooksRestartAndRemoval(t *testing.T) {
	path := filepath.Join(t.TempDir(), "groups.sqlite")
	db, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	lib := cc1Library(t, db, "books", "Books", "audiobook", "/books")
	book := cc1Entity(t, db, Entity{Library: lib, Kind: Book, Key: "book:book", Title: "Book"},
		map[string]any{"library_id": lib, "local_key": "book", "author": "Author", "provider_match_status": "matched"})
	fileA := cc1Entity(t, db, Entity{Library: lib, Kind: Part, Parent: book, Key: "file:a", Title: "A"},
		map[string]any{"book_id": book})
	fileB := cc1Entity(t, db, Entity{Library: lib, Kind: Part, Parent: book, Key: "file:b", Title: "B"},
		map[string]any{"book_id": book})
	cc1Exec(t, db, `INSERT INTO listening_book_groups(id,library_id,kind,name,name_key) VALUES('author','books','author','Author','author')`)
	cc1Drain(t, db)
	class := cc1Class(t, db, lib, "class")
	cc1Visible(t, db, class, fileA, 9)
	cc1Visible(t, db, class, fileB, 9)
	cc1Drain(t, db)
	if got := cc1Scalar(t, db, `SELECT total FROM catalog_book_group_visible_counts WHERE group_id=(SELECT id FROM catalog_book_groups WHERE token='author')`); got != 1 {
		t.Fatalf("two visible files counted as %d books", got)
	}
	cc1Exec(t, db, `DELETE FROM compact_visibility_rows WHERE entity_id=?`, fileA)
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	cc1Drain(t, db)
	if got := cc1Scalar(t, db, `SELECT total FROM catalog_book_group_visible_counts WHERE group_id=(SELECT id FROM catalog_book_groups WHERE token='author')`); got != 1 {
		t.Fatalf("one remaining visible file counted as %d books", got)
	}
	cc1Exec(t, db, `DELETE FROM compact_visibility_rows WHERE entity_id=?`, fileB)
	cc1Drain(t, db)
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_book_group_visible_counts WHERE group_id=(SELECT id FROM catalog_book_groups WHERE token='author')`); got != 0 {
		t.Fatalf("hidden book retained group count: %d", got)
	}
	cc1Visible(t, db, class, fileA, 9)
	cc1Drain(t, db)
	if got := cc1Scalar(t, db, `SELECT total FROM catalog_book_group_visible_counts WHERE group_id=(SELECT id FROM catalog_book_groups WHERE token='author')`); got != 1 {
		t.Fatalf("replay count = %d", got)
	}
	cc1Exec(t, db, `DELETE FROM listening_book_groups WHERE id='author'`)
	cc1Drain(t, db)
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_book_group_visible_counts WHERE group_id=(SELECT id FROM catalog_book_groups WHERE token='author')`); got != 0 {
		t.Fatalf("retired group kept visible count: %d", got)
	}
}
