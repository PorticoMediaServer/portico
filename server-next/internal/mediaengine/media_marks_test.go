package mediaengine_test

import "testing"

// A mark covers only the failure it names: anything else that fails in a marked case is
// still a finding. Runs in the ordinary job; it needs no media.
func TestAnExpectedFailureMarkHidesOnlyTheFailureItNames(t *testing.T) {
	marks := knownFailures["hevc-hdr10 for chromium after a seek"]
	late := "video starts at 13.417s, want about 12s"
	ended := `segment-000003.m4s, named in video.m3u8, answered 404: "{\"error\":{\"code\":\"not_found\",\"message\":\"This playback has ended. Start it again.\",\"retry\":false}}\n"`
	for _, c := range []struct {
		failed     []string
		unexpected int
		seen       bool
	}{
		{[]string{late, "audio starts at 13.440s, want about 12s"}, 2, false},
		{[]string{ended, late}, 1, true},
		{[]string{ended, "the seek is planned copy_remux, the start video_conversion"}, 1, true},
		{[]string{ended, "audio has a 0.512s hole at 14.000s"}, 1, true},
		{[]string{"segment-000003.m4s, named in video.m3u8, answered 500: \"\""}, 1, false},
		{[]string{"video.m3u8 has 2 segments, wanted from 2"}, 1, false},
		{nil, 0, false},
	} {
		unexpected, seen := classify(marks, c.failed)
		if len(unexpected) != c.unexpected || len(seen) != 1 || seen[0] != c.seen {
			t.Errorf("%q: unexpected %q, seen %v; want %d unexpected, seen %v", c.failed, unexpected, seen, c.unexpected, c.seen)
		}
	}
}
