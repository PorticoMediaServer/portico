package db_test

import (
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/seed"
)

// A holder is flagged when some playable title under it is rated or covered by labels
// differently from it, because only then can a profile see it and nothing under it. The flag
// must be set by the write that causes it: an import, more of the library arriving, a title
// given a rating of its own, a show given a new rating. Working every flag out again from
// nothing must then change none. Moves, splits and labels put on and taken off are checked by
// TestLabelsFollowMovesAndSplits; what a profile sees, by the restricted-profile tests.
func TestHolderFlagsAreSetByTheWritesThatCauseThem(t *testing.T) {
	l := newLibrary(t, 1500)
	check := func(when string) map[int64]bool {
		t.Helper()
		missing, extra, stored, err := db.HolderFlags(l.d)
		if err != nil {
			t.Fatal(err)
		}
		if len(missing) > 0 || extra > 0 {
			t.Fatalf("%s: %d holders not flagged, %d flagged for nothing; %v", when, len(missing), extra, missing)
		}
		return stored
	}
	flagged := func(stored map[int64]bool) (n int) {
		for _, flag := range stored {
			if flag {
				n++
			}
		}
		return n
	}
	// Labels on albums flag their artists at import.
	if flagged(check("after the import")) == 0 {
		t.Fatal("the import flagged no holder; the seeded library has labelled albums")
	}
	var shows []db.Item
	for _, lib := range l.libs {
		if lib.Kind == db.LibraryShows {
			shows = everything(t, l.d, l.all, db.ItemQuery{Library: lib.PublicID, Kinds: []db.ItemKind{db.KindShow}, Limit: 200})
			episodes := everything(t, l.d, l.all, db.ItemQuery{Library: lib.PublicID, Kinds: []db.ItemKind{db.KindEpisode}, Limit: 200})
			// One episode given a rating of its own, as an NFO file can say.
			adult := 17
			if err := l.d.ApplyDetails(ctx, db.Background, episodes[0].ID, db.ItemDetails{ContentRating: "R", RatingAge: &adult}); err != nil {
				t.Fatal(err)
			}
			// Then its show rated afresh: the other episodes follow the show, that one does not.
			kids := 0
			if err := l.d.ApplyDetails(ctx, db.Background, episodes[0].RootID, db.ItemDetails{ContentRating: "G", RatingAge: &kids}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(shows) == 0 {
		t.Fatal("no shows")
	}
	check("after an episode and its show were rated")
	// A new show whose one episode arrives with a rating of its own, as a scan reads from an
	// NFO file, flags its season and show in the same import.
	libraries, err := l.d.AllLibraries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, lib := range libraries {
		if lib.Kind != db.LibraryShows {
			continue
		}
		show, season, adult := db.ItemKey{Kind: db.KeyFolder, Key: "own-rating"}, db.ItemKey{Kind: db.KeyName, Key: "own-rating/1"}, 17
		kids, one := 0, 1
		if _, err := l.d.Import(ctx, db.ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: 1, Items: []db.ImportItem{
			{Kind: db.KindShow, Keys: []db.ItemKey{show}, Title: "Own rating", ContentRating: "G", RatingAge: &kids},
			{Kind: db.KindSeason, Keys: []db.ItemKey{season}, Parent: &show, Title: "Season 1", IndexNumber: &one},
			{Kind: db.KindEpisode, Keys: []db.ItemKey{{Kind: db.KeyName, Key: "own-rating/1/1"}}, Parent: &season, Title: "Pilot",
				IndexNumber: &one, ContentRating: "R", RatingAge: &adult},
		}}); err != nil {
			t.Fatal(err)
		}
	}
	check("after a show arrived with an episode rated on its own")
	if _, err := seed.Import(ctx, l.d, seed.Config{Seed: 7, Items: 2400}, nil); err != nil {
		t.Fatal(err)
	}
	before := check("after more of the library arrived")
	if err := l.d.RebuildHolderFlags(ctx); err != nil {
		t.Fatal(err)
	}
	after := check("after every flag was worked out again")
	for id, flag := range before {
		if after[id] != flag {
			t.Fatalf("working the flags out again changed item %d from %v to %v", id, flag, after[id])
		}
	}
}

// A profile with both a rating limit and a blocked label: an artist some of whose albums the
// label hides, and the rest of whose tracks are rated past the limit, is left with nothing,
// though neither restriction alone empties it. The artist and its albums must be gone from
// that profile's lists and its own page, and every total, genre count and letter rail must
// still be the size of its list, for that profile and for one with either restriction alone.
func TestAHolderEmptiedOnlyByARatingAndALabelTogether(t *testing.T) {
	l := newLibrary(t, 3000)
	var music db.Library
	for _, lib := range l.libs {
		if lib.Kind == db.LibraryMusic {
			music = lib
		}
	}
	labelsOf := func(id string) []string {
		detail, err := l.d.Item(ctx, l.all, id)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, tag := range detail.Tags {
			if tag.Kind == db.TagLabel {
				out = append(out, tag.PublicID)
			}
		}
		return out
	}
	artists := everything(t, l.d, l.all, db.ItemQuery{Library: music.PublicID, Kinds: []db.ItemKind{db.KindArtist}, Limit: 200})
	albums := everything(t, l.d, l.all, db.ItemQuery{Library: music.PublicID, Kinds: []db.ItemKind{db.KindAlbum}, Limit: 200})
	tracks := everything(t, l.d, l.all, db.ItemQuery{Library: music.PublicID, Kinds: []db.ItemKind{db.KindTrack}, Limit: 200})
	var artist db.Item
	var block []string
	plain := map[int64]bool{} // the artist's albums that carry no label
	for _, a := range artists {
		if len(labelsOf(a.PublicID)) > 0 {
			continue
		}
		var labels []string
		free := map[int64]bool{}
		for _, album := range albums {
			if album.ParentID != a.ID {
				continue
			}
			if own := labelsOf(album.PublicID); len(own) > 0 {
				labels = append(labels, own...)
			} else {
				free[album.ID] = true
			}
		}
		if len(labels) > 0 && len(free) > 0 {
			artist, block, plain = a, labels, free
			break
		}
	}
	if artist.ID == 0 {
		t.Fatal("the test library has no artist with a labelled album and an unlabelled one")
	}
	adult := 17
	rated := 0
	for _, track := range tracks {
		if track.RootID == artist.ID && plain[track.ParentID] {
			if err := l.d.ApplyDetails(ctx, db.Background, track.ID, db.ItemDetails{ContentRating: "R", RatingAge: &adult}); err != nil {
				t.Fatal(err)
			}
			rated++
		}
	}
	if rated == 0 {
		t.Fatal("no track to rate")
	}
	limit := 13
	profile := func(p db.SeedProfile) db.Viewer {
		v, err := l.d.CreateSeedProfile(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	byRating := profile(db.SeedProfile{Name: "rating", MaxRatingAge: &limit, AllowUnrated: true})
	byLabel := profile(db.SeedProfile{Name: "label", BlockedTags: block})
	both := profile(db.SeedProfile{Name: "both", MaxRatingAge: &limit, AllowUnrated: true, BlockedTags: block})

	has := func(v db.Viewer, kind db.ItemKind, id int64) bool {
		for _, it := range everything(t, l.d, v, db.ItemQuery{Library: music.PublicID, Kinds: []db.ItemKind{kind}, Limit: 200}) {
			if it.ID == id {
				return true
			}
		}
		return false
	}
	if !has(byRating, db.KindArtist, artist.ID) || !has(byLabel, db.KindArtist, artist.ID) {
		t.Fatal("the artist should still be listed for a profile with only one of the two restrictions")
	}
	if has(both, db.KindArtist, artist.ID) {
		t.Error("the artist is listed for the profile that can see nothing under it")
	}
	if _, err := l.d.Item(ctx, both, artist.PublicID); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("the artist's own page: %v; want not found", err)
	}
	for _, album := range albums {
		if album.ParentID == artist.ID && has(both, db.KindAlbum, album.ID) {
			t.Errorf("album %q of the emptied artist is listed", album.Title)
		}
	}
	for _, who := range []db.Viewer{both, byRating, byLabel} {
		for _, kinds := range [][]db.ItemKind{{db.KindArtist}, {db.KindAlbum}, {db.KindArtist, db.KindAlbum}, {db.KindArtist, db.KindAlbum, db.KindTrack}} {
			q := db.ItemQuery{Library: music.PublicID, Kinds: kinds, Limit: 200}
			list := everything(t, l.d, who, q)
			q.WithTotal = true
			page, err := l.d.Items(ctx, who, q)
			if err != nil {
				t.Fatal(err)
			}
			if page.Total != len(list) {
				t.Errorf("profile %d kinds %v: total %d, list %d", who.ProfileID, kinds, page.Total, len(list))
			}
			q.WithTotal = false
			for _, facet := range []string{db.FacetGenre, db.FacetDecade, db.FacetLetter} {
				values, err := l.d.Facets(ctx, who, q, facet)
				if err != nil {
					t.Fatal(err)
				}
				sum := 0
				for _, value := range values {
					sum += value.Count
					if facet == db.FacetGenre {
						with := q
						with.Tag = value.Value
						if n := len(everything(t, l.d, who, with)); n != value.Count {
							t.Errorf("profile %d kinds %v genre %s: count %d, list %d", who.ProfileID, kinds, value.Label, value.Count, n)
						}
					}
				}
				if facet == db.FacetLetter && sum != len(list) {
					t.Errorf("profile %d kinds %v: the letter rail adds to %d, list %d", who.ProfileID, kinds, sum, len(list))
				}
			}
		}
	}
}
