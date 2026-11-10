package db_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/titledata/titledatatest"
)

// moodLibrary is the seeded library with a title data file in use: thirty films and five shows
// are cozy, ten more films folk horror, and the first film is like the next twelve and the first
// show, best first.
func moodLibrary(t *testing.T) (*library, []db.ItemDetail, []db.ItemDetail) {
	t.Helper()
	l := newLibrary(t, 900)
	films, shows := titles(t, l, db.KindMovie, 40), titles(t, l, db.KindShow, 5)
	f := titledatatest.File{Vocabulary: []titledatatest.Tag{{ID: 1, Key: "mood:cozy", RowLabel: "cozy stories"},
		{ID: 2, Key: "subgenre:folk-horror", RowLabel: "folk horror"}}}
	for n, film := range films {
		w := titledatatest.Work{QID: int64(100 + n), IDs: map[int]string{titledatatest.TMDBMovie: externalID(t, film, "tmdb")},
			Tags: map[int]int{1: 3}}
		if n >= 30 {
			w.Tags = map[int]int{2: 2}
		}
		if n == 0 {
			for similar := range 12 {
				w.Similar = append(w.Similar, int64(101+similar))
			}
			w.Similar = append(w.Similar, 200)
		}
		f.Works = append(f.Works, w)
	}
	for n, show := range shows {
		f.Works = append(f.Works, titledatatest.Work{QID: int64(200 + n), Show: true,
			IDs: map[int]string{titledatatest.TVDBSeries: externalID(t, show, "tvdb")}, Tags: map[int]int{1: 2}})
	}
	useTitleData(t, l.d, f)
	applyTitleData(t, l.d)
	return l, films, shows
}

func sees(t *testing.T, l *library, v db.Viewer, itemID string) bool {
	t.Helper()
	_, err := l.d.Item(ctx, v, itemID)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		t.Fatal(err)
	}
	return err == nil
}

// watch plays a title part-way, as a player's progress reports do, then marks it watched.
func watch(t *testing.T, l *library, v db.Viewer, itemID string) {
	t.Helper()
	yes, at := true, int64(60_000)
	for _, edit := range []db.StateEdit{{PositionMS: &at}, {Watched: &yes}} {
		if _, _, err := l.d.SetState(ctx, v, itemID, edit); err != nil {
			t.Fatal(err)
		}
	}
}

// Every profile, restricted or not, is offered only titles it may see and has not watched, in
// one row at most, and is told only of titles it may see. More like this lists, best first,
// exactly the similar titles the profile may see.
func TestMoodRowsAndMoreLikeThisShowOnlyWhatTheProfileMaySee(t *testing.T) {
	l, films, shows := moodLibrary(t)
	for n, v := range l.viewers {
		watched := map[string]bool{}
		for _, film := range films[:30] {
			if len(watched) < 3 && sees(t, l, v, film.PublicID) {
				watch(t, l, v, film.PublicID)
				watched[film.PublicID] = true
			}
		}
		rows, err := l.d.MoodRows(ctx, v)
		if err != nil {
			t.Fatal(err)
		}
		offered := map[string]bool{}
		for _, row := range rows {
			for _, it := range row.Items {
				if !sees(t, l, v, it.PublicID) || watched[it.PublicID] || offered[it.PublicID] {
					t.Fatalf("profile %d: %q in %q is hidden from it, watched or already offered", n, it.Title, row.Key)
				}
				offered[it.PublicID] = true
			}
			for _, it := range row.Because {
				if !watched[it.PublicID] {
					t.Fatalf("profile %d: %q names %q, which it did not watch or may not see", n, row.Key, it.Title)
				}
			}
		}
		if n == 0 && (len(rows) != 1 || rows[0].Key != "mood:cozy" || rows[0].Label != "cozy stories" || len(rows[0].Items) < 6 ||
			len(rows[0].Because) != 2) {
			t.Fatalf("an unrestricted profile that watched three cozy films should get one cozy row naming two of them: %+v", rows)
		}

		like, err := l.d.MoreLikeThis(ctx, v, films[0].PublicID)
		if !sees(t, l, v, films[0].PublicID) {
			if !errors.Is(err, db.ErrNotFound) {
				t.Fatalf("profile %d: more like a title it may not see answered %v", n, err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		var want, got []string
		for _, similar := range append(slices.Clone(films[1:13]), shows[0]) {
			if sees(t, l, v, similar.PublicID) {
				want = append(want, similar.PublicID)
			}
		}
		for _, it := range like {
			got = append(got, it.PublicID)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("profile %d: more like this is %v, want %v", n, got, want)
		}
	}
}

// A title the profile may no longer see does not count towards what it likes: with one of its
// three cozy films now hidden, it has no cozy row.
func TestHiddenTitlesDoNotCountTowardsAMoodRow(t *testing.T) {
	l, films, _ := moodLibrary(t)
	kid := l.viewers[len(l.viewers)-1] // rating limit 13
	v := l.viewers[1]
	var shown, hidden []string
	for _, film := range films[:30] {
		if sees(t, l, kid, film.PublicID) {
			shown = append(shown, film.PublicID)
		} else {
			hidden = append(hidden, film.PublicID)
		}
	}
	if len(shown) < 2 || len(hidden) < 1 {
		t.Fatalf("the seed's cozy films need both kinds: %d a child may see, %d not", len(shown), len(hidden))
	}
	for _, id := range []string{shown[0], shown[1], hidden[0]} {
		watch(t, l, v, id)
	}
	if rows, err := l.d.MoodRows(ctx, v); err != nil || len(rows) != 1 {
		t.Fatalf("before the limit: %d rows, %v", len(rows), err)
	}
	profile, err := l.d.ProfileByID(ctx, v.UserID, v.ProfileID)
	if err != nil {
		t.Fatal(err)
	}
	age := 13
	if _, err := l.d.SetProfileRestrictions(ctx, v.UserID, profile.PublicID, db.Restrictions{MaxRatingAge: &age, AllLibraries: true},
		time.Now()); err != nil {
		t.Fatal(err)
	}
	if rows, err := l.d.MoodRows(ctx, v); err != nil || len(rows) != 0 {
		t.Fatalf("with one of its three cozy films hidden the profile still has %d mood rows (%v)", len(rows), err)
	}
}
