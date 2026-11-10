package api

import (
	"testing"
	"time"
)

// Removing a title from Continue Watching takes it off the row until it is played again and
// changes nothing else; Not interested leaves it there.
func TestRemoveFromContinueWatching(t *testing.T) {
	th := newTheatre(t)
	token := th.owner.AccessToken
	stopAt := func(positionMs int64) {
		t.Helper()
		th.clock = time.Now() // the player reports where it is now
		session := th.play(token, th.film)
		status(t, th.s, "DELETE", "/v1/playback/sessions/"+session.ID, token, PlaybackStop{PositionMs: &positionMs})
		// Each step is at least a millisecond after the one before, as a person's taps are.
		time.Sleep(2 * time.Millisecond)
	}
	continuing := func() bool {
		t.Helper()
		for _, row := range get[Home](t, th.s, token, "/v1/home").Rows {
			if row.ID == "continue" {
				for _, item := range row.Items {
					if item.ID == th.film {
						return true
					}
				}
			}
		}
		return false
	}
	edit := func(body map[string]any) ItemState {
		t.Helper()
		var out StateReply
		if w := call(t, th.s, "PUT", "/v1/items/"+th.film+"/state", token, body, &out); w.Code != 200 {
			t.Fatalf("state %v answered %d: %s", body, w.Code, w.Body)
		}
		time.Sleep(2 * time.Millisecond)
		return out.ItemState
	}

	stopAt(30 * 60_000)
	if state := edit(map[string]any{"notInterested": true}); !state.NotInterested || !continuing() {
		t.Fatal("Not interested took the film off Continue Watching")
	}
	edit(map[string]any{"notInterested": false})
	for range 2 {
		if state := edit(map[string]any{"continueDismissed": true}); state.PositionMs != 30*60_000 || state.Watched || state.NotInterested {
			t.Fatalf("removing from Continue Watching changed the state: %+v", state)
		}
	}
	if continuing() {
		t.Fatal("the film is still on Continue Watching")
	}
	edit(map[string]any{"continueDismissed": false})
	if !continuing() {
		t.Fatal("undoing the removal did not put the film back")
	}
	edit(map[string]any{"continueDismissed": true})
	stopAt(35 * 60_000)
	if !continuing() {
		t.Fatal("playing the film again did not put it back on Continue Watching")
	}
}
