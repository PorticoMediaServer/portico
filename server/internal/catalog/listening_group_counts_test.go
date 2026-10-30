package catalog

import (
	"context"
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

func TestRestrictedBookGroupCountTracksVisibleBooks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, c, names := phase34ListeningFixture(t)
	var books int64
	if err := c.DB.QueryRow(`SELECT id FROM catalog_libraries WHERE library_id='books'`).Scan(&books); err != nil {
		t.Fatal(err)
	}
	// A second series proves the author link follows this series' own visible
	// book: "Hidden" is rated R below, while its author also owns a visible
	// book in another series. "Visible" keeps this series reachable.
	hidden, visible := c.Book(books, "Hidden", "Author"), c.Book(books, "Visible", "Other Author")
	hiddenPart, visiblePart := c.BookFile(hidden, 1, "/books/hidden.m4b"), c.BookFile(visible, 1, "/books/visible.m4b")
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for _, part := range []struct {
			item  catalogtest.Item
			index string
		}{{hiddenPart, "1"}, {visiblePart, "2"}} {
			if _, err := tx.ExecContext(ctx, `INSERT INTO audio_tag_evidence(library_id,asset_id,field,source,value) VALUES('books',?,'series','portico_json','Other Saga'),('books',?,'series_index','portico_json',?)`, part.item.Token, part.item.Token, part.index); err != nil {
				return err
			}
		}
		return nil
	})
	c.Drain()
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
	if _, err := c.DB.Exec(`INSERT INTO content_rating_ages(value_key,minimum_age) VALUES('r',17),('pg',10)`); err != nil {
		t.Fatal(err)
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		if err := compactcatalog.SetAttributesTx(ctx, tx, names["part-b1"].ID, "contentRating", []string{"R"}); err != nil {
			return err
		}
		return compactcatalog.SetAttributesTx(ctx, tx, hiddenPart.ID, "contentRating", []string{"R"})
	})
	c.Drain()
	if more, err := s.ClassifyPendingRatings(context.Background()); err != nil || more {
		t.Fatal("pending ratings", more, err)
	}
	if err := s.RebuildVisibilityClass(context.Background(), "books", phase34RestrictionOf(phase34Ceiling(13), false)); err != nil {
		t.Fatal(err)
	}
	c.Drain()
	restriction := phase34RestrictionOf(phase34Ceiling(13), false)
	r := phase34ListeningRequest("books", "series", "")
	r.Viewer.Restrictions = restriction
	page, err := s.Content(r)
	if err != nil {
		t.Fatal(err)
	}
	if sections := listeningSectionIDs(page); sections != "groups" {
		t.Fatalf("restricted series sections %q: %+v", sections, page.Sections)
	}
	groups := listeningSectionByID(t, page, "groups")
	var saga, otherSaga string
	for name, target := range map[string]*string{"Saga": &saga, "Other Saga": &otherSaga} {
		if err := c.DB.QueryRow(`SELECT token FROM catalog_book_groups WHERE kind=2 AND name=?`, name).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if len(groups.Entries) != 2 {
		t.Fatalf("restricted series set: %+v", groups)
	}
	// Both series own one visible book; identify each by its canonical id.
	for _, want := range []struct{ id, title string }{{saga, "Saga"}, {otherSaga, "Other Saga"}} {
		entry := listeningEntryByID(t, groups, want.id)
		if entry.Title != want.title || entry.Count == nil || *entry.Count != 1 {
			t.Fatalf("restricted series %q: %+v", want.title, entry)
		}
	}
	// The unrestricted series page fixes the author destination identity the
	// restricted page must keep.
	free := phase34ListeningRequest("books", "book_series", saga)
	free.Sort = "order"
	freePage, err := s.Content(free)
	if err != nil {
		t.Fatal(err)
	}
	if sections := listeningSectionIDs(freePage); sections != "books,context" {
		t.Fatalf("unrestricted series sections %q: %+v", sections, freePage.Sections)
	}
	freeContext := listeningSectionByID(t, freePage, "context")
	if len(freeContext.Entries) != 1 || freeContext.Entries[0].Title != "Author" {
		t.Fatalf("unrestricted series author context: %+v", freeContext.Entries)
	}
	authorID := freeContext.Entries[0].ID
	// The author destination resolves the same identity on the real page.
	authorPage, err := s.Content(ContentRequest{Viewer: free.Viewer, ServerID: free.ServerID, Library: free.Library, Profile: free.Profile, ViewerFence: free.ViewerFence, View: "author", EntityID: authorID, Limit: 40})
	if err != nil || authorPage.Entity == nil || authorPage.Entity.Title != "Author" {
		t.Fatalf("author navigation id %q is not the author destination: %+v %v", authorID, authorPage.Entity, err)
	}
	r.View = "book_series"
	r.EntityID = saga
	r.Sort = "order"
	child, err := s.Content(r)
	if err != nil {
		t.Fatal(err)
	}
	if sections := listeningSectionIDs(child); sections != "books,context" {
		t.Fatalf("restricted series sections %q: %+v", sections, child.Sections)
	}
	if books := listeningSectionByID(t, child, "books"); books.TotalCount != 1 || len(books.Entries) != 1 || books.Entries[0].ID != names["book-a"].Public {
		t.Fatalf("restricted series books: %+v", books)
	}
	if context := listeningSectionByID(t, child, "context"); context.TotalCount != 1 || len(context.Entries) != 1 || context.Entries[0].ID != authorID || context.Entries[0].Title != "Author" {
		t.Fatalf("restricted series author context: %+v, want %q", context.Entries, authorID)
	}
	// The hidden series still has a visible book, but this author's membership
	// is through its hidden book alone: a visible book elsewhere must not leak
	// the name onto this page.
	// Unrestricted, the fixture names both authors, so the restricted page's
	// single author is the guard and not a missing membership.
	free.EntityID = otherSaga
	if all, err := s.Content(free); err != nil {
		t.Fatal(err)
	} else if context := listeningSectionByID(t, all, "context"); context.TotalCount != 2 || len(context.Entries) != 2 {
		t.Fatalf("hidden series unrestricted authors: %+v", context)
	}
	r.EntityID = otherSaga
	hiddenPage, err := s.Content(r)
	if err != nil {
		t.Fatal(err)
	}
	if sections := listeningSectionIDs(hiddenPage); sections != "books,context" {
		t.Fatalf("hidden series sections %q: %+v", sections, hiddenPage.Sections)
	}
	if books := listeningSectionByID(t, hiddenPage, "books"); books.TotalCount != 1 || len(books.Entries) != 1 || books.Entries[0].ID != visible.Public {
		t.Fatalf("hidden series books: %+v", books)
	}
	if context := listeningSectionByID(t, hiddenPage, "context"); context.TotalCount != 1 || len(context.Entries) != 1 || context.Entries[0].Title != "Other Author" || context.Entries[0].ID == authorID {
		t.Fatalf("hidden series author context: %+v", context.Entries)
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAttributesTx(ctx, tx, names["part-b1"].ID, "contentRating", []string{"PG"})
	})
	c.Drain()
	if more, err := s.ClassifyPendingRatings(context.Background()); err != nil || more {
		t.Fatal("pending ratings", more, err)
	}
	// A class one revision behind serves its last published generation.
	if _, err = s.Content(r); err != nil {
		t.Fatalf("a stale class failed the page: %v", err)
	}
	if err = s.RebuildVisibilityClass(context.Background(), "books", restriction); err != nil {
		t.Fatal(err)
	}
	c.Drain()
	r.View = "series"
	r.EntityID = ""
	r.Sort = ""
	page, err = s.Content(r)
	if err != nil {
		t.Fatal(err)
	}
	if sections := listeningSectionIDs(page); sections != "groups" {
		t.Fatalf("updated restricted series sections %q: %+v", sections, page.Sections)
	}
	groups = listeningSectionByID(t, page, "groups")
	if len(groups.Entries) != 2 {
		t.Fatalf("updated restricted series set: %+v", groups)
	}
	for _, want := range []struct {
		id, title string
		count     int
	}{{saga, "Saga", 2}, {otherSaga, "Other Saga", 1}} {
		entry := listeningEntryByID(t, groups, want.id)
		if entry.Title != want.title || entry.Count == nil || *entry.Count != want.count {
			t.Fatalf("updated restricted series %q: %+v", want.title, entry)
		}
	}
}

func listeningSectionByID(t *testing.T, page ContentEnvelope, id string) ContentSection {
	t.Helper()
	for _, section := range page.Sections {
		if section.ID == id {
			return section
		}
	}
	t.Fatalf("section %q absent: %+v", id, page.Sections)
	return ContentSection{}
}

func listeningEntryByID(t *testing.T, section ContentSection, id string) ContentEntry {
	t.Helper()
	for _, entry := range section.Entries {
		if entry.ID == id {
			return entry
		}
	}
	t.Fatalf("entry %q absent from section %q: %+v", id, section.ID, section.Entries)
	return ContentEntry{}
}

func listeningSectionIDs(page ContentEnvelope) string {
	ids := ""
	for index, section := range page.Sections {
		if index > 0 {
			ids += ","
		}
		ids += section.ID
	}
	return ids
}
