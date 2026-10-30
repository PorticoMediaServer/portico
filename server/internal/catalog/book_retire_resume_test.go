package catalog

import (
	"context"
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

func TestEmptyBookKeepsItsOwnResumeWhenTheLastPartMoved(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("books", "Books", "audiobook", "/books")
	old := c.Book(library, "Original grouping", "Author")
	current := c.Book(library, "Tagged grouping", "Author")
	part := c.BookFile(current, 1, "/books/one.m4b")
	// Retagging moved the part; this profile still owns its prior book's resume.
	c.Exec(`INSERT INTO book_resume(profile_id,book_id,item_id,playback_id,position) VALUES('profile',?,?,'playback',45000)`, old.ID, part.ID)
	c.Write(func(ctx context.Context, tx *sql.Tx) error { return settleEmptiedBook(ctx, tx, old.ID, current.ID) })
	var retired bool
	if err := c.DB.QueryRow(`SELECT retired FROM catalog_entities WHERE id=?`, old.ID).Scan(&retired); err != nil || !retired {
		t.Fatal("personal-state book was deleted instead of retired", retired, err)
	}
	var item, position int64
	if err := c.DB.QueryRow(`SELECT item_id,position FROM book_resume WHERE profile_id='profile' AND book_id=?`, old.ID).Scan(&item, &position); err != nil || item != part.ID || position != 45000 {
		t.Fatal("resume changed", item, position, err)
	}
	var parent int64
	if err := c.DB.QueryRow(`SELECT book_id FROM catalog_book_files WHERE entity_id=?`, part.ID).Scan(&parent); err != nil || parent != current.ID {
		t.Fatal("current part changed", parent, err)
	}
}
