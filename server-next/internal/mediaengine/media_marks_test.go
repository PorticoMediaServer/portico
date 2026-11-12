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
		seen       []bool
	}{
		{[]string{late, "audio starts at 13.440s, want about 12s"}, 0, []bool{true, false}},
		{[]string{ended, late}, 0, []bool{true, true}},
		{[]string{late, "the seek is planned copy_remux, the start video_conversion"}, 1, []bool{true, false}},
		{[]string{late, "audio has a 0.512s hole at 14.000s"}, 1, []bool{true, false}},
		{[]string{"segment-000003.m4s, named in video.m3u8, answered 500: \"\""}, 1, []bool{false, false}},
		{[]string{"video.m3u8 has 2 segments, wanted from 2"}, 1, []bool{false, false}},
		{nil, 0, []bool{false, false}},
	} {
		unexpected, seen := classify(marks, c.failed)
		if len(unexpected) != c.unexpected || len(seen) != len(c.seen) || seen[0] != c.seen[0] || seen[1] != c.seen[1] {
			t.Errorf("%q: unexpected %q, seen %v; want %d unexpected, seen %v", c.failed, unexpected, seen, c.unexpected, c.seen)
		}
	}
}
