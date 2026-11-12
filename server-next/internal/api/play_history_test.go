package api

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// The owner and administrators read every play, filtered by person and kind, and a
// summary of it; a member reads neither. A play stays, under the name it had, after its
// account is removed. An administrator held to some libraries reads every play, with the
// titles outside those withheld, and a summary of only theirs.
func TestPlayHistoryForTheOwnerAndAdministrators(t *testing.T) {
	th := newTheatre(t)
	owner := th.owner.AccessToken
	ada := addMember(t, th.s, owner, "ada", "admin")
	max := addMember(t, th.s, owner, "max", "member")
	admin := signInAs(t, th.s, "ada", testPassword, 1).AccessToken
	member := signInAs(t, th.s, "max", testPassword, 2).AccessToken

	finish := func(token, item string) {
		session := th.play(token, item)
		status(t, th.s, "DELETE", "/v1/playback/sessions/"+session.ID, token, PlaybackStop{PositionMs: ptr(hour)})
	}
	finish(member, th.film)
	finish(owner, th.eps[0])

	if code := status(t, th.s, "GET", "/v1/admin/play-history", member, nil); code != 403 {
		t.Errorf("a member read Play history: %d", code)
	}
	var all Page[PlayEntry]
	if w := call(t, th.s, "GET", "/v1/admin/play-history", admin, nil, &all); w.Code != 200 || len(all.Items) != 2 {
		t.Fatalf("an administrator's Play history answered %d: %s", w.Code, w.Body)
	}
	var theirs Page[PlayEntry]
	call(t, th.s, "GET", "/v1/admin/play-history?person="+max.ID, admin, nil, &theirs)
	if len(theirs.Items) != 1 || theirs.Items[0].Title != "Heat" || theirs.Items[0].Username != "max" || !theirs.Items[0].Away ||
		theirs.Items[0].LibraryName == "" || theirs.Items[0].DeviceName == "" {
		t.Errorf("the member's plays: %+v", theirs.Items)
	}
	var episodes Page[PlayEntry]
	if call(t, th.s, "GET", "/v1/admin/play-history?kind=episode", owner, nil, &episodes); len(episodes.Items) != 1 || episodes.Items[0].Title != "Secrets" {
		t.Errorf("episodes only: %+v", episodes.Items)
	}
	var summary PlaySummary
	if call(t, th.s, "GET", "/v1/admin/play-history/summary", owner, nil, &summary); len(summary.People) != 2 || len(summary.Titles) != 2 ||
		summary.Totals == nil || summary.Totals.Plays != 2 || summary.Window == 0 || summary.CountedFrom != nil {
		t.Errorf("the summary: %+v", summary)
	}

	// The member's account goes; the play stays under the name it had.
	if code := status(t, th.s, "DELETE", "/v1/admin/members/"+max.ID, owner, nil); code != 204 {
		t.Fatalf("removing the member answered %d", code)
	}
	var after Page[PlayEntry]
	call(t, th.s, "GET", "/v1/admin/play-history", owner, nil, &after)
	if len(after.Items) != 2 || after.Items[1].Username != "max" || after.Items[1].AccountID != "" {
		t.Errorf("after the member was removed: %+v", after.Items)
	}

	// The administrator is held to the music library: no film or episode plays are theirs to read.
	music, no := []string{th.libs["music"].PublicID}, false
	if code := status(t, th.s, "PATCH", "/v1/admin/members/"+ada.ID, owner, MemberEdit{AllLibraries: &no, Libraries: &music}); code != 200 {
		t.Fatalf("holding the administrator to music answered %d", code)
	}
	admin = signInAs(t, th.s, "ada", testPassword, 3).AccessToken
	// They read every play, with the titles of the plays outside music withheld (the lead's ruling,
	// 12 Nov, as Now playing withholds them); searching or filtering by library reveals none.
	var held Page[PlayEntry]
	call(t, th.s, "GET", "/v1/admin/play-history", admin, nil, &held)
	if len(held.Items) != 2 {
		t.Fatalf("an administrator held to music read %d plays: %+v", len(held.Items), held.Items)
	}
	for _, p := range held.Items {
		if !p.Withheld || p.Title != "" || p.Subtitle != "" || p.ItemID != "" || p.LibraryID != "" || p.LibraryName != "" ||
			p.Username == "" || p.DeviceName == "" || p.PlayedAt.IsZero() || p.Delivery == "" {
			t.Errorf("a play outside the administrator's libraries: %+v", p)
		}
	}
	var searched, filtered Page[PlayEntry]
	if call(t, th.s, "GET", "/v1/admin/play-history?text=Heat", admin, nil, &searched); len(searched.Items) != 0 {
		t.Errorf("a search found a withheld title: %+v", searched.Items)
	}
	if call(t, th.s, "GET", "/v1/admin/play-history?library="+th.libs[db.LibraryMovies].PublicID, admin, nil, &filtered); len(filtered.Items) != 0 {
		t.Errorf("a library filter read a library that is not theirs: %+v", filtered.Items)
	}
	var mine Page[PlayEntry]
	if call(t, th.s, "GET", "/v1/admin/play-history", owner, nil, &mine); len(mine.Items) != 2 || mine.Items[0].Withheld || mine.Items[0].Title == "" {
		t.Errorf("the owner sees every title: %+v", mine.Items)
	}
	// The server-wide totals are not theirs either: they would count the other libraries.
	var heldSummary PlaySummary
	if call(t, th.s, "GET", "/v1/admin/play-history/summary", admin, nil, &heldSummary); heldSummary.Totals != nil || len(heldSummary.Titles) != 0 {
		t.Errorf("an administrator held to music read the summary %+v", heldSummary)
	}
}

