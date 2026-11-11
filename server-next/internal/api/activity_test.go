package api

import "testing"

// The owner and administrators see every stream and can stop one with a message the viewer is
// told; a member sees neither. The stop is in the activity record, which only the owner reads.
func TestNowPlayingAndStop(t *testing.T) {
	th := newTheatre(t)
	owner := th.owner.AccessToken
	addMember(t, th.s, owner, "ada", "admin")
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
