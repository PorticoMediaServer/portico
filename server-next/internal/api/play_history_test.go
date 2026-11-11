package api

import (
	"testing"
)

// The owner and administrators read every finished play, filtered by person and kind, and a
// summary of it; a member reads neither. A play stays, under the name it had, after its
// account is removed. An administrator held to some libraries reads only their plays.
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
	if call(t, th.s, "GET", "/v1/admin/play-history/summary", owner, nil, &summary); len(summary.People) != 2 || len(summary.Titles) != 2 {
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
	var held Page[PlayEntry]
	if call(t, th.s, "GET", "/v1/admin/play-history", admin, nil, &held); len(held.Items) != 0 {
		t.Errorf("an administrator held to music read %+v", held.Items)
	}
}
