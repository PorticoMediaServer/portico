package api

import (
	"testing"
	"time"
)

// Rule 8 as the owner ruled on 22 Nov: a playing opened under the started mark (3:00 of an
// hour) and stopped before reaching it leaves the earlier resume point where it was; every other
// stop moves the point to where the person stopped (decision R09), including a playing that began
// from the resume point and was sought back. A kept point still counts the title as played, so
// one taken off Continue Watching comes back (rule 13). Out-of-reach plays keep R09.
func TestAShortMisStartKeepsAnEarlierResumePoint(t *testing.T) {
	th := newTheatre(t)
	token, ep := th.owner.AccessToken, th.eps[0]
	const point = 20 * 60_000
	set := func(body map[string]any) {
		t.Helper()
		if code := status(t, th.s, "PUT", "/v1/items/"+ep+"/state", token, body); code != 200 {
			t.Fatalf("state %v: %d", body, code)
		}
	}
	// play opens the episode (from the beginning, or at its resume point), reports the
	// positions given a minute apart, and stops at the last with the time played.
	play := func(from string, played time.Duration, madeAgo int64, positions ...int64) int64 {
		t.Helper()
		set(map[string]any{"positionMs": point})
		th.clock.Set(th.clock.Now().Add(time.Minute))
		code, s := th.start(token, "", PlaybackStart{ItemID: ep, StartFrom: from})
		if code != 201 {
			t.Fatalf("start: %d", code)
		}
		for _, at := range positions[:len(positions)-1] {
			th.clock.Set(th.clock.Now().Add(time.Second))
			if code := th.report(token, s.ID, "playing", at); code != 204 && code != 200 {
				t.Fatalf("report %d: %d", at, code)
			}
		}
		th.clock.Set(th.clock.Now().Add(played))
		stop := PlaybackStop{ProgressToken: s.ProgressToken, PositionMs: ptr(positions[len(positions)-1]), PlayClockMs: played.Milliseconds(), MadeAgoMs: madeAgo}
		if code := status(t, th.s, "DELETE", "/v1/playback/sessions/"+s.ID, token, stop); code != 204 {
			t.Fatalf("stop: %d", code)
		}
		return th.state(ep).PositionMS
	}
	if at := play("beginning", 15*time.Second, 0, 15_000); at != point {
		t.Fatalf("opened from the start and stopped at 0:15, the 20:00 point moved to %d", at)
	}
	if at := play("beginning", 15*time.Second, 5_000, 15_000); at != point {
		t.Fatalf("the same with its final position sent late moved the point to %d", at)
	}
	if at := play("beginning", 4*time.Minute, 0, 15_000); at != 15_000 {
		t.Fatalf("opened from the start, played past 3:00 and sought back to 0:15, the point is %d", at)
	}
	if at := play("beginning", 20*time.Second, 0, 4*60_000, 15_000, 15_000); at != 15_000 {
		t.Fatalf("opened from the start, sought past 3:00 and back to 0:15, the point is %d", at)
	}
	if at := play("resume", 20*time.Second, 0, 90_000); at != 90_000 {
		t.Fatalf("resumed at 20:00 and sought back to 1:30 (rule 8), the point is %d", at)
	}

	// A kept point still counts the title as played now: Continue Watching shows a title played
	// after it was taken off (rule 13), so a mis-start brings it back, at 20:00.
	set(map[string]any{"positionMs": point})
	before := th.state(ep).LastPlayedAt
	th.clock.Set(th.clock.Now().Add(time.Minute))
	code, s := th.start(token, "", PlaybackStart{ItemID: ep, StartFrom: "beginning"})
	if code != 201 {
		t.Fatalf("start: %d", code)
	}
	th.clock.Set(th.clock.Now().Add(15 * time.Second))
	stop := PlaybackStop{ProgressToken: s.ProgressToken, PositionMs: ptr(int64(15_000)), PlayClockMs: 15_000}
	if code := status(t, th.s, "DELETE", "/v1/playback/sessions/"+s.ID, token, stop); code != 204 {
		t.Fatalf("stop: %d", code)
	}
	if got := th.state(ep); got.PositionMS != point || got.LastPlayedAt <= before {
		t.Fatalf("a mis-start kept %d and moved last played from %d to %d", got.PositionMS, before, got.LastPlayedAt)
	}

	// A play made out of reach says nothing of where it began: rule 8 as it stands.
	set(map[string]any{"positionMs": point})
	batch := map[string]any{"plays": []map[string]any{{"itemId": ep, "playId": "away-1", "positionMs": 15_000, "playedMs": 15_000}}}
	if code := status(t, th.s, "POST", "/v1/items/state:batch", token, batch); code != 200 {
		t.Fatalf("batch: %d", code)
	}
	if at := th.state(ep).PositionMS; at != 15_000 {
		t.Fatalf("an out-of-reach play stopped at 0:15 left the point at %d", at)
	}
}
