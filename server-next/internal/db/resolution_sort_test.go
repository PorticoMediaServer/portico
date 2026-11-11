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
	// A film has two versions and the wider one's file goes: the title is as wide as the
	// version that is left, and no wider.
	gone := false
	for _, film := range everything(t, l.d, l.all, db.ItemQuery{Library: l.libs[0].PublicID, Kinds: []db.ItemKind{db.KindMovie}, Limit: 200}) {
		detail, err := l.d.Item(ctx, l.all, film.PublicID)
		if err != nil {
			t.Fatal(err)
		}
		if len(detail.Versions) != 2 || detail.Versions[0].Width == detail.Versions[1].Width {
			continue
		}
		gone = true
		wide, narrow := detail.Versions[0], detail.Versions[1]
		if narrow.Width > wide.Width {
			wide, narrow = narrow, wide
		}
		width, err := db.VersionGone(l.d, wide.PublicID)
		if err != nil {
			t.Fatal(err)
		}
		if width != narrow.Width {
			t.Fatalf("with its %d-wide version gone, %q is stored as %d wide; the version left is %d wide", wide.Width, film.Title, width, narrow.Width)
		}
		check("after a film's wider version went missing")
		break
	}
	if !gone {
		t.Fatal("the test library has no film with two versions of different widths")
	}
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
