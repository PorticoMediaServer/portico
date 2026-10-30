package httpapi

import (
	"testing"
	"time"
)

// NEW-28 part 2 (demo, 24 Sep): a live tune's first producer was stopped about
// a quarter of a second after it started, the channel reported
// "source_unavailable: context canceled", and the web player showed "The
// channel's source isn't responding" before the retry succeeded. The client's
// join is a tune, then a go-live seek once the stream exists. From the tune
// to active, the viewer must never see a recoverable failure.
func TestChannelsALiveJoinNeverReportsAFailure(t *testing.T) {
	eachChannelDriver(t, 60*time.Second, func(t *testing.T, s *channelSuite, d channelDriver) {
		ref := s.liveChannel(t, 2)
		h := d.tune(t, ref)
		joined := false
		deadline := time.Now().Add(45 * time.Second)
		seen := []string{}
		for {
			v := d.read(t, h)
			if len(seen) == 0 || seen[len(seen)-1] != v.Status+"/"+v.ErrorCode {
				seen = append(seen, v.Status+"/"+v.ErrorCode)
			}
			if v.Status == "recoverable" || v.ErrorCode != "" {
				t.Fatalf("the join reported a failure: %+v (statuses %v)", v, seen)
			}
			if !joined && v.StreamURL != "" {
				d.intent(t, h, "playing", nil, true)
				joined = true
			}
			if joined && active(v) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("no active live tune after 45 s: %+v (statuses %v)", v, seen)
			}
			time.Sleep(50 * time.Millisecond)
		}
		// A few more seconds of playing: still no failure.
		for end := time.Now().Add(4 * time.Second); time.Now().Before(end); time.Sleep(200 * time.Millisecond) {
			if v := d.read(t, h); v.Status == "recoverable" || v.ErrorCode != "" {
				t.Fatalf("after joining: %+v (statuses %v)", v, seen)
			}
		}
		t.Logf("statuses %v", seen)
		d.stop(t, h)
	})
}
