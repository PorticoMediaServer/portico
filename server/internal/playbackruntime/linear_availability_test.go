package playbackruntime

import (
	"errors"
	"testing"
)

// Live TV is gated on the real sandbox probe, not on the OS: a Linux host with
// working bubblewrap runs live like macOS does.
func TestLinearAvailabilityFollowsTheConfinementProbe(t *testing.T) {
	missing := errors.New("missing")
	unconfined := errors.New("bwrap (bubblewrap) is not on the server's PATH")
	for _, c := range []struct {
		ffmpeg, ffprobe error
		confined        bool
		want            string
	}{
		{nil, nil, true, ""},
		{nil, nil, false, "decoder_confinement_unavailable"},
		{missing, nil, true, "ffmpeg_not_configured"},
		{nil, missing, true, "ffprobe_not_configured"},
		{missing, missing, false, "decoder_confinement_unavailable"},
	} {
		got, cause := linearAvailability(c.ffmpeg, c.ffprobe, func() error {
			if c.confined {
				return nil
			}
			return unconfined
		})
		if got != c.want || (got == "") != (cause == nil) {
			t.Errorf("%+v: %q %v", c, got, cause)
		}
		if got == "decoder_confinement_unavailable" && !errors.Is(cause, unconfined) {
			t.Errorf("the probe's reason is lost: %v", cause)
		}
	}
}
