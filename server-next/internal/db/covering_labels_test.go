package db_test

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/seed"
)

// Every title stores the labels that cover it: its own, its season's or album's and its show's
// or artist's. After an import, after more of the library arrives, and after a version is
// split off, the stored value must be exactly those, and working every row out again from
// nothing must change none. What a profile with a blocked label may see is held to the rule,
// title by title, by TestRestrictedProfileNeverReceivesARestrictedTitle.
func TestStoredLabelsAreTheTitlesOwnAndItsAncestors(t *testing.T) {
	l := newLibrary(t, 1500)
	check := func(when string) map[int64]string {
		t.Helper()
		wrong, stored, err := db.CoveringLabels(l.d)
		if err != nil {
			t.Fatal(err)
		}
		if len(wrong) > 0 {
			t.Fatalf("%s: %d titles are wrong, the first: %s", when, len(wrong), wrong[0])
		}
		return stored
	}
	covered, inherited := 0, 0
	for _, labels := range check("after the import") {
		if labels != "" {
			covered++
		}
	}
	// An episode or a track has no label of its own in the seeded library: one that is covered
	// is covered by its show, its season, its artist or its album.
	for _, lib := range l.libs {
		for _, kind := range []db.ItemKind{db.KindEpisode, db.KindTrack} {
			visible := len(everything(t, l.d, l.viewers[7], db.ItemQuery{Library: lib.PublicID, Kinds: []db.ItemKind{kind}, Limit: 200}))
			all := len(everything(t, l.d, l.all, db.ItemQuery{Library: lib.PublicID, Kinds: []db.ItemKind{kind}, Limit: 200}))
			inherited += all - visible
		}
	}
	if covered == 0 || inherited == 0 {
		t.Fatalf("%d titles are covered by a label and %d episodes or tracks are hidden by one above them; the test needs both", covered, inherited)
	}
	if _, err := seed.Import(ctx, l.d, seed.Config{Seed: 7, Items: 2400}, nil); err != nil {
		t.Fatal(err)
	}
	check("after more of the library arrived")
	for _, film := range everything(t, l.d, l.all, db.ItemQuery{Library: l.libs[0].PublicID, Kinds: []db.ItemKind{db.KindMovie}, Limit: 200}) {
		detail, err := l.d.Item(ctx, l.all, film.PublicID)
		if err != nil {
			t.Fatal(err)
		}
		if len(detail.Versions) > 1 {
			if _, err := l.d.SplitVersion(ctx, detail.Versions[1].PublicID); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	before := check("after a version was split off")
	if err := l.d.RebuildCoveringLabels(ctx); err != nil {
		t.Fatal(err)
	}
	after := check("after every row was worked out again")
	for id, labels := range before {
		if after[id] != labels {
			t.Fatalf("working the labels out again changed item %d from %q to %q", id, labels, after[id])
		}
	}
}
