package catalog

import (
	"context"
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// Two spellings of one author are one author once the owner merges them:
// every book of either is on the author that stays, and the absorbed name
// leads there too.
func TestOwnerMergesTwoAuthors(t *testing.T) {
	c := catalogtest.Open(t)
	books := c.Library("books", "Books", "audiobook", "/books")
	hobbit := c.Book(books, "The Hobbit", "J.R.R. Tolkien")
	c.BookFile(hobbit, 1, "/books/hobbit/01.m4b")
	silmarillion := c.Book(books, "The Silmarillion", "JRR Tolkien")
	part := c.BookFile(silmarillion, 1, "/books/silmarillion/01.m4b")
	c.Drain()
	author := func(name string) int64 {
		t.Helper()
		var id int64
		if err := c.DB.QueryRow(`SELECT e.id FROM catalog_identities i JOIN catalog_entities e ON e.public_id=i.public_id WHERE i.source_key=?`, compactcatalog.AuthorKey(name)).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	keep, other := author("J.R.R. Tolkien"), author("JRR Tolkien")
	if keep == other {
		t.Fatal("two spellings started as one author")
	}
	if err := New(c.DB).MergeTitles(context.Background(), c.Public(other), c.Public(keep), func(*sql.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	c.Drain()
	if got := author("JRR Tolkien"); got != keep {
		t.Fatalf("the absorbed spelling leads to %d, want %d", got, keep)
	}
	if n := rows(t, c, `SELECT count(*) FROM catalog_browse_memberships WHERE entity_id=? AND item_id=?`, keep, part.ID); n != 1 {
		t.Fatal("the absorbed author's book is not on the author that stayed")
	}
	if n := rows(t, c, `SELECT count(*) FROM catalog_entities WHERE kind=? AND retired=0`, int(compactcatalog.Author)); n != 1 {
		t.Fatalf("authors after the merge: %d", n)
	}
}
