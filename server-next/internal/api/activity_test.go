package api

import "testing"

// The owner and administrators see every stream and can stop one with a message the viewer is
// told; a member sees neither. The stop is in the activity record, which only the owner reads.
func TestNowPlayingAndStop(t *testing.T) {
	th := newTheatre(t)
	owner := th.owner.AccessToken
	ada := addMember(t, th.s, owner, "ada", "admin")
	addMember(t, th.s, owner, "max", "member")
	admin := signInAs(t, th.s, "ada", testPassword, 1).AccessToken
	member := signInAs(t, th.s, "max", testPassword, 2).AccessToken
	th.play(owner, th.film)
	theirs := th.play(member, th.song)

	if code := status(t, th.s, "GET", "/v1/admin/sessions", member, nil); code != 403 {
		t.Errorf("a member read Now playing: %d", code)
	}
	var playing Page[NowPlaying]
	if w := call(t, th.s, "GET", "/v1/admin/sessions", admin, nil, &playing); w.Code != 200 || len(playing.Items) != 2 {
		t.Fatalf("the administrator's Now playing answered %d: %s", w.Code, w.Body)
	}
	var song NowPlaying
	for _, p := range playing.Items {
		if p.ID == theirs.ID {
			song = p
		}
	}
	if song.Username != "max" || song.Title != "Song" || song.DeviceName == "" || !song.Away {
		t.Errorf("the member's stream reads %+v", song)
	}
	// An administrator whose account is held to the music library sees every stream, and the
	// title only of the song.
	music, no := []string{th.libs["music"].PublicID}, false
	if code := status(t, th.s, "PATCH", "/v1/admin/members/"+ada.ID, owner, MemberEdit{AllLibraries: &no, Libraries: &music}); code != 200 {
		t.Fatalf("holding the administrator to music answered %d", code)
	}
	held := signInAs(t, th.s, "ada", testPassword, 3).AccessToken
	var limited Page[NowPlaying]
	call(t, th.s, "GET", "/v1/admin/sessions", held, nil, &limited)
	for _, p := range limited.Items {
		if p.ID == theirs.ID && p.Title != "Song" || p.ID != theirs.ID && p.Title != "" {
			t.Errorf("an administrator held to music reads %+v", p)
		}
	}
	if len(limited.Items) != 2 {
		t.Errorf("an administrator held to music sees %d streams, want 2", len(limited.Items))
	}
	admin = held // changing what the account may see renews its sign-ins
	if code := status(t, th.s, "DELETE", "/v1/admin/sessions/"+theirs.ID, member, StreamStop{}); code != 403 {
		t.Errorf("a member stopped a stream: %d", code)
	}
	if code := status(t, th.s, "DELETE", "/v1/admin/sessions/"+theirs.ID, admin, StreamStop{Message: "Server going down"}); code != 204 {
		t.Fatalf("stopping answered %d", code)
	}
	var ended PlaybackSession
	call(t, th.s, "GET", "/v1/playback/sessions/"+theirs.ID, member, nil, &ended)
	if ended.End == nil || ended.End.Reason != "terminated" || ended.End.Message != "Server going down" {
		t.Errorf("the viewer was told %+v", ended.End)
	}
	if code := status(t, th.s, "DELETE", "/v1/admin/sessions/"+theirs.ID, admin, StreamStop{}); code != 204 {
		t.Errorf("stopping it again answered %d", code)
	}
	if code := status(t, th.s, "GET", "/v1/admin/audit", admin, nil); code != 403 {
		t.Errorf("an administrator read the activity record: %d", code)
	}
	var record Page[ActivityEntry]
	call(t, th.s, "GET", "/v1/admin/audit?action=stream.", owner, nil, &record)
	if len(record.Items) != 1 || record.Items[0].Actor != "ada" || record.Items[0].Detail != "Server going down" {
		t.Errorf("the activity record of stops: %+v", record.Items)
	}
}
