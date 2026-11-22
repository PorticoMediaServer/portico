package scan_test

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Two recordings of one show on one day (named as the recorder names them, with the date and
// time) are two titles, not one title with two versions (live TV rule 47); a rescan keeps them.
func TestTwoRecordingsOfOneDayAreTwoTitles(t *testing.T) {
	f := newFixture(t, db.LibraryShows)
	f.write("World Tonight/World Tonight - 2026-11-21 18.00 Northwind News 1.ts")
	f.write("World Tonight/World Tonight - 2026-11-21 23.30 Northwind News 1.ts")
	f.scan()
	page, err := f.d.Items(ctx, f.viewer, db.ItemQuery{Library: f.lib.PublicID, Kinds: []db.ItemKind{db.KindEpisode}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	episodes := len(page.Items)
	for _, e := range page.Items {
		if versions := len(f.detail(e.PublicID).Versions); versions != 1 {
			t.Fatalf("%s has %d versions", e.Title, versions)
		}
	}
	if episodes != 2 {
		t.Fatalf("two recordings of 21 Nov are %d titles", episodes)
	}
	if again := f.scan(); again.RowsWritten != 0 {
		t.Fatalf("an unchanged rescan wrote %d rows", again.RowsWritten)
	}
}
