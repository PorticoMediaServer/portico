package db_test

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A provider's answer can change a title's rating long after the title was made. What a
// profile with a rating limit is offered must follow in the same write: after ApplyDetails
// makes half the shows it could see adults only, each genre's count is still the size of
// that genre's list, for shows and for their episodes, and the shows are gone from them.
func TestGenreChoicesFollowARatingChangedByAProvider(t *testing.T) {
	l := newLibrary(t, 1500)
	age := 13
	limited, err := l.d.CreateSeedProfile(ctx, db.SeedProfile{Name: "limited-by-rating", MaxRatingAge: &age})
	if err != nil {
		t.Fatal(err)
	}
	var shows db.Library
	for _, lib := range l.libs {
		if lib.Kind == db.LibraryShows {
			shows = lib
		}
	}
	agree := func(kind db.ItemKind) (sum int) {
		t.Helper()
		q := db.ItemQuery{Library: shows.PublicID, Kinds: []db.ItemKind{kind}}
		choices, err := l.d.Facets(ctx, limited, q, db.FacetGenre)
		if err != nil {
			t.Fatal(err)
		}
		for _, choice := range choices {
			q.Tag, q.WithTotal = choice.Value, true
			page, err := l.d.Items(ctx, limited, q)
			if err != nil {
				t.Fatal(err)
			}
			listed := len(everything(t, l.d, limited, db.ItemQuery{Library: shows.PublicID, Kinds: []db.ItemKind{kind}, Tag: choice.Value, Limit: 200}))
			if listed != choice.Count || page.Total != choice.Count {
				t.Fatalf("%s, genre %q: the choice says %d, the total %d, the list holds %d", kind, choice.Label, choice.Count, page.Total, listed)
			}
			sum += choice.Count
		}
		return sum
	}
	before := agree(db.KindShow) + agree(db.KindEpisode)
	visible := everything(t, l.d, limited, db.ItemQuery{Library: shows.PublicID, Kinds: []db.ItemKind{db.KindShow}, Limit: 200})
	if len(visible) < 2 {
		t.Fatalf("the test library shows the limited profile %d shows", len(visible))
	}
	adult := 18
	for _, show := range visible[:len(visible)/2] {
		// The answer names no genre, so the show keeps the tag rows it had: they are right
		// afterwards only if the write told them of the new rating.
		details := db.ItemDetails{ContentRating: "NC-17", RatingAge: &adult}
		if err := l.d.ApplyDetails(ctx, db.Background, show.ID, details); err != nil {
			t.Fatal(err)
		}
	}
	after := agree(db.KindShow) + agree(db.KindEpisode)
	if after >= before {
		t.Fatalf("genre counts were %d before half the shows became adults only and %d after", before, after)
	}
	still := everything(t, l.d, limited, db.ItemQuery{Library: shows.PublicID, Kinds: []db.ItemKind{db.KindShow}, Limit: 200})
	if len(still) != len(visible)-len(visible)/2 {
		t.Fatalf("the limited profile sees %d shows; want %d", len(still), len(visible)-len(visible)/2)
	}
	if err := l.d.Check(ctx); err != nil {
		t.Fatal(err)
	}
}

