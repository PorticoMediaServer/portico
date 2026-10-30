package httpapi

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/reading"
)

// A moved file keeps its inventory object and asset identity, so its Reading
// publication follows it. The old folder loses a title and the new folder gains
// one: both attributions must be republished, and the old folder's series must
// be withdrawn from the automatic baseline.
func TestReadingPrivatePathMovedFolderWithdrawsOldAttribution(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	r := newReadingFixture(t, "book", "pdf")
	ctx := context.Background()
	one := readingTreeFile(t, r, "Author/Series/One.epub")
	two := readingTreeFile(t, r, "Author/Series/Two.epub")
	standalone := readingTreeFile(t, r, "Author/Standalone.epub")
	readingScan(t, r.f, r.library)
	oneID := readingPathItem(t, r, one)
	twoID := readingPathItem(t, r, two)
	standaloneID := readingPathItem(t, r, standalone)
	expect := func(id, author, series string) {
		t.Helper()
		list, err := r.s.List(ctx, r.p, r.library, "", 100)
		if err != nil {
			t.Fatal(err)
		}
		var row reading.LibraryRow
		found := false
		for _, v := range list.Items {
			if v.ID == id {
				row, found = v, true
			}
		}
		if !found {
			t.Fatalf("title %s vanished from the authorized page", id)
		}
		if author == "" {
			if row.Author != nil {
				t.Fatalf("%s invented author %+v", id, row.Author)
			}
		} else if row.Author == nil || row.Author.Value != author || row.Author.Source != compactcatalog.ReadingFolder {
			t.Fatalf("%s author=%+v want %q/folder", id, row.Author, author)
		}
		if series == "" {
			if row.Series != nil {
				t.Fatalf("%s kept series %+v", id, row.Series)
			}
		} else if row.Series == nil || row.Series.Name != series || row.Series.Source != compactcatalog.ReadingFolder {
			t.Fatalf("%s series=%+v want %q/folder", id, row.Series, series)
		}
	}
	expect(oneID, "Author", "Series")
	expect(twoID, "Author", "Series")
	expect(standaloneID, "Author", "")
	// The destination folder sorts after the source folder, so the source page
	// is refreshed before the move is observed at its destination.
	later := filepath.Join(filepath.Dir(r.path), "Author", "Zeta", "Two.epub")
	if err := os.MkdirAll(filepath.Dir(later), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(two, later); err != nil {
		t.Fatal(err)
	}
	readingScan(t, r.f, r.library)
	if moved := readingPathItem(t, r, later); moved != twoID {
		t.Fatalf("reparent changed the title identity: %s want %s", moved, twoID)
	}
	expect(oneID, "Series", "")
	expect(twoID, "Zeta", "")
	expect(standaloneID, "Author", "")
	// Moving it back restores the two-title series interpretation.
	if err := os.Rename(later, two); err != nil {
		t.Fatal(err)
	}
	readingScan(t, r.f, r.library)
	expect(oneID, "Author", "Series")
	expect(twoID, "Author", "Series")
}
