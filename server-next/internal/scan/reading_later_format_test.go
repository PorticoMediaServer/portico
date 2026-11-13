package scan_test

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

func TestALaterPDFJoinsTheExistingEPUB(t *testing.T) {
	f := newFixture(t, db.LibraryBooks)
	f.put("Harbour Lamps.epub", epub(t, "3.0", `<dc:title>Harbour Lamps</dc:title><dc:creator>Mira Quinn</dc:creator>`))
	f.scan()
	original := f.titles(db.KindBook)["Harbour Lamps"]
	if original.PublicID == "" {
		t.Fatal("the EPUB was not added")
	}
	f.text("Harbour Lamps.pdf", "%PDF-1.4\nLater format of Harbour Lamps.\n")
	if added := f.scan(); added.ItemsNew != 0 {
		t.Fatalf("the later PDF created %d new titles instead of joining the EPUB", added.ItemsNew)
	}
	page, err := f.d.Items(ctx, f.viewer, db.ItemQuery{Library: f.lib.PublicID, Kinds: []db.ItemKind{db.KindBook}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].PublicID != original.PublicID {
		t.Fatalf("the two formats are not the original book: %+v", page.Items)
	}
	book := f.detail(original.PublicID)
	if len(book.Versions) != 2 || len(book.Credits) != 1 || book.Credits[0].Name != "Mira Quinn" {
		t.Fatalf("the book lost a format or its EPUB details: versions %+v, credits %+v", book.Versions, book.Credits)
	}
}