// The same for a rating a person types in the editor: the genre choices of a profile with a
// rating limit follow it in the same write.
func TestGenreChoicesFollowARatingChangedByHand(t *testing.T) {
	l := newLibrary(t, 1500)
	age := 13
	limited, err := l.d.CreateSeedProfile(ctx, db.SeedProfile{Name: "limited-by-rating", MaxRatingAge: &age})
	if err != nil {
		t.Fatal(err)
	}
	var shows db.Library
	for _, lib := range l.libs {
		if lib.Kind == db.LibraryShows {
			shows = lib
		}
	}
	q := db.ItemQuery{Library: shows.PublicID, Kinds: []db.ItemKind{db.KindShow}}
	visible := everything(t, l.d, limited, db.ItemQuery{Library: shows.PublicID, Kinds: []db.ItemKind{db.KindShow}, Limit: 200})
	if len(visible) < 2 {
		t.Fatalf("the test library shows the limited profile %d shows", len(visible))
	}
	rating := "NC-17"
	for _, show := range visible[:len(visible)/2] {
		locks, err := l.d.MetadataLocks(ctx, show.PublicID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := l.d.EditMetadata(ctx, show.PublicID, locks.Revision, db.MetadataEdit{ContentRating: &rating}); err != nil {
			t.Fatal(err)
		}
	}
	choices, err := l.d.Facets(ctx, limited, q, db.FacetGenre)
	if err != nil {
		t.Fatal(err)
	}
	for _, choice := range choices {
		listed := len(everything(t, l.d, limited, db.ItemQuery{Library: shows.PublicID, Kinds: []db.ItemKind{db.KindShow}, Tag: choice.Value, Limit: 200}))
		if listed != choice.Count {
			t.Fatalf("genre %q: the choice says %d, the list holds %d", choice.Label, choice.Count, listed)
		}
	}
}

// An episode put back by Undo follows its show's rating as it is now: Undo of an episode's
// edit made before its show became adults only must not hand a rating-limited profile the
// episode.
func TestUndoOnAnEpisodeKeepsItsShowsRatingNow(t *testing.T) {
	l := newLibrary(t, 1500)
	age := 13
	limited, err := l.d.CreateSeedProfile(ctx, db.SeedProfile{Name: "limited-by-rating", MaxRatingAge: &age})
	if err != nil {
		t.Fatal(err)
	}
	var ep db.Item
	for _, e := range everything(t, l.d, limited, db.ItemQuery{Library: showsLibrary(l).PublicID, Kinds: []db.ItemKind{db.KindEpisode}, Limit: 200}) {
		if e.ContentRating == "" {
			ep = e
			break
		}
	}
	if ep.ID == 0 {
		t.Fatal("the test library has no episode the limited profile sees without a rating of its own")
	}
	title, rating := "Renamed", "NC-17"
	edit := func(id string, e db.MetadataEdit) {
		t.Helper()
		locks, err := l.d.MetadataLocks(ctx, id)
		if err == nil {
			_, err = l.d.EditMetadata(ctx, id, locks.Revision, e)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	edit(ep.PublicID, db.MetadataEdit{Title: &title})
	edit(ep.RootPublicID, db.MetadataEdit{ContentRating: &rating})
	history, err := l.d.MetadataHistory(ctx, ep.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.d.UndoMetadata(ctx, ep.PublicID, history[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := l.d.Item(ctx, limited, ep.PublicID); err == nil {
		t.Fatal("after Undo the limited profile can open an episode of an adults-only show")
	}
}

// Merging a show into an adults-only show hides the episodes that move across.
func TestMergedEpisodesTakeTheirNewShowsRating(t *testing.T) {
	l := newLibrary(t, 1500)
	age := 13
	limited, err := l.d.CreateSeedProfile(ctx, db.SeedProfile{Name: "limited-by-rating", MaxRatingAge: &age})
	if err != nil {
		t.Fatal(err)
	}
	shows := showsLibrary(l)
	visible := everything(t, l.d, limited, db.ItemQuery{Library: shows.PublicID, Kinds: []db.ItemKind{db.KindShow}, Limit: 200})
	from, into := visible[0], visible[1]
	locks, err := l.d.MetadataLocks(ctx, into.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	rating := "NC-17"
	if _, err := l.d.EditMetadata(ctx, into.PublicID, locks.Revision, db.MetadataEdit{ContentRating: &rating}); err != nil {
		t.Fatal(err)
	}
	if err := l.d.MergeItems(ctx, from.PublicID, into.PublicID); err != nil {
		t.Fatal(err)
	}
	for _, e := range everything(t, l.d, limited, db.ItemQuery{Library: shows.PublicID, Kinds: []db.ItemKind{db.KindEpisode}, Limit: 200}) {
		if e.RootPublicID == into.PublicID {
			t.Fatalf("the limited profile is offered %q, an episode of the adults-only show it was merged into", e.Title)
		}
	}
}

func showsLibrary(l *library) db.Library {
	for _, lib := range l.libs {
		if lib.Kind == db.LibraryShows {
			return lib
		}
	}
	return db.Library{}
}
