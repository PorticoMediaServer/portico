package db_test

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Review desk A, #2715. A merge moves seasons and episodes under another show, and they keep
// the rating and labels they had under the show they came from. The show they land under must
// be flagged then: its titles now differ from it, and a profile that may see the show and
// none of them would otherwise meet an empty show. Both merges are tried: the owner's Merge,
// and the one a chosen match makes when another show of the library has that match already.
func TestReviewAMergeFlagsTheShowItsTitlesLandUnder(t *testing.T) {
	for _, how := range []struct {
		name  string
		merge func(d *db.DB, a, b string) error
	}{
		{"merge", func(d *db.DB, a, b string) error { return d.MergeItems(ctx, b, a) }},
		{"match", func(d *db.DB, a, b string) error {
			if err := db.MatchTitle(d, a, "tmdb:show:9001", false); err != nil {
				return err
			}
			return db.MatchTitle(d, b, "tmdb:show:9001", true)
		}},
	} {
		// Each merge starts from its own database, so neither can pass or fail on the other.
		l := newLibrary(t, 300)
		lib := showLibrary(t, l)
		limit := 13
		limited, err := l.d.CreateSeedProfile(ctx, db.SeedProfile{Name: "review-a-13", MaxRatingAge: &limit, AllowUnrated: true})
		if err != nil {
			t.Fatal(err)
		}
		kids, adult, one, two := 0, 17, 1, 2
		showA, showB := db.ItemKey{Kind: db.KeyFolder, Key: how.name + "-a"}, db.ItemKey{Kind: db.KeyFolder, Key: how.name + "-b"}
		seasonB := db.ItemKey{Kind: db.KeyName, Key: how.name + "-b/2"}
		if _, err := l.d.Import(ctx, db.ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: 1, Items: []db.ImportItem{
			// A show the scanner made with no file of its own yet, rated for children.
			{Kind: db.KindShow, Keys: []db.ItemKey{showA}, Title: how.name + " A", ContentRating: "G", RatingAge: &kids},
			// The same show in another folder, rated for adults, with the only episode.
			{Kind: db.KindShow, Keys: []db.ItemKey{showB}, Title: how.name + " B", ContentRating: "R", RatingAge: &adult},
			{Kind: db.KindSeason, Keys: []db.ItemKey{seasonB}, Parent: &showB, Title: "Season 2", IndexNumber: &two},
			{Kind: db.KindEpisode, Keys: []db.ItemKey{{Kind: db.KeyName, Key: how.name + "-b/2/1"}}, Parent: &seasonB, Title: "Pilot", IndexNumber: &one},
		}}); err != nil {
			t.Fatal(err)
		}
		if missing, _, _, err := db.HolderFlags(l.d); err != nil || len(missing) > 0 {
			t.Fatalf("before the merge: %v %v", err, missing)
		}
		var a, b string
		for _, show := range everything(t, l.d, l.all, db.ItemQuery{Library: lib.PublicID, Kinds: []db.ItemKind{db.KindShow}, Limit: 200}) {
			switch show.Title {
			case how.name + " A":
				a = show.PublicID
			case how.name + " B":
				b = show.PublicID
			}
		}
		if a == "" || b == "" {
			t.Fatal("the two shows were not imported")
		}
		if err := how.merge(l.d, a, b); err != nil {
			t.Fatal(err)
		}
		if missing, _, _, err := db.HolderFlags(l.d); err != nil || len(missing) > 0 {
			t.Errorf("%s: after merging a show rated for adults into one rated for children: %v %v", how.name, err, missing)
		}
		// What that costs: a profile limited to 13 is listed a show with nothing in it.
		for _, show := range everything(t, l.d, limited, db.ItemQuery{Library: lib.PublicID, Kinds: []db.ItemKind{db.KindShow}}) {
			if show.PublicID == a {
				t.Errorf("%s: the profile limited to 13 is listed the merged show, whose only episode is rated 17", how.name)
			}
		}
	}
}

func showLibrary(t *testing.T, l *library) db.Library {
	t.Helper()
	libraries, err := l.d.AllLibraries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, lib := range libraries {
		if lib.Kind == db.LibraryShows {
			return lib
		}
	}
	t.Fatal("no show library")
	return db.Library{}
}
