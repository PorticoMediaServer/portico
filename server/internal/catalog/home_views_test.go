package catalog

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestHomeSavedViewKeepsOwnerLibraryAndContinuationFences(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("books", "Audiobooks", "audiobook", "/books")
	books := []catalogtest.Item{}
	for _, title := range []string{"Alpha (Unabridged)", "Beta: Special Edition", "Gamma"} {
		book := c.Book(library, title, "Author")
		part := c.BookFile(book, 1, "/books/"+title+".flac")
		c.Attributes(part.ID, "contentRating", "PG")
		books = append(books, book)
	}
	c.Drain()
	s := New(c.DB)
	actor := ResourceActor{Authority: "local", AccountID: "account", ProfileID: "profile"}
	name := "Books"
	definition := SavedViewDefinition{LibraryID: "books", Pivot: "books", Sort: titleAscending(), Presentation: "grid"}
	made, err := s.MutateSavedResource(actor, "", "create", SavedResourceMutation{OperationID: "home-view-create", Kind: "view", Name: &name, Definition: &definition}, func(*sql.Tx) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	rowID := homeViewPrefix + made.ResourceID
	r := HomeRequest{Viewer: Viewer{Profile: actorKey(actor), Fence: "owner", Libraries: []string{"books"}}, RowOrder: []string{rowID}}
	page, err := s.HomeSingleRow(r, rowID, HomeRowPage{Limit: 2})
	if err != nil || len(page.Entries) != 2 || rowTotal(page) != 3 || page.NextCursor == "" || page.Entries[0].ID != books[0].Public || page.Entries[0].Title != "Alpha (Unabridged)" || page.Entries[1].Title != "Beta: Special Edition" {
		t.Fatalf("owner page: %+v %v", page, err)
	}
	// The same raw profile ID in a different account or authority is not the
	// resource owner. Possessing its opaque row ID cannot make it readable.
	for _, other := range []ResourceActor{
		{Authority: "local", AccountID: "other", ProfileID: "profile"},
		{Authority: "hosted", AccountID: "account", ProfileID: "profile"},
	} {
		foreign := r
		foreign.Viewer.Profile = actorKey(other)
		if _, err := s.HomeSingleRow(foreign, rowID, HomeRowPage{Limit: 2}); !errors.Is(err, ErrHomeRowUnknown) {
			t.Fatalf("foreign owner tuple was admitted: %+v %v", other, err)
		}
	}
	denied := r
	denied.Viewer.Libraries = []string{}
	if _, err := s.HomeSingleRow(denied, rowID, HomeRowPage{Limit: 2}); !errors.Is(err, ErrHomeRowUnknown) {
		t.Fatalf("empty library scope was admitted: %v", err)
	}
	changedFence := r
	changedFence.Viewer.Fence = "changed-authority"
	if _, err := s.HomeSingleRow(changedFence, rowID, HomeRowPage{Limit: 2, Cursor: page.NextCursor}); !errors.Is(err, ErrCursor) {
		t.Fatalf("cursor crossed viewer fence: %v", err)
	}
	// Content restrictions narrow the stored query rather than changing its
	// definition or granting access through the unrestricted resource owner.
	c.Attributes(books[1].ID, "contentRating", "R")
	var part int64
	if err := c.DB.QueryRow(`SELECT entity_id FROM catalog_book_files WHERE book_id=?`, books[1].ID).Scan(&part); err != nil {
		t.Fatal(err)
	}
	c.Attributes(part, "contentRating", "R")
	c.Drain()
	age := 12
	restricted := r
	restricted.Viewer.Restrictions = identity.ContentRestrictions{MaximumAge: &age, BlockUnrated: true}
	visible, err := s.HomeSingleRow(restricted, rowID, HomeRowPage{Limit: 2})
	if err != nil || rowTotal(visible) != 0 || len(visible.Entries) != 0 {
		t.Fatalf("pending ratings did not stay closed: %+v %v", visible, err)
	}
	if _, err := s.ClassifyPendingRatings(context.Background()); err != nil {
		t.Fatal(err)
	}
	visible, err = s.HomeSingleRow(restricted, rowID, HomeRowPage{Limit: 2})
	if err != nil || rowTotal(visible) != 2 || len(visible.Entries) != 2 || visible.Entries[0].ID != books[0].Public || visible.Entries[1].ID != books[2].Public {
		t.Fatalf("restriction did not narrow saved query: %+v %v", visible, err)
	}
	page, err = s.HomeSingleRow(r, rowID, HomeRowPage{Limit: 2})
	if err != nil || page.NextCursor == "" {
		t.Fatalf("current owner page: %+v %v", page, err)
	}
	definition.Sort[0].Direction = "desc"
	updated, err := s.MutateSavedResource(actor, made.ResourceID, "update", SavedResourceMutation{OperationID: "home-view-sort", ExpectedRevision: made.Revision, Definition: &definition}, func(*sql.Tx) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.HomeSingleRow(r, rowID, HomeRowPage{Limit: 2, Cursor: page.NextCursor}); !errors.Is(err, ErrStaleContinuation) {
		t.Fatalf("cursor crossed changed view definition: %v", err)
	}
	if _, err := s.MutateSavedResource(actor, made.ResourceID, "delete", SavedResourceMutation{OperationID: "home-view-delete", ExpectedRevision: updated.Revision}, func(*sql.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HomeSingleRow(r, rowID, HomeRowPage{Limit: 2}); !errors.Is(err, ErrHomeRowUnknown) {
		t.Fatalf("deleted view remained readable: %v", err)
	}
}
