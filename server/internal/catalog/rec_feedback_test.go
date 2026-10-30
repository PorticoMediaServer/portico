package catalog

import (
	"database/sql"
	"errors"
	"testing"
)

// Recommendation feedback: "Not interested" through the personal batch
// (receipted, undoable) and "Reset recommendations".

func recBatch(t *testing.T, w *recWorld, operation, item string, notInterested bool) PersonalBatchResult {
	t.Helper()
	out, err := w.s.SetPersonalBatch("local:a", "p", PersonalBatchMutation{OperationID: operation, Items: []PersonalBatchItem{{ItemID: item, NotInterested: &notInterested}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 1 {
		t.Fatalf("results: %+v", out.Results)
	}
	return out.Results[0]
}

func TestNotInterestedIsReceiptedUndoableAndLeavesRecommended(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := newRecWorld(t)
	for _, f := range w.scifi[:3] {
		w.watch("p", f)
	}
	w.c.Drain()
	target := w.scifi[3].Public
	if !rankedHas(w.rank(t, "p"), target) {
		t.Fatal("setup: the film should be recommended")
	}
	r := recBatch(t, w, "hide-1", target, true)
	if !r.OK || r.Personal == nil || !r.Personal.NotInterested || r.Personal.Revision != 1 {
		t.Fatalf("not interested: %+v %+v", r, r.Personal)
	}
	// The mark shows on the very next load, before the taste worker runs.
	if rankedHas(w.rank(t, "p"), target) {
		t.Fatal("a title marked not interested is still recommended")
	}
	// A replay returns the first receipt; the state doesn't move again.
	if again := recBatch(t, w, "hide-1", target, true); !again.OK || again.Personal.Revision != 1 {
		t.Fatalf("replay: %+v", again.Personal)
	}
	if _, err := w.s.SetPersonalBatch("local:a", "p", PersonalBatchMutation{OperationID: "hide-1", Items: []PersonalBatchItem{{ItemID: target, Favorite: new(bool)}}}, nil); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("an operationId reused for another body: %v", err)
	}
	if state, err := w.s.Personal("p", target); err != nil || !state.NotInterested {
		t.Fatalf("read back: %+v %v", state, err)
	}
	w.c.Drain()
	w.check(t)
	if rankedHas(w.rank(t, "p"), target) {
		t.Fatal("the worker's result differs from the overlay")
	}
	// Undo.
	if r = recBatch(t, w, "hide-1-undo", target, false); !r.OK || r.Personal.NotInterested || r.Personal.Revision != 2 {
		t.Fatalf("undo: %+v", r.Personal)
	}
	w.c.Drain()
	w.check(t)
	if !rankedHas(w.rank(t, "p"), target) {
		t.Fatal("an undone mark still hides the title")
	}
}

func TestNotInterestedNamesAShowButNothingElseOnOne(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := newRecWorld(t)
	shows := w.c.Library("t", "Shows", "tv", "/t")
	show := w.c.Show(shows, "Series", 2020)
	w.c.Drain()
	yes := true
	out, err := w.s.SetPersonalBatch("local:a", "p", PersonalBatchMutation{OperationID: "show-1", Items: []PersonalBatchItem{{ItemID: show.Public, NotInterested: &yes}}}, nil)
	if err != nil || out.Updated != 1 || !out.Results[0].Personal.NotInterested {
		t.Fatalf("not interested on a show: %+v %v", out, err)
	}
	var queued int
	if err = w.c.DB.QueryRow(`SELECT count(*) FROM rec_profile_jobs WHERE profile_id='p' AND work_id=?`, show.ID).Scan(&queued); err != nil || queued != 1 {
		t.Fatalf("the show's taste job: %d %v", queued, err)
	}
	// Every other field is an item's: a show answers as absent, unchanged.
	out, err = w.s.SetPersonalBatch("local:a", "p", PersonalBatchMutation{OperationID: "show-2", Items: []PersonalBatchItem{{ItemID: show.Public, Watchlisted: &yes}}}, nil)
	if err != nil || out.Failed != 1 || out.Results[0].Code != "not_found" {
		t.Fatalf("watchlisted on a show: %+v %v", out, err)
	}
	if _, err = w.s.SetPersonal("local:a", "p", show.Public, PersonalMutation{OperationID: "show-3", Favorite: &yes}, nil); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("favorite on a show: %v", err)
	}
}

func TestResetRecommendationsForgetsTasteButNotHistory(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := newRecWorld(t)
	for _, f := range w.scifi[:3] {
		w.watch("p", f)
	}
	w.personal("p", w.romance[0], "favorite", 1)
	w.personal("p", w.romance[1], "rating", 9)
	w.personal("p", w.dramas[0], "rating", 1.0)
	w.c.Drain()
	recBatch(t, w, "hide-2", w.scifi[5].Public, true)
	// A watch the worker hasn't processed yet is settled by the reset too.
	w.watch("p", w.scifi[4])
	// Another profile's taste is not this reset's.
	w.watch("q", w.scifi[0])
	w.c.Drain()
	w.watch("p", w.scifi[6])
	count := func(query string, args ...any) int {
		t.Helper()
		var n int
		if err := w.c.DB.QueryRow(query, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	var before int64
	if err := w.c.DB.QueryRow(`SELECT revision FROM rec_profile_revisions WHERE profile_id='p'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	receipt, err := w.s.ResetRecommendations("p", RecommendationsReset{OperationID: "reset-1"}, nil)
	if err != nil || receipt.ClearedNotInterested != 1 {
		t.Fatalf("reset: %+v %v", receipt, err)
	}
	w.check(t)
	if n := count(`SELECT count(*) FROM rec_profile_taste WHERE profile_id='p'`); n != 0 {
		t.Fatalf("%d taste facets survived the reset", n)
	}
	if n := count(`SELECT count(*) FROM rec_profile_signals WHERE profile_id='p' AND (weight<>0 OR long<>0 OR short<>0)`); n != 0 {
		t.Fatalf("%d weighted signals survived the reset", n)
	}
	if n := count(`SELECT count(*) FROM rec_profile_jobs WHERE profile_id='p'`); n != 0 {
		t.Fatalf("%d taste jobs survived the reset", n)
	}
	if n := count(`SELECT count(*) FROM personal_items WHERE profile_id='p' AND not_interested=1`); n != 0 {
		t.Fatal("a not-interested mark survived the reset")
	}
	var after int64
	if err = w.c.DB.QueryRow(`SELECT revision FROM rec_profile_revisions WHERE profile_id='p'`).Scan(&after); err != nil || after <= before {
		t.Fatalf("the taste revision went from %d to %d: a memoised ranking could match again", before, after)
	}
	// History and lists stay.
	if n := count(`SELECT count(*) FROM personal_history WHERE profile_id='p'`); n != 5 {
		t.Fatalf("history: %d", n)
	}
	if n := count(`SELECT count(*) FROM personal_items WHERE profile_id='p' AND (favorite=1 OR rating IS NOT NULL)`); n != 3 {
		t.Fatalf("favorites and ratings: %d", n)
	}
	if n := count(`SELECT count(*) FROM rec_profile_taste WHERE profile_id='q'`); n == 0 {
		t.Fatal("another profile's taste was reset")
	}
	// The next load ranks like a new profile's (best rated first), still
	// without anything the profile watched, favorited, rated or disliked; the
	// title it had turned down is back.
	ranked := w.rank(t, "p")
	if len(ranked) == 0 || !publicSet(w.dramas)[ranked[0].ID] {
		t.Fatalf("after a reset the first recommendation should be a well-rated drama: %v", ranked[:min(5, len(ranked))])
	}
	for _, it := range append(append([]string{}, w.scifi[0].Public, w.scifi[1].Public, w.scifi[2].Public, w.scifi[4].Public, w.scifi[6].Public), w.romance[0].Public, w.romance[1].Public, w.dramas[0].Public) {
		if rankedHas(ranked, it) {
			t.Fatalf("%s is recommended after the reset, though the profile engaged with or disliked it", it)
		}
	}
	// A replay returns the first receipt and resets nothing further: what the
	// profile does after the reset is kept.
	w.watch("p", w.romance[2])
	w.c.Drain()
	learned := count(`SELECT count(*) FROM rec_profile_taste WHERE profile_id='p'`)
	if learned == 0 {
		t.Fatal("a watch after the reset taught nothing")
	}
	again, err := w.s.ResetRecommendations("p", RecommendationsReset{OperationID: "reset-1"}, nil)
	if err != nil || again != receipt {
		t.Fatalf("replay: %+v %v", again, err)
	}
	if n := count(`SELECT count(*) FROM rec_profile_taste WHERE profile_id='p'`); n != learned {
		t.Fatal("a replayed reset forgot what was learned after it")
	}
	w.check(t)
}

// Trending (a provider feed, scored by the older query) leaves out a title the
// profile marked not interested, as it leaves out a disliked one.
func TestTrendingLeavesOutNotInterested(t *testing.T) {
	f := recommendationFixtureDB(t)
	s := New(f.db)
	r := engineRequest("movies")
	engineExec(t, f, `INSERT INTO personal_items(profile_id,item_id,revision,not_interested) VALUES(?,?,1,1)`, r.Profile, f.items["m1"].ID)
	base, args := recBase(r, engineWholeLibrary(t, s, r))
	var negative int
	if err := s.read().QueryRow(base+` SELECT COALESCE(max(negative),0) FROM personal WHERE work IN(SELECT work FROM members WHERE id=?)`, append(args, f.items["m1"].ID)...).Scan(&negative); err != nil {
		t.Fatal(err)
	}
	if negative != 1 {
		t.Fatal("a not-interested title is not negative in the trending/community scorer")
	}
}
