package httpapi

import (
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"testing"
)

func TestRetiredBrowseReadsUseCanonicalBookSection(t *testing.T) {
	d, session := logoutHTTPFixture(t)
	c := catalogtest.New(t, d.DB)
	lib := c.Library("books", "Books", "audiobook", "/books")
	book := c.Book(lib, "Book", "Author")
	one := c.BookFile(book, 1, "/books/one.m4b")
	two := c.BookFile(book, 2, "/books/two.m4b")
	c.Drain()
	h := New(d)
	for _, path := range []string{"/v1/libraries/books/artists", "/v1/libraries/books/books", "/v1/libraries/books/shows", "/v1/books/" + book.Public + "/files"} {
		w := editorRequest(t, h, "GET", path, session.AccessToken, "", nil)
		if w.Code != 404 {
			t.Fatalf("retired %s: %d %s", path, w.Code, w.Body)
		}
	}
	path := "/v1/libraries/books/content?view=book&entityId=" + book.Public + "&section=files&limit=1"
	ids := []string{}
	cursor := ""
	for i := 0; i < 2; i++ {
		request := path
		if cursor != "" {
			request += "&cursor=" + cursor
		}
		w := editorRequest(t, h, "GET", request, session.AccessToken, "", nil)
		var body catalog.ContentEnvelope
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Scope.EntityID != book.Public || body.Scope.LibraryID != "books" {
			t.Fatalf("content: %d %s", w.Code, w.Body)
		}
		if len(body.Sections) != 1 || body.Sections[0].ID != "files" || body.Sections[0].TotalCount != 2 {
			t.Fatalf("sections: %s", w.Body)
		}
		for _, entry := range body.Sections[0].Entries {
			ids = append(ids, entry.ID)
		}
		cursor = body.Sections[0].NextCursor
	}
	if len(ids) != 2 || ids[0] != one.Public || ids[1] != two.Public || cursor != "" {
		t.Fatalf("lost book hierarchy: %v, cursor %q", ids, cursor)
	}
}
