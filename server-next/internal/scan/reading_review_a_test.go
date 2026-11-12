package scan_test

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Review desk A, #2958.

// A book whose file changes and cannot be read at that moment (it is still being copied, or
// was cut short) keeps the details it had: a failed read says nothing about the book.
func TestReviewAABookWhoseChangedFileCannotBeReadKeepsItsDetails(t *testing.T) {
	f := newFixture(t, db.LibraryBooks)
	f.put("novel.epub", epub(t, "3.0", `<dc:title>The Real Title</dc:title><dc:creator>Ada Writer</dc:creator><dc:date>2021</dc:date>`))
	f.scan()
	before := f.titles(db.KindBook)["The Real Title"]
	if before.PublicID == "" {
		t.Fatalf("books: %v", keys(f.titles(db.KindBook)))
	}
	whole := epub(t, "3.0", `<dc:title>The Real Title</dc:title><dc:creator>Ada Writer</dc:creator><dc:date>2021</dc:date>`)
	f.put("novel.epub", whole[:len(whole)/2]) // half a copy: not an archive yet
	f.scan()
	books := f.titles(db.KindBook)
	after, ok := books["The Real Title"]
	if !ok || after.PublicID != before.PublicID {
		t.Fatalf("after a half-written copy the book is listed as %v, want its own title kept", keys(books))
	}
	if d := f.detail(after.PublicID); d.Year != 2021 || len(creditNames(d)) != 1 || creditNames(d)[0] != "Ada Writer" {
		t.Errorf("after a half-written copy: year %d, credits %v, want 2021 and Ada Writer kept", d.Year, creditNames(d))
	}
}
