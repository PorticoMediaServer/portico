package api

import (
	"context"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

func (b *bookShelf) indexText() {
	b.Helper()
	files, err := b.s.db.BookTextFiles(context.Background())
	if err != nil {
		b.Fatal(err)
	}
	if err := b.s.bookText.Load().Update(context.Background(), files); err != nil {
		b.Fatal(err)
	}
}

func (b *bookShelf) searchBooks(token, q, more string) BookSearchPage {
	b.Helper()
	return get[BookSearchPage](b.T, b.s, token, "/v1/reading/search?q="+url.QueryEscape(q)+more)
}

// The text a hit points at, as the reader checks it: the match in the excerpt.
func quoted(h BookSearchHit) string {
	units := utf16.Encode([]rune(h.Excerpt))
	return string(utf16.Decode(units[h.MatchStart : h.MatchStart+h.MatchLength]))
}

// Search all books finds a line in any book the profile may see, with the place the reader
// opens it at: the version and fingerprint GET /v1/items/{id}/book gives, the section, and
// the offset in that section's text. A MOBI is searched as the EPUB the reader rebuilds it into.
func TestSearchAllBooksFindsALineWhereTheReaderOpensIt(t *testing.T) {
	b := newBookShelf(t)
	b.indexText()
	owner := b.owner.AccessToken
	page := b.searchBooks(owner, "BRIGHT cold", "")
	if !page.IndexedOnly || page.IndexRevision == 0 || page.Anchor != bookSearchAnchor || len(page.Hits) != 1 || page.Next != "" {
		t.Fatalf("page %+v", page)
	}
	h := page.Hits[0]
	nineteen := b.find(db.KindBook, "Nineteen")
	opened := get[BookAnswer](t, b.s, owner, "/v1/items/"+nineteen+"/book?version="+h.SourceID)
	if h.ItemID != nineteen || h.Title != "Nineteen" || h.SourceRevision != opened.Fingerprint || opened.Format != "epub" ||
		h.SectionPath != "OEBPS/text/chapter 1.xhtml" || h.SectionIndex != 0 || h.UTF16Offset != 9 || quoted(h) != "bright cold" {
		t.Fatalf("hit %+v (book %s, fingerprint %s)", h, nineteen, opened.Fingerprint)
	}
	if page := b.searchBooks(owner, "cafe by the river", ""); len(page.Hits) != 1 || quoted(page.Hits[0]) != "Café by the river" {
		t.Fatalf("the MOBI's line: %+v", page)
	}
	if page := b.searchBooks(b.limited, "bright cold", ""); len(page.Hits) != 0 || page.Next != "" {
		t.Fatalf("a profile that may see no book found %+v, next %q", page.Hits, page.Next)
	}
	if page := b.searchBooks(owner, "\u0301", ""); len(page.Hits) != 0 {
		t.Fatalf("a search of a lone accent found %+v", page.Hits)
	}
	if page := b.searchBooks(owner, "bright cold", "&libraryId=other"); len(page.Hits) != 0 {
		t.Fatalf("another library found %+v", page.Hits)
	}
	if w := call(t, b.s, "GET", "/v1/reading/search?q="+url.QueryEscape("?!"), owner, nil, nil); w.Code != 400 {
		t.Fatalf("a search with no words answered %d", w.Code)
	}
}

// Hits come a page at a time, each once; a book whose file changed is left out until it is
// read again, so no hit points into text the file no longer has.
func TestSearchAllBooksPagesAndFollowsChangedBooks(t *testing.T) {
	b := newBookShelf(t)
	epubFile(t, filepath.Join(b.books, "Cold.epub"), `<html xmlns="http://www.w3.org/1999/xhtml"><body><p>Cold. Cold, cold!</p></body></html>`)
	b.scan()
	b.indexText()
	owner := b.owner.AccessToken
	seen, cursor := map[[2]any]bool{}, ""
	for pages := 0; ; pages++ {
		page := b.searchBooks(owner, "cold", "&limit=2"+cursor)
		if len(page.Hits) > 2 || pages > 3 {
			t.Fatalf("page %d: %+v", pages, page)
		}
		for _, h := range page.Hits {
			at := [2]any{h.ItemID, h.UTF16Offset}
			if seen[at] || !strings.EqualFold(quoted(h), "cold") {
				t.Fatalf("hit %+v again or wrong", h)
			}
			seen[at] = true
		}
		if page.Next == "" {
			break
		}
		cursor = "&cursor=" + page.Next
	}
	if len(seen) != 4 {
		t.Fatalf("%d hits, want 4: %v", len(seen), seen)
	}

	epubFile(t, filepath.Join(b.books, "Orwell", "Nineteen.epub"), `<html xmlns="http://www.w3.org/1999/xhtml"><body><p>A warm and longer day.</p></body></html>`)
	b.scan()
	if page := b.searchBooks(owner, "bright", ""); len(page.Hits) != 0 {
		t.Fatalf("a changed book's old text was found: %+v", page.Hits)
	}
	before := b.searchBooks(owner, "warm", "").IndexRevision
	b.indexText()
	if page := b.searchBooks(owner, "warm", ""); len(page.Hits) != 1 || page.IndexRevision == before {
		t.Fatalf("the changed book read again: %+v", page)
	}
}
