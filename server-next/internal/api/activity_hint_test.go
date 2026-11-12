package api

import (
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
)

// What the owner's activity panel shows is summed up so a change is noticed and told once: a
// stream starting, pausing, buffering, becoming a problem and stopping each change the summary;
// a report of where it is does not. A change reaches the owner's devices as one `activity` hint.
func TestTheOwnersDevicesAreToldWhenActivityChanges(t *testing.T) {
	th := newTheatre(t)
	owner := th.owner.AccessToken
	cursor := th.s.live.Now()
	quiet := th.s.activityNow()
	_, session := th.start(owner, "", PlaybackStart{ItemID: th.film})
	started := th.s.activityNow()
	if started == quiet {
		t.Fatal("a stream starting did not change the summary")
	}
	// The server's own once-a-second look tells the owner's devices, once.
	told := 0
	for deadline := time.Now().Add(5 * time.Second); told == 0 && time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		events, _, _ := th.s.live.After(1, 1, cursor)
		for _, e := range events {
			if e.Topic == "activity" && e.Op == "updated" && e.ID == "" {
				told++
			}
		}
	}
	if told != 1 {
		t.Fatalf("the owner's devices were told %d times that a stream started", told)
	}
	events, _, _ := th.s.live.After(2, 2, cursor)
	for _, e := range events {
		if e.Topic == "activity" {
			t.Fatal("another account heard about the owner's activity")
		}
	}
	th.report(owner, session.ID, playback.StatePlaying, 30_000)
	if th.s.activityNow() != started {
		t.Fatal("a report of where it is changed the summary")
	}
	steps := []struct {
		what string
		do   func()
	}{
		{"changing quality", func() {
			if code := status(t, th.s, "PATCH", "/v1/playback/sessions/"+session.ID, owner,
				PlaybackChange{Quality: &playback.Quality{Mode: "fixed", ID: "720p-high"}}); code != 200 {
				t.Fatalf("quality change: %d", code)
			}
		}},
		{"pausing", func() { th.report(owner, session.ID, playback.StatePaused, 30_000) }},
		{"buffering", func() { th.report(owner, session.ID, playback.StateBuffering, 30_000) }},
		{"becoming a problem", func() { th.clock.Add(ProblemAfter) }},
		{"stopping", func() { status(t, th.s, "DELETE", "/v1/playback/sessions/"+session.ID, owner, nil) }},
	}
	last := started
	for _, step := range steps {
		step.do()
		if now := th.s.activityNow(); now == last {
			t.Fatalf("%s did not change the summary", step.what)
		} else {
			last = now
		}
	}
}
