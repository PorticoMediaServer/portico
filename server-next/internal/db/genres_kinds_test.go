package db_test

import (
	"slices"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A show whose every episode is rated past a profile's limit holds nothing that profile may
// see. It must not be counted under its genres or lend them its backdrop.
func TestGenresLeaveOutAShowEmptiedByTheRatingLimit(t *testing.T) {
	l := newLibrary(t, 1500)
	if err := l.d.ExecForTest(ctx, `UPDATE images SET role = ? WHERE role = ?`, db.ImageBackdrop, db.ImagePoster); err != nil {
		t.Fatal(err)
	}
	video := []db.ItemKind{db.KindMovie, db.KindShow}
	var tv db.Library
	for _, lib := range l.libs {
		if lib.Kind == db.LibraryShows {
			tv = lib
		}
	}
	shows := everything(t, l.d, l.all, db.ItemQuery{Library: tv.PublicID, Kinds: []db.ItemKind{db.KindShow}, Limit: 200})
	episodes := everything(t, l.d, l.all, db.ItemQuery{Library: tv.PublicID, Kinds: []db.ItemKind{db.KindEpisode}, Limit: 200})
	adult, emptied := 17, 0
	for n, show := range shows {
		if n%2 == 1 {
			continue
		}
		for _, e := range episodes {
			if e.RootID == show.ID {
				if err := l.d.ApplyDetails(ctx, db.Background, e.ID, db.ItemDetails{ContentRating: "R", RatingAge: &adult}); err != nil {
					t.Fatal(err)
				}
			}
		}
		emptied++
	}
	limit := 13
	v, err := l.d.CreateSeedProfile(ctx, db.SeedProfile{Name: "rated", MaxRatingAge: &limit, AllowUnrated: true})
	if err != nil {
		t.Fatal(err)
	}
	counted, err := l.d.Genres(ctx, v, "", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d of %d shows emptied; %d genres", emptied, len(shows), len(counted))
	for _, g := range counted {
		titles := everything(t, l.d, v, db.ItemQuery{Tag: g.PublicID, Kinds: video, Limit: 200})
		if g.Count != len(titles) {
			t.Errorf("genre %s: count %d, the list holds %d", g.Name, g.Count, len(titles))
		}
		var backdrops []string
		for _, it := range titles {
			for _, im := range it.Images {
				if im.Role == db.ImageBackdrop {
					backdrops = append(backdrops, im.FileHash)
				}
			}
		}
		if g.CoverHash != "" && !slices.Contains(backdrops, g.CoverHash) {
			t.Errorf("genre %s: its cover is not a backdrop of a title this profile may see", g.Name)
		}
	}
}

// An anime library's shows are shows: their genres belong in the list.
func TestGenresTakeInAnAnimeLibrary(t *testing.T) {
	l := newLibrary(t, 1500)
	before, err := l.d.Genres(ctx, l.all, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.d.ExecForTest(ctx, `UPDATE libraries SET kind = ? WHERE kind = ?`, db.LibraryAnime, db.LibraryShows); err != nil {
		t.Fatal(err)
	}
	after, err := l.d.Genres(ctx, l.all, "", true)
	if err != nil {
		t.Fatal(err)
	}
	total := func(list []db.Genre) (n int) {
		for _, g := range list {
			n += g.Count
		}
		return n
	}
	if total(after) != total(before) {
		t.Errorf("with the shows library an anime library: %d genres counting %d titles, was %d counting %d", len(after), total(after), len(before), total(before))
	}
}
