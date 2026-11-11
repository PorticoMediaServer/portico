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

// Two shows are merged. What moves under the show that remains takes that show's rating and
// its labels, so a profile with a rating limit or a blocked label neither gains nor loses an
// episode it should not.
func TestAMergeKeepsRatingLimitsAndBlockedLabels(t *testing.T) {
	for _, c := range []struct {
		name, from, into string
		limited          int // episodes the profile limited to 13 lists afterwards
	}{
		// Everything is now part of the show for children: the limited profile sees all three.
		{"the grown-up show into the young one", "The Wire", "Bluey", 3},
		// Everything is now part of the show for adults: it sees none.
		{"the young show into the grown-up one", "Bluey", "The Wire", 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			root := t.TempDir()
			for n, rel := range []string{"Bluey/Season 1/Bluey - S01E01.mkv", "The Wire/Season 1/The Wire - S01E02.mkv", "The Wire/Season 2/The Wire - S02E01.mkv"} {
				full := filepath.Join(root, rel)
				if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(full, make([]byte, 101+n), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			lib, err := d.CreateLibrary(ctx, "Shows", db.LibraryShows, "en", []string{root})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := (&scan.Scanner{DB: d}).Scan(ctx, lib); err != nil {
				t.Fatal(err)
			}
			// Two profiles: the second is limited to 13.
			viewers, err := seed.Profiles(ctx, d, 2)
			if err != nil {
				t.Fatal(err)
			}
			everyone, limited := viewers[0], viewers[1]
			shows, err := d.Items(ctx, everyone, db.ItemQuery{Library: lib.PublicID, Kinds: []db.ItemKind{db.KindShow}, Limit: 50})
			if err != nil || len(shows.Items) != 2 {
				t.Fatalf("shows: %+v, %v", shows.Items, err)
			}
			ids := map[string]string{}
			for _, show := range shows.Items {
				ids[show.Title] = show.PublicID
				if err := db.SetRatingAge(d, show.PublicID, map[string]int{"Bluey": 8, "The Wire": 18}[show.Title]); err != nil {
					t.Fatal(err)
				}
			}
			// The show that remains carries a label one profile is kept from.
			if err := db.PutLabel(d, ids[c.into], "Not tonight", false); err != nil {
				t.Fatal(err)
			}
			labels, err := d.Tags(ctx, db.TagLabel)
			if err != nil || len(labels) != 1 {
				t.Fatalf("labels: %v, %v", labels, err)
			}
			blocked, err := d.CreateSeedProfile(ctx, db.SeedProfile{Name: "blocked", BlockedTags: []string{labels[0].PublicID}})
			if err != nil {
				t.Fatal(err)
			}
			episodes := func(v db.Viewer) int {
				t.Helper()
				page, err := d.Items(ctx, v, db.ItemQuery{Library: lib.PublicID, Kinds: []db.ItemKind{db.KindEpisode}, Limit: 50})
				if err != nil {
					t.Fatal(err)
				}
				return len(page.Items)
			}
			if episodes(everyone) != 3 || episodes(limited) != 1 {
				t.Fatalf("before the merge: %d episodes for everyone, %d for the limited profile; want 3 and 1", episodes(everyone), episodes(limited))
			}

			if err := d.MergeItems(ctx, ids[c.from], ids[c.into]); err != nil {
				t.Fatal(err)
			}
			if got := episodes(everyone); got != 3 {
				t.Fatalf("after the merge everyone lists %d episodes; want 3", got)
			}
			if got := episodes(limited); got != c.limited {
				t.Errorf("after the merge the profile limited to 13 lists %d episodes; want %d", got, c.limited)
			}
			if got := episodes(blocked); got != 0 {
				t.Errorf("after the merge the profile kept from the show's label lists %d of its episodes; want none", got)
			}
			wrong, _, err := db.CoveringLabels(d)
			if err != nil {
				t.Fatal(err)
			}
			if len(wrong) > 0 {
				t.Errorf("after the merge %d titles carry the wrong labels, the first: %s", len(wrong), wrong[0])
			}
			if err := d.Check(ctx); err != nil {
				t.Error(err)
			}
		})
	}
}
