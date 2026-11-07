package writer

import (
	"testing"
)

func uoPersonalHistoryEvent(t *testing.T, w *executor, q uoPersonalIntentInput, kind string, sequence int64) int64 {
	t.Helper()
	var id int64
	err := w.db.QueryRowContext(t.Context(), `INSERT INTO play_history(occurred_at,event_kind,account_key,profile_id,playback_id,occurrence_generation,accepted_sequence,item_public_id,library_public_id,subject_id,activity_generation,membership_incarnation,profile_lifetime,started_ms) VALUES(?,?,'account-one','profile-one','playing-one',1,?,?,?,2111,1,?,?,1000) RETURNING id`, q.at, kind, sequence, q.publicID[:], q.library, q.policy.memberBirth, q.policy.profileBirth).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func TestUOPersonalHistoryPrivacyCutoffRetainsAuditAndSavedFlags(t *testing.T) {
	w, q := uoPersonalFixture(t)
	turn := uoPersonalTurn(t, w)
	_, err := applyUOPersonalIntent(t.Context(), turn, q)
	if err != nil {
		t.Fatal(err)
	}
	uoFlagsCommit(t, turn)
	wc008NativeExec(t, w, `UPDATE personal_items SET watchlisted=1,favorite=1,rating=7,play_count=2,played_ms=100 WHERE item_id=2111`)
	first := uoPersonalHistoryEvent(t, w, q, "started", 1)
	turn = uoPersonalTurn(t, w)
	e, err := applyUOPersonalClear(t.Context(), turn, uoPersonalClearInput{q.policy, uoClearActivityAndHistory, 1})
	if err != nil || e.head.activity != 2 || e.head.recommendation != 2 || e.head.cutoff != first+1 || e.head.action != 2 {
		t.Fatal(e, err)
	}
	uoFlagsCommit(t, turn)
	var rows, saved, favorite, rating, generation int64
	if err = w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM play_history),watchlisted,favorite,rating,activity_generation FROM personal_items WHERE item_id=2111`).Scan(&rows, &saved, &favorite, &rating, &generation); err != nil || rows != 1 || saved != 1 || favorite != 1 || rating != 7 || generation != 1 {
		t.Fatal(rows, saved, favorite, rating, generation, err)
	}
	// Captured playback from generation1 cannot resurrect current activity.
	turn = uoPersonalTurn(t, w)
	bad, err := applyUOPersonalIntent(t.Context(), turn, q)
	if err == nil || bad != (uoPersonalIntentEffect{}) {
		t.Fatal(bad, err)
	}
	uoPersonalRollback(t, turn)
	q.activityGeneration = 2
	q.expectedRevision = 1
	q.watched = false
	turn = uoPersonalTurn(t, w)
	_, err = applyUOPersonalIntent(t.Context(), turn, q)
	if err != nil {
		t.Fatal(err)
	}
	uoFlagsCommit(t, turn)
	var plays, played int64
	if err = w.db.QueryRowContext(t.Context(), `SELECT play_count,played_ms,watchlisted FROM personal_items WHERE item_id=2111`).Scan(&plays, &played, &saved); err != nil || plays != 0 || played != 0 || saved != 1 {
		t.Fatal("lazy reset failed", plays, played, saved, err)
	}
	// Hide-only does not clear current activity/recommendation generation.
	turn = uoPersonalTurn(t, w)
	hide, err := applyUOPersonalClear(t.Context(), turn, uoPersonalClearInput{q.policy, uoHideHistory, 3})
	if err != nil || hide.head.activity != 2 || hide.head.recommendation != 2 {
		t.Fatal(hide, err)
	}
	uoFlagsCommit(t, turn)
}
func TestUOPersonalHistoryMPBoundaryNoDuplicateTimeCredit(t *testing.T) {
	w, q := uoPersonalFixture(t)
	id := uoPersonalHistoryEvent(t, w, q, "started", 1)
	// COMPONENT ONLY: stand in for WC's already accepted personal prefix. Its
	// own native controls own occurrence/time acceptance. UO must not recount it.
	wc008NativeExec(t, w, `INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id,revision,started_count,play_count,played_ms,watchlisted,resume_position,resume_playback_id,last_started_at) VALUES('profile-one',?,2111,7,7,9,4,456,1,1234,'playing-one',?)`, q.publicID[:], q.at)
	b := uoPersonalBoundaryInput{policy: q.policy, subjectID: q.subjectID, libraryID: q.libraryID, eventID: id, occurrence: 1, acceptedSequence: 1, activityGeneration: 1, expectedRevision: 7, publicID: q.publicID, library: q.library, playback: "playing-one"}
	turn := uoPersonalTurn(t, w)
	e, err := applyUOPersonalBoundary(t.Context(), turn, b)
	if err != nil || e.revision != 7 {
		t.Fatal(e, err)
	}
	uoFlagsCommit(t, turn)
	var starts, plays, played, history, position, saved int64
	check := func(wantPlays, wantHistory int64) {
		t.Helper()
		if err = w.db.QueryRowContext(t.Context(), `SELECT started_count,play_count,played_ms,(SELECT count(*) FROM play_history),resume_position,watchlisted FROM personal_items WHERE item_id=2111`).Scan(&starts, &plays, &played, &history, &position, &saved); err != nil || starts != 9 || plays != wantPlays || played != 456 || history != wantHistory || position != 1234 || saved != 1 {
			t.Fatal("UO duplicated/rewrote source facts", starts, plays, played, history, position, saved, err)
		}
	}
	check(4, 1)
	id = uoPersonalHistoryEvent(t, w, q, "completed", 2)
	wc008NativeExec(t, w, `UPDATE personal_items SET revision=8,play_count=5,watched=1,last_completed_at=? WHERE item_id=2111`, q.at)
	b.eventID = id
	b.acceptedSequence = 2
	b.expectedRevision = 8
	b.completed = true
	turn = uoPersonalTurn(t, w)
	e, err = applyUOPersonalBoundary(t.Context(), turn, b)
	if err != nil || e.revision != 9 {
		t.Fatal(e, err)
	}
	uoFlagsCommit(t, turn)
	check(5, 2)
	turn = uoPersonalTurn(t, w)
	e, err = applyUOPersonalBoundary(t.Context(), turn, b)
	if err == nil || e != (uoPersonalBoundaryEffect{}) {
		t.Fatal("stale source row replay", e, err)
	}
	uoPersonalRollback(t, turn)
}
func TestUOPersonalHistoryBoundaryNeedsAcceptedPersonalPrefix(t *testing.T) {
	w, q := uoPersonalFixture(t)
	id := uoPersonalHistoryEvent(t, w, q, "started", 1)
	b := uoPersonalBoundaryInput{policy: q.policy, subjectID: q.subjectID, libraryID: q.libraryID, eventID: id, occurrence: 1, acceptedSequence: 1, activityGeneration: 1, expectedRevision: 1, publicID: q.publicID, library: q.library, playback: "playing-one"}
	turn := uoPersonalTurn(t, w)
	out, err := applyUOPersonalBoundary(t.Context(), turn, b)
	if err == nil || out != (uoPersonalBoundaryEffect{}) {
		t.Fatal("history row minted occurrence credit", out, err)
	}
	uoPersonalRollback(t, turn)
	var rows int64
	if err = w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM personal_items`).Scan(&rows); err != nil || rows != 0 {
		t.Fatal(rows, err)
	}
}
