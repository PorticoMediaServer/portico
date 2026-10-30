package httpapi

import (
	"strings"
	"testing"
	"time"
)

// BE-MEDIA-03 / SEC-03: turning the profile's Live TV switch off stops a live
// channel that is already playing. The running producer's next check stops it
// at once (a control fault, never retried as transient) and the next media
// request is refused; a new tune is refused too.
func TestChannelsTheLiveTVSwitchStopsAPlayingChannel(t *testing.T) {
	eachChannelDriver(t, 60*time.Second, func(t *testing.T, s *channelSuite, d channelDriver) {
		ref := s.liveChannel(t, 2)
		h := d.tune(t, ref)
		v := waitChannel(t, d, h, 45*time.Second, "the live tune becomes active", active)
		p, err := s.f.d.Identity.Authenticate(s.f.token)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.f.d.DB.Exec(`INSERT INTO profile_restrictions(profile_id,allow_live_tv) VALUES(?,0) ON CONFLICT(profile_id) DO UPDATE SET allow_live_tv=0,revision=revision+1`, p.ProfileID); err != nil {
			t.Fatal(err)
		}
		switched := time.Now()
		deadline := switched.Add(5 * time.Second)
		for {
			code, _ := s.media(v.StreamURL)
			if code == 403 || code == 404 || code == 410 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("media still answers %d five seconds after the Live TV switch went off", code)
			}
			time.Sleep(100 * time.Millisecond)
		}
		// The session ends with the reason, so the player can say why.
		if dv, ok := d.(*v1ChannelDriver); ok {
			sv := dv.session(t, h)
			if sv.State != "ended" || sv.End == nil || sv.End.Reason != "feature_restricted" {
				t.Fatalf("the session after the switch: state %q end %+v", sv.State, sv.End)
			}
		}
		// A new tune is refused while the switch is off.
		if dv, ok := d.(*v1ChannelDriver); ok {
			w, _ := dv.start(t, map[string]any{"channelId": v1ChannelID(ref), "state": "playing"})
			if w.Code != 403 || !strings.Contains(w.Body.String(), "feature_restricted") {
				t.Fatalf("a tune with the Live TV switch off: %d %s", w.Code, w.Body)
			}
		}
		d.stop(t, h)
	})
}
