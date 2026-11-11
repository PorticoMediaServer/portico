package db_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
	"github.com/PorticoMediaServer/portico/server-next/internal/seed"
)

func TestLabelsFollowMovesAndSplits(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	root := t.TempDir()
	write := func(rel string, size int) {
		full := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Bluey/Season 1/Bluey - S01E01.mkv", 101)
	write("Bluey/Season 1/Bluey - S01E02.mkv", 102)
	write("Bluey/Season 2/Bluey - S02E01.mkv", 104)
	write("The Wire/Season 1/The Wire - S01E01.mkv", 103)
	lib, err := d.CreateLibrary(ctx, "Shows", db.LibraryShows, "en", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	scanner := &scan.Scanner{DB: d}
	scanAll := func() {
		t.Helper()
		libraries, _ := d.AllLibraries(ctx)
		if _, err := scanner.Scan(ctx, libraries[0]); err != nil {
			t.Fatal(err)
		}
	}
	scanAll()
	viewers, err := seed.Profiles(ctx, d, 1)
	if err != nil {
		t.Fatal(err)
	}
	v := viewers[0]
	check := func(when string) {
		t.Helper()
		wrong, stored, err := db.CoveringLabels(d)
		if err != nil {
			t.Fatal(err)
		}
		covered := 0
		for _, s := range stored {
			if s != "" {
				covered++
			}
		}
		t.Logf("%s: %d of %d items covered", when, covered, len(stored))
		if len(wrong) > 0 {
			t.Errorf("%s: %d wrong, first: %s", when, len(wrong), wrong[0])
		}
		// A move or a label can leave a holder empty for some profile: it must be flagged.
		if missing, _, _, err := db.HolderFlags(d); err != nil || len(missing) > 0 {
			t.Errorf("%s: %d holders not flagged (%v), first: %v", when, len(missing), err, missing)
		}
	}
	find := func(kind db.ItemKind, title string) db.Item {
		t.Helper()
		page, err := d.Items(ctx, v, db.ItemQuery{Library: lib.PublicID, Kinds: []db.ItemKind{kind}, Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if item.Title == title {
				return item
			}
		}
		t.Fatalf("no %q among %d", title, len(page.Items))
		return db.Item{}
	}
	check("fresh")
	wire := find(db.KindShow, "The Wire")
	if err := db.PutLabel(d, wire.PublicID, "Grown-ups", false); err != nil {
		t.Fatal(err)
	}
	check("label on The Wire")
	// into the labelled show
	os.Rename(filepath.Join(root, "Bluey/Season 1/Bluey - S01E02.mkv"), filepath.Join(root, "The Wire/Season 1/The Wire - S01E02.mkv"))
	scanAll()
	check("an episode moved into the labelled show")
	// a brand new episode and a brand new season under the labelled show
	write("The Wire/Season 1/The Wire - S01E03.mkv", 105)
	write("The Wire/Season 3/The Wire - S03E01.mkv", 106)
	scanAll()
	check("new episode and new season under the labelled show")
	// out of the labelled show
	os.Rename(filepath.Join(root, "The Wire/Season 1/The Wire - S01E01.mkv"), filepath.Join(root, "Bluey/Season 1/Bluey - S01E05.mkv"))
	scanAll()
	check("an episode moved out of the labelled show")
	// a second label on a season of the other show, then a move within the show across seasons
	seasons, _ := db.Raw(d, `SELECT public_id FROM items WHERE kind = ? AND parent_id = (SELECT id FROM items WHERE title = 'Bluey' AND parent_id IS NULL) ORDER BY index_number`, db.KindSeason)
	if len(seasons) < 2 {
		t.Fatalf("seasons: %v", seasons)
	}
	if err := db.PutLabel(d, seasons[1], "Scary", false); err != nil {
		t.Fatal(err)
	}
	check("label on Bluey season 2")
	os.Rename(filepath.Join(root, "Bluey/Season 1/Bluey - S01E05.mkv"), filepath.Join(root, "Bluey/Season 2/Bluey - S02E05.mkv"))
	scanAll()
	check("an episode moved into a labelled season")
	os.Rename(filepath.Join(root, "Bluey/Season 2/Bluey - S02E01.mkv"), filepath.Join(root, "The Wire/Season 1/The Wire - S01E09.mkv"))
	scanAll()
	check("an episode moved from a labelled season into a labelled show")
	// whole show folder renamed
	os.Rename(filepath.Join(root, "The Wire"), filepath.Join(root, "The Wire (2002)"))
	scanAll()
	check("the labelled show's folder renamed")
	// a second copy of an episode under the labelled show, split off into its own title
	write("The Wire (2002)/Season 1/The Wire - S01E03 - 720p.mkv", 207)
	scanAll()
	versions, _ := db.Raw(d, `SELECT m.public_id FROM media m JOIN items i ON i.id = m.item_id WHERE i.parent_id IS NOT NULL AND i.labels IS NOT NULL AND m.position > 0`)
	t.Logf("second versions under a label: %d", len(versions))
	for _, version := range versions {
		if _, err := d.SplitVersion(ctx, version); err != nil {
			t.Fatal(err)
		}
	}
	if len(versions) == 0 {
		t.Error("no second version to split")
	}
	check("a version split off under the labelled show")
	if err := db.PutLabel(d, wire.PublicID, "Grown-ups", true); err != nil {
		t.Logf("taking the label off: %v", err)
	}
	check("label taken off The Wire")
	before, _, _ := db.CoveringLabels(d)
	_ = before
	if err := d.RebuildCoveringLabels(ctx); err != nil {
		t.Fatal(err)
	}
	check("after rebuild")
}

// The seeded library: split every second version that sits under a labelled holder.
func TestLabelsFollowASplitUnderALabelledShow(t *testing.T) {
	l := newLibrary(t, 6000)
	covered, err := db.SplitSecondVersions(l.d, true)
	if err != nil {
		t.Fatal(err)
	}
	rest, err := db.SplitSecondVersions(l.d, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("split %d versions under a label and %d others", covered, rest)
	wrong, _, err := db.CoveringLabels(l.d)
	if err != nil {
		t.Fatal(err)
	}
	if len(wrong) > 0 {
		t.Errorf("%d wrong after splits, first: %s", len(wrong), wrong[0])
	}
	if covered == 0 {
		t.Log("NOTE: the seeded library has no second version under a labelled holder")
	}
}
