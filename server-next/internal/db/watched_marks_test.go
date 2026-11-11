package db_test

import (
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/seed"
)

// A show, season or album carries a watched mark for each profile. After every kind of change
// that can move it, each profile's stored mark on every holder must equal what is under the
// holder for that profile, and working every mark out again from nothing must change none.
func TestWatchedMarkFollowsEveryChange(t *testing.T) {
	l := newLibrary(t, 1500)
	v := l.all
	var shows db.Library
	for _, lib := range l.libs {
		if lib.Kind == db.LibraryShows {
			shows = lib
		}
	}
	check := func(when string) {
		t.Helper()
		_, wrong, err := db.WatchedMarks(l.d)
		if err != nil {
			t.Fatal(err)
		}
		if len(wrong) > 0 {
			t.Fatalf("%s: %d marks are wrong, the first: %s", when, len(wrong), wrong[0])
		}
	}
	yes, no := true, false
	listed := func(show db.Item, watched bool) bool {
		t.Helper()
		for _, item := range everything(t, l.d, v, db.ItemQuery{Library: shows.PublicID, Kinds: []db.ItemKind{db.KindShow}, Watched: &watched, Limit: 200}) {
			if item.PublicID == show.PublicID {
				return true
			}
		}
		return false
	}
	all := everything(t, l.d, v, db.ItemQuery{Library: shows.PublicID, Kinds: []db.ItemKind{db.KindShow}, Limit: 200})
	episodes := map[string][]db.Item{}
	for _, episode := range everything(t, l.d, v, db.ItemQuery{Library: shows.PublicID, Kinds: []db.ItemKind{db.KindEpisode}, Limit: 200}) {
		episodes[episode.RootPublicID] = append(episodes[episode.RootPublicID], episode)
	}
	if len(all) < 4 {
		t.Fatalf("the test library has %d shows", len(all))
	}
	check("after the seeder's own state")

	// One episode at a time: the show is watched only when the last one is.
	first := all[0]
	for _, episode := range episodes[first.PublicID] {
		state(t, l.d, v, episode.PublicID, db.StateEdit{Watched: &yes})
	}
	if !listed(first, true) || listed(first, false) {
		t.Fatal("a show with every episode watched is not listed as watched")
	}
	check("after watching every episode of a show")
	last := episodes[first.PublicID][len(episodes[first.PublicID])-1]
	state(t, l.d, v, last.PublicID, db.StateEdit{Watched: &no})
	if listed(first, true) || !listed(first, false) {
		t.Fatal("a show with one episode unwatched is still listed as watched")
	}
	check("after unwatching one episode")

	// Playback reaching the end finishes the show again.
	device, err := db.DeviceForTest(l.d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.d.SaveProgress(ctx, db.User, []db.Progress{{Viewer: v, ItemID: last.PublicID, DeviceID: device, PlayID: "play-1",
		PositionMS: last.DurationMS, At: time.Now(), WatchedMS: last.DurationMS}}); err != nil {
		t.Fatal(err)
	}
	if !listed(first, true) {
		t.Fatal("playing the last episode to its end did not make the show watched")
	}
	check("after playback reached the end")

	// A whole show marked at once, and taken back.
	second := all[1]
	if _, _, err := l.d.SetState(ctx, v, second.PublicID, db.StateEdit{Watched: &no}); err != nil {
		t.Fatal(err)
	}
	_, undo, err := l.d.SetState(ctx, v, second.PublicID, db.StateEdit{Watched: &yes})
	if err != nil || undo == "" {
		t.Fatalf("marking a show watched: undo %q, %v", undo, err)
	}
	if !listed(second, true) {
		t.Fatal("a show marked watched is not listed as watched")
	}
	check("after marking a whole show")
	if err := l.d.UndoState(ctx, v, undo); err != nil {
		t.Fatal(err)
	}
	if listed(second, true) {
		t.Fatal("a show whose mark was undone is still listed as watched")
	}
	check("after undoing it")

	// Many at once, for another profile too.
	third := all[2]
	var edits []db.ItemEdit
	for _, episode := range episodes[third.PublicID] {
		edits = append(edits, db.ItemEdit{ItemID: episode.PublicID, StateEdit: db.StateEdit{Watched: &yes}})
	}
	for _, who := range []db.Viewer{v, l.viewers[1], l.viewers[9]} {
		if _, _, err := l.d.SetStates(ctx, db.User, who, edits); err != nil {
			t.Fatal(err)
		}
	}
	check("after a batch for three profiles")

	// An unwatched episode is removed: what is left of the show is all watched.
	state(t, l.d, v, last.PublicID, db.StateEdit{Watched: &no})
	if listed(first, true) {
		t.Fatal("the show is listed as watched with an episode unwatched")
	}
	if err := db.RemoveItem(l.d, last.PublicID); err != nil {
		t.Fatal(err)
	}
	if !listed(first, true) {
		t.Fatal("removing the only unwatched episode did not make the show watched")
	}
	check("after an episode was removed")

	// More of the library arrives: new episodes under shows people had finished.
	if _, err := seed.Import(ctx, l.d, seed.Config{Seed: 7, Items: 2400}, nil); err != nil {
		t.Fatal(err)
	}
	check("after more episodes arrived")

	// What a profile may see changes. Limited to the film library it sees no episode, so no
	// show can be watched for it; given every library again, the shows it finished are.
	profiles, err := l.d.Profiles(ctx, v.UserID)
	if err != nil || len(profiles) == 0 {
		t.Fatalf("profiles: %v", err)
	}
	var films string
	for _, lib := range l.libs {
		if lib.Kind == db.LibraryMovies {
			films = lib.PublicID
		}
	}
	marked, _, err := db.WatchedMarks(l.d)
	if err != nil {
		t.Fatal(err)
	}
	age := 13
	for _, r := range []db.Restrictions{{Libraries: []string{films}}, {AllLibraries: true, MaxRatingAge: &age}, {AllLibraries: true}} {
		if _, err := l.d.SetProfileRestrictions(ctx, v.UserID, profiles[0].PublicID, r, time.Now()); err != nil {
			t.Fatal(err)
		}
		check("after what the profile may see changed")
	}
	back, _, err := db.WatchedMarks(l.d)
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != len(marked) || len(marked) == 0 {
		t.Fatalf("%d holders were marked before the profile's limits came and went, %d after", len(marked), len(back))
	}

	// Working every mark out from nothing agrees with what the changes left.
	before, _, err := db.WatchedMarks(l.d)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) == 0 {
		t.Fatal("no holder is marked watched for anyone, so this test compared nothing")
	}
	if err := l.d.RebuildWatchedMarks(ctx); err != nil {
		t.Fatal(err)
	}
	after, wrong, err := db.WatchedMarks(l.d)
	if err != nil || len(wrong) > 0 {
		t.Fatalf("after a rebuild: %v, %v", err, wrong)
	}
	same := len(before) == len(after)
	for mark := range before {
		same = same && after[mark]
	}
	if !same {
		t.Fatalf("a rebuild changed the marks: %d before, %d after", len(before), len(after))
	}
}
