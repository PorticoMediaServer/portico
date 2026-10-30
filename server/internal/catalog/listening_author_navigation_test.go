package catalog

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

func TestAuthorBrowseAndBookContextOpenTheSameAuthor(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("books", "Books", "audiobook", "/books")
	c.Library("other", "Other books", "audiobook", "/other")
	book := c.Book(lib, "Browser Book", "Élodie Author")
	c.BookFile(book, 1, "/books/book.m4b")
	c.Drain()
	s := New(c.DB)
	for {
		more, err := s.RefreshListeningGroups(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !more {
			break
		}
	}
	c.Drain()
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"books", "other"}}
	browse, err := s.BrowseEntities(viewer, BrowseRequest{ServerID: "server", Library: "books", Profile: "p", ViewerFence: "f", Pivot: "authors", Limit: 40})
	if err != nil || len(browse.Entries) != 1 {
		t.Fatalf("author browse: %+v, %v", browse, err)
	}
	authorID := browse.Entries[0].ID
	base := ContentRequest{Viewer: viewer, ServerID: "server", Library: "books", Profile: "p", ViewerFence: "f", Limit: 40}
	author := base
	author.View = "author"
	author.EntityID = authorID
	page, err := s.Content(author)
	if err != nil || len(page.Sections) != 1 || len(page.Sections[0].Entries) != 1 || page.Sections[0].Entries[0].ID != book.Public {
		t.Fatalf("browse author opens its book: %+v, %v", page, err)
	}
	if page.Entity == nil || page.Entity.ID != authorID || page.Entity.Title != "Élodie Author" {
		t.Fatalf("author hero stays in loading state: %+v", page.Entity)
	}
	listing := base
	listing.View = "authors"
	page, err = s.Content(listing)
	if err != nil || len(page.Sections) != 1 || page.Sections[0].Entries[0].ID != authorID {
		t.Fatalf("authored listing identity: %+v, %v", page, err)
	}
	detail := base
	detail.View = "book"
	detail.EntityID = book.Public
	page, err = s.Content(detail)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, section := range page.Sections {
		for _, entry := range section.Entries {
			if entry.Kind == "author" {
				found = true
				if entry.ID != authorID || entry.Navigation.EntityID != authorID {
					t.Fatalf("book context uses another author: %+v", entry)
				}
			}
		}
	}
	if !found {
		t.Fatal("book author context absent")
	}
	author.Library = "other"
	if _, err = s.Content(author); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cross-library author accepted: %v", err)
	}
	var oldToken string
	if err = c.DB.QueryRow(`SELECT token FROM catalog_book_groups WHERE kind=1`).Scan(&oldToken); err != nil {
		t.Fatal(err)
	}
	author.Library = "books"
	author.EntityID = oldToken
	if _, err = s.Content(author); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("obsolete author token accepted: %v", err)
	}
}
