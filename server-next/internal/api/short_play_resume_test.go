package api

import (
	"context"
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
	// The point is set as made an hour ago, as a point is made before the next playing: the
	// state route keeps the real clock and the plays here run on the test's.
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
		set(map[string]any{"positionMs": point, "madeAgoMs": 3_600_000})
		th.clock.Set(th.clock.Now().Add(5 * time.Second))
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
	// A point under the mark is still the resume point: resumed from 1:30 and stopped at 2:30.
	// The film, whose point nothing here has touched yet, so the point set as old stays old.
	resumeFrom := func(item string, at, stop int64) int64 {
		t.Helper()
		if code := status(t, th.s, "PUT", "/v1/items/"+item+"/state", token, map[string]any{"positionMs": at, "madeAgoMs": 3_600_000}); code != 200 {
			t.Fatalf("state: %d", code)
		}
		th.clock.Set(th.clock.Now().Add(5 * time.Second))
		code, s := th.start(token, "", PlaybackStart{ItemID: item, StartFrom: "resume"})
		if code != 201 {
			t.Fatalf("start: %d", code)
		}
		th.clock.Set(th.clock.Now().Add(5 * time.Second))
		end := PlaybackStop{ProgressToken: s.ProgressToken, PositionMs: ptr(stop), PlayClockMs: 5_000}
		if code := status(t, th.s, "DELETE", "/v1/playback/sessions/"+s.ID, token, end); code != 204 {
			t.Fatalf("stop: %d", code)
		}
		return th.state(item).PositionMS
	}
	if at := resumeFrom(th.film, 90_000, 150_000); at != 150_000 {
		t.Fatalf("resumed at 1:30 (under the mark) and stopped at 2:30, the point is %d", at)
	}
	if at := resumeFrom(ep, 90_000, 10_000); at != 10_000 {
		t.Fatalf("resumed at 1:30 and sought back to 0:10, the point is %d", at)
	}

	// A playing that moved the point while live keeps rule 8 when its final position comes late:
	// opened from the start, sought to 25:00 (saved), then back to 0:15 out of reach.
	set(map[string]any{"positionMs": point, "madeAgoMs": 3_600_000})
	th.clock.Set(th.clock.Now().Add(5 * time.Second))
	code, late := th.start(token, "", PlaybackStart{ItemID: ep, StartFrom: "beginning"})
	if code != 201 {
		t.Fatalf("start: %d", code)
	}
	th.clock.Set(th.clock.Now().Add(5 * time.Second))
	if code := th.report(token, late.ID, "paused", 25*60_000); code != 204 && code != 200 {
		t.Fatalf("report: %d", code)
	}
	if at := th.state(ep).PositionMS; at != 25*60_000 {
		t.Fatalf("the seek to 25:00 saved %d", at)
	}
	// The server lets the session go (its device went quiet, and it is forgotten after a while),
	// so the final position comes with nothing but its bound to say how far the playing went.
	th.clock.Set(th.clock.Now().Add(3 * time.Minute))
	th.s.playback.Sweep(context.Background())
	th.clock.Set(th.clock.Now().Add(3 * time.Minute))
	th.s.playback.Sweep(context.Background())
	if _, ok := th.s.playback.Furthest(late.ID, 1); ok {
		t.Fatal("the session was not let go")
	}
	final := PlaybackStop{ProgressToken: late.ProgressToken, PositionMs: ptr(int64(15_000)), PlayClockMs: 90_000, MadeAgoMs: 1_000}
	if code := status(t, th.s, "DELETE", "/v1/playback/sessions/"+late.ID, token, final); code != 204 {
		t.Fatalf("late stop: %d", code)
	}
	if at := th.state(ep).PositionMS; at != 15_000 {
		t.Fatalf("a late final at 0:15 after the playing moved the point to 25:00 left it at %d", at)
	}

	// A kept point still counts the title as played now: Continue Watching shows a title played
	// after it was taken off (rule 13), so a mis-start brings it back, at 20:00.
	// The other episode, whose point no earlier playing here has touched.
	other := th.eps[1]
	if code := status(t, th.s, "PUT", "/v1/items/"+other+"/state", token, map[string]any{"positionMs": point, "madeAgoMs": 3_600_000}); code != 200 {
		t.Fatalf("state: %d", code)
	}
	before := th.state(other).LastPlayedAt
	th.clock.Set(th.clock.Now().Add(5 * time.Second))
	code, s := th.start(token, "", PlaybackStart{ItemID: other, StartFrom: "beginning"})
	if code != 201 {
		t.Fatalf("start: %d", code)
	}
	th.clock.Set(th.clock.Now().Add(15 * time.Second))
	stop := PlaybackStop{ProgressToken: s.ProgressToken, PositionMs: ptr(int64(15_000)), PlayClockMs: 15_000}
	if code := status(t, th.s, "DELETE", "/v1/playback/sessions/"+s.ID, token, stop); code != 204 {
		t.Fatalf("stop: %d", code)
	}
	if got := th.state(other); got.PositionMS != point || got.LastPlayedAt <= before {
		t.Fatalf("a mis-start kept %d and moved last played from %d to %d", got.PositionMS, before, got.LastPlayedAt)
	}

	// A play made out of reach says nothing of where it began: rule 8 as it stands.
	set(map[string]any{"positionMs": point, "madeAgoMs": 3_600_000})
	batch := map[string]any{"plays": []map[string]any{{"itemId": ep, "playId": "away-1", "positionMs": 15_000, "playedMs": 15_000}}}
	if code := status(t, th.s, "POST", "/v1/items/state:batch", token, batch); code != 200 {
		t.Fatalf("batch: %d", code)
	}
	if at := th.state(ep).PositionMS; at != 15_000 {
		t.Fatalf("an out-of-reach play stopped at 0:15 left the point at %d", at)
	}
}

