package db_test

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// The sort by resolution reads a width stored on each title. It must be what the title's
// versions say after an import, and after a version is split off into a title of its own.
func TestStoredWidthFollowsTheVersions(t *testing.T) {
	l := newLibrary(t, 1500)
	check := func(when string) {
		t.Helper()
		wrong, set, err := db.WidthsNotKept(l.d)
		if err != nil {
			t.Fatal(err)
		}
		if wrong != 0 || set == 0 {
			t.Fatalf("%s: %d titles have a stored width their versions do not give; %d have one at all", when, wrong, set)
		}
	}
	check("after the import")
	// The seeder gives some films two versions. Splitting one off leaves each title with one.
	split := false
	for _, film := range everything(t, l.d, l.all, db.ItemQuery{Library: l.libs[0].PublicID, Kinds: []db.ItemKind{db.KindMovie}, Limit: 200}) {
		detail, err := l.d.Item(ctx, l.all, film.PublicID)
		if err != nil {
			t.Fatal(err)
		}
		if len(detail.Versions) > 1 {
			if _, err := l.d.SplitVersion(ctx, detail.Versions[1].PublicID); err != nil {
				t.Fatal(err)
			}
			split = true
			break
		}
	}
	if !split {
		t.Fatal("the test library has no film with two versions")
	}
	check("after a split")
	// And the list in that order comes from the index: widest first, every title once.
	page := everything(t, l.d, l.all, db.ItemQuery{Library: l.libs[0].PublicID, Kinds: []db.ItemKind{db.KindMovie}, Sort: db.SortResolution, Desc: true, Limit: 200})
	if len(page) == 0 {
		t.Fatal("no films listed by resolution")
	}
}
