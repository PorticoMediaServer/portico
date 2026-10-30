package catalog

import (
	"context"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

func TestAuthorBooksPublishStoredFactsAndOwnProgress(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("books", "Books", "audiobook", "/books")
	book := c.Book(lib, "The Martian", "Andy Weir")
	c.Fields(book.ID, map[string]any{"narrator": "R. C. Bray", "series": "Andy Weir", "series_position": "1"})
	file := c.BookFile(book, 1, "/books/martian.m4b")
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
	if _, err := c.DB.Exec(`INSERT INTO book_resume(profile_id,book_id,item_id,playback_id,position,unit,completed) VALUES('p',?,?,'x',500000,0,0)`, book.ID, file.ID); err != nil {
		t.Fatal(err)
	}
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"books"}}
	base := ContentRequest{Viewer: viewer, ServerID: "server", Library: "books", Profile: "p", ViewerFence: "f", Limit: 40}
	browse, err := s.BrowseEntities(viewer, BrowseRequest{ServerID: "server", Library: "books", Profile: "p", ViewerFence: "f", Pivot: "authors", Limit: 40})
	if err != nil || len(browse.Entries) != 1 {
		t.Fatalf("author browse: %+v, %v", browse, err)
	}
	author := base
	author.View = "author"
	author.EntityID = browse.Entries[0].ID
	page, err := s.Content(author)
	if err != nil {
		t.Fatal(err)
	}
	if page.Entity == nil || page.Entity.Count == nil || *page.Entity.Count != 1 {
		t.Fatalf("author count: %+v", page.Entity)
	}
	if page.Entity.Duration == nil || *page.Entity.Duration != 5400 {
		t.Fatalf("author duration: %+v", page.Entity)
	}
	if len(page.Entity.Narrators) != 1 || page.Entity.Narrators[0] != "R. C. Bray" {
		t.Fatalf("author narrators: %+v", page.Entity.Narrators)
	}
	var entry *ContentEntry
	for i := range page.Sections {
		if page.Sections[i].ID != "books" || len(page.Sections[i].Entries) != 1 {
			t.Fatalf("books section: %+v", page.Sections)
		}
		entry = &page.Sections[i].Entries[0]
	}
	if entry.Author != "Andy Weir" || entry.Duration == nil || *entry.Duration != 5400 {
		t.Fatalf("book facts: %+v", entry)
	}
	if len(entry.Narrators) != 1 || entry.Narrators[0] != "R. C. Bray" {
		t.Fatalf("book narrator: %+v", entry.Narrators)
	}
	if len(entry.Series) != 1 || entry.Series[0].Name != "Andy Weir" || entry.Series[0].Position != "1" {
		t.Fatalf("book series: %+v", entry.Series)
	}
	if entry.BookProgress == nil || entry.BookProgress.DurationMs == nil || *entry.BookProgress.DurationMs != 5400000 ||
		entry.BookProgress.PositionMs == nil || *entry.BookProgress.PositionMs != 500000 ||
		entry.BookProgress.RemainingMs == nil || *entry.BookProgress.RemainingMs != 4900000 {
		t.Fatalf("book progress: %+v", entry.BookProgress)
	}
	other := base
	other.Profile = "other"
	other.Viewer = Viewer{Profile: "other", Fence: "f", Libraries: []string{"books"}}
	other.View = "author"
	other.EntityID = browse.Entries[0].ID
	page, err = s.Content(other)
	if err != nil {
		t.Fatal(err)
	}
	for i := range page.Sections {
		if len(page.Sections[i].Entries) == 1 && page.Sections[i].Entries[0].BookProgress != nil {
			t.Fatalf("another profile's progress leaked: %+v", page.Sections[i].Entries[0].BookProgress)
		}
	}
}

func TestAuthorAggregateOmitsDurationAndNarratorsOnAPagedWindow(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("books", "Books", "audiobook", "/books")
	first := c.Book(lib, "The Martian", "Andy Weir")
	c.Fields(first.ID, map[string]any{"narrator": "R. C. Bray"})
	c.BookFile(first, 1, "/books/martian.m4b")
	second := c.Book(lib, "Project Hail Mary", "Andy Weir")
	c.Fields(second.ID, map[string]any{"narrator": "Ray Porter"})
	c.BookFile(second, 1, "/books/hail-mary.m4b")
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
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"books"}}
	browse, err := s.BrowseEntities(viewer, BrowseRequest{ServerID: "server", Library: "books", Profile: "p", ViewerFence: "f", Pivot: "authors", Limit: 40})
	if err != nil || len(browse.Entries) != 1 {
		t.Fatalf("author browse: %+v, %v", browse, err)
	}
	request := ContentRequest{Viewer: viewer, ServerID: "server", Library: "books", Profile: "p", ViewerFence: "f", Limit: 1,
		View: "author", EntityID: browse.Entries[0].ID}
	page, err := s.Content(request)
	if err != nil {
		t.Fatal(err)
	}
	if page.Entity == nil || page.Entity.Count == nil || *page.Entity.Count != 2 {
		t.Fatalf("author count: %+v", page.Entity)
	}
	if page.Entity.Duration != nil || len(page.Entity.Narrators) != 0 {
		t.Fatalf("paged author aggregate leaked: %+v", page.Entity)
	}
	var books *ContentSection
	for i := range page.Sections {
		if page.Sections[i].ID == "books" {
			books = &page.Sections[i]
		}
	}
	if books == nil || len(books.Entries) != 1 || books.NextCursor == "" {
		t.Fatalf("expected a bounded first books page: %+v", books)
	}
}