// The owner's Play history lists every play (console rule 16), with how long was watched, and
// plays made under "Pause history" (rule 18); a profile's own History lists only finished plays
// (personal state rule 18). A finished play is listed once, and a playing that never played a
// second leaves nothing.
func TestEveryPlayIsInPlayHistoryAndOnlyFinishedOnesInHistory(t *testing.T) {
	th := newTheatre(t)
	ctx, token, refresh := context.Background(), th.owner.AccessToken, th.owner.RefreshToken
	renewals := 0
	// The clock moves past an access token's life: the sign-in is renewed, as an app does.
	advance := func(d time.Duration) {
		t.Helper()
		th.clock.Add(d)
		renewals++
		code, renewed := renew(t, th.s, refresh, 0, "play-history-"+strconv.Itoa(renewals))
		if code != 200 {
			t.Fatalf("renewing the sign-in answered %d", code)
		}
		token, refresh = renewed.AccessToken, renewed.RefreshToken
	}
	// Plays for some time, saving as the server does while it plays, then stops at stopAt.
	play := func(item string, played time.Duration, stopAt int64) {
		t.Helper()
		session := th.play(token, item)
		advance(played)
		ms := played.Milliseconds()
		if code := status(t, th.s, "PUT", "/v1/playback/sessions/"+session.ID+"/progress", token,
			PlaybackProgress{State: "playing", PositionMs: min(ms, stopAt), PlayClockMs: ms}); code != 200 {
			t.Fatalf("reporting progress answered %d", code)
		}
		th.s.playback.Save(ctx, db.Background)
		status(t, th.s, "DELETE", "/v1/playback/sessions/"+session.ID, token, PlaybackStop{PositionMs: &stopAt})
		advance(time.Minute)
	}
	plays := func() []PlayEntry {
		t.Helper()
		return get[Page[PlayEntry]](t, th.s, token, "/v1/admin/play-history").Items
	}
	history := func() []HistoryEntry {
		t.Helper()
		return get[HistoryPage](t, th.s, token, "/v1/history").Items
	}

	// Stopped part-way after twenty minutes: in Play history with its time, not in History.
	play(th.film, 20*time.Minute, 20*time.Minute.Milliseconds())
	if got := plays(); len(got) != 1 || got[0].Title != "Heat" || got[0].WatchedMs != 20*time.Minute.Milliseconds() {
		t.Fatalf("Play history after a part-way play: %+v", got)
	}
	if got := history(); len(got) != 0 {
		t.Fatalf("a part-way play reached the profile's History: %+v", got)
	}
	// A play that never played a second leaves nothing.
	play(th.eps[1], 0, 0)
	if got := plays(); len(got) != 1 {
		t.Fatalf("a playing that never played was listed: %+v", got)
	}
	// Played through, with a save part-way: listed once in each.
	play(th.eps[0], 50*time.Minute, hour)
	if got := plays(); len(got) != 2 || got[0].Title != "Secrets" || got[0].WatchedMs != 50*time.Minute.Milliseconds() {
		t.Fatalf("Play history after a finished play: %+v", got)
	}
	if got := history(); len(got) != 1 || got[0].Title != "Secrets" {
		t.Fatalf("History after a finished play: %+v", got)
	}
	// Under Pause history: a part-way play is still the owner's record, and History is unchanged.
	if err := th.s.db.SetProfilePreference(ctx, 1, db.PrefPauseHistory, "true", th.clock.Now()); err != nil {
		t.Fatal(err)
	}
	play(th.film, 5*time.Minute, 5*time.Minute.Milliseconds())
	if got := plays(); len(got) != 3 || got[0].Title != "Heat" || got[0].WatchedMs != 5*time.Minute.Milliseconds() {
		t.Fatalf("Play history after a play under Pause history: %+v", got)
	}
	if got := history(); len(got) != 1 {
		t.Fatalf("History after a play under Pause history: %+v", got)
	}
}
