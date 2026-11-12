package db_test

import (
	"slices"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Reset recommendations (area 6 rule 22) forgets the profile's taste: its mood rows go at once
// and come back only from what it watches or likes afterwards. Its watched marks, favourites,
// ratings, resume points and Not interested marks stay as they were.
func TestResetRecommendationsForgetsTheTasteAndNothingElse(t *testing.T) {
	l, films, _ := moodLibrary(t)
	v := l.viewers[0]
	for _, film := range films[:3] {
		watch(t, l, v, film.PublicID)
	}
	yes, rating, at := true, 8, int64(90_000)
	edits := map[string]db.StateEdit{films[0].PublicID: {Favorite: &yes, Rating: &rating}, films[5].PublicID: {PositionMS: &at},
		films[6].PublicID: {Hidden: &yes}}
	for id, edit := range edits {
		if _, _, err := l.d.SetState(ctx, v, id, edit); err != nil {
			t.Fatal(err)
		}
	}
	if rows := moodRows(t, l, v); len(rows) != 1 {
		t.Fatalf("before the reset: %d mood rows, want 1", len(rows))
	}
	before := map[string]db.State{}
	for _, film := range films[:7] {
		before[film.PublicID] = stateOf(t, l, v, film.PublicID)
	}

	time.Sleep(2 * time.Millisecond) // the reset is later than every play before it
	_, from, _, err := l.d.Events(ctx, v, 0, 100_000)
	if err != nil {
		t.Fatal(err)
	}
	_, otherFrom, _, err := l.d.Events(ctx, l.viewers[1], 0, 100_000)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.d.ResetTaste(ctx, v, time.Now()); err != nil {
		t.Fatal(err)
	}
	// The profile's other devices are told to read again what they show; no other profile is.
	told := func(who db.Viewer, after int64) bool {
		t.Helper()
		events, _, _, err := l.d.Events(ctx, who, after, 100)
		if err != nil {
			t.Fatal(err)
		}
		return slices.ContainsFunc(events, func(e db.Event) bool { return e.Topic == db.TopicState && e.PublicID == "" })
	}
	if !told(v, from) || told(l.viewers[1], otherFrom) {
		t.Errorf("after the reset the profile was told %v and another profile %v; want true and false", told(v, from), told(l.viewers[1], otherFrom))
	}
	// At once, before anything is chosen again, and after.
	if rows, err := l.d.MoodRows(ctx, v); err != nil || len(rows) != 0 {
		t.Fatalf("straight after the reset: %d mood rows, %v", len(rows), err)
	}
	if rows := moodRows(t, l, v); len(rows) != 0 {
		t.Fatalf("after the reset the old plays still give %d mood rows", len(rows))
	}
	for id, was := range before {
		if now := stateOf(t, l, v, id); now.Watched != was.Watched || now.Favorite != was.Favorite || now.Hidden != was.Hidden ||
			now.PositionMS != was.PositionMS || (now.Rating == nil) != (was.Rating == nil) || now.PlayCount != was.PlayCount {
			t.Errorf("the reset changed %s's state from %+v to %+v", id, was, now)
		}
	}
	// What it watches afterwards is its taste again, and the row names only that.
	time.Sleep(2 * time.Millisecond)
	for _, film := range films[10:13] {
		watch(t, l, v, film.PublicID)
	}
	rows := moodRows(t, l, v)
	if len(rows) != 1 {
		t.Fatalf("after three cozy films since the reset: %d mood rows, want 1", len(rows))
	}
	for _, because := range rows[0].Because {
		if _, old := before[because.Item.PublicID]; old {
			t.Errorf("the row still names %q, watched before the reset", because.Item.Title)
		}
	}
}

func stateOf(t *testing.T, l *library, v db.Viewer, itemID string) db.State {
	t.Helper()
	detail, err := l.d.Item(ctx, v, itemID)
	if err != nil {
		t.Fatal(err)
	}
	return detail.State
}