// A mis-start on one device keeps the point another device moved meanwhile: the phone opens the
// episode from the start by mistake, the TV resumes it and plays on to 25:00, and the phone's stop
// at 0:30 leaves 25:00.
func TestAMisStartKeepsWhatAnotherDevicePlayed(t *testing.T) {
	th := newTheatre(t)
	phone, ep := th.owner.AccessToken, th.eps[0]
	tv := onMain(t, th.s, signIn(t, th.s, 2)).AccessToken
	if code := status(t, th.s, "PUT", "/v1/items/"+ep+"/state", phone, map[string]any{"positionMs": 20 * 60_000, "madeAgoMs": 3_600_000}); code != 200 {
		t.Fatalf("state: %d", code)
	}
	code, mistake := th.start(phone, "", PlaybackStart{ItemID: ep, StartFrom: "beginning"})
	if code != 201 {
		t.Fatalf("phone start: %d", code)
	}
	th.clock.Set(th.clock.Now().Add(20 * time.Second))
	code, watched := th.start(tv, "", PlaybackStart{ItemID: ep, StartFrom: "resume"})
	if code != 201 {
		t.Fatalf("tv start: %d", code)
	}
	th.clock.Set(th.clock.Now().Add(5 * time.Second))
	if code := th.report(tv, watched.ID, "paused", 25*60_000); code != 204 && code != 200 {
		t.Fatalf("tv report: %d", code)
	}
	if at := th.state(ep).PositionMS; at != 25*60_000 {
		t.Fatalf("the TV's 25:00 saved %d", at)
	}
	th.clock.Set(th.clock.Now().Add(5 * time.Second))
	stop := PlaybackStop{ProgressToken: mistake.ProgressToken, PositionMs: ptr(int64(30_000)), PlayClockMs: 30_000}
	if code := status(t, th.s, "DELETE", "/v1/playback/sessions/"+mistake.ID, phone, stop); code != 204 {
		t.Fatalf("phone stop: %d", code)
	}
	if at := th.state(ep).PositionMS; at != 25*60_000 {
		t.Fatalf("the phone's mis-start overwrote the TV's 25:00 with %d", at)
	}
}
