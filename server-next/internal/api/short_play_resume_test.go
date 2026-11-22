package api

import (
	"testing"
	"time"
)

// A play too short to count as started (opened from the start, stopped at 0:15) leaves the
// title's further resume point where it was; a play that counted as started and was then sought
// back to 0:15 stops there (personal state rule 8).
func TestAShortPlayKeepsAnEarlierResumePoint(t *testing.T) {
	th := newTheatre(t)
	token, ep := th.owner.AccessToken, th.eps[0]
	playTo := func(played time.Duration, stopAt int64) int64 {
		t.Helper()
		if code := status(t, th.s, "PUT", "/v1/items/"+ep+"/state", token, map[string]any{"positionMs": 1_200_000}); code != 200 {
			t.Fatalf("resume point: %d", code)
		}
		th.clock.Set(th.clock.Now().Add(time.Minute))
		code, s := th.start(token, "", PlaybackStart{ItemID: ep, StartFrom: "beginning"})
		if code != 201 {
			t.Fatalf("start: %d", code)
		}
		th.clock.Set(th.clock.Now().Add(played))
		stop := PlaybackStop{ProgressToken: s.ProgressToken, PositionMs: ptr(stopAt), PlayClockMs: played.Milliseconds()}
		if code := status(t, th.s, "DELETE", "/v1/playback/sessions/"+s.ID, token, stop); code != 204 {
			t.Fatalf("stop: %d", code)
		}
		return th.state(ep).PositionMS
	}
	if at := playTo(15*time.Second, 15_000); at != 1_200_000 {
		t.Fatalf("a 15-second play moved the 20-minute resume point to %d", at)
	}
	if at := playTo(10*time.Minute, 15_000); at != 15_000 {
		t.Fatalf("a play sought back to 0:15 after ten minutes stopped at %d", at)
	}
}
