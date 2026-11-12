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

// The AV1 seek marks follow the FFmpeg: in force on FFmpeg 6, not on 8, nor on one whose
// version cannot be read.
func TestTheSeekMarksAreInForceOnlyOnFFmpeg6(t *testing.T) {
	for line, want := range map[string]bool{
		"ffmpeg version 6.1.1-3ubuntu5 Copyright (c) 2000-2023 the FFmpeg developers":   true,
		"ffmpeg version n6.0 Copyright (c) 2000-2023 the FFmpeg developers":             true,
		"ffmpeg version 8.0.1-3ubuntu2 Copyright (c) 2000-2025 the FFmpeg developers":   false,
		"ffmpeg version 8.1.3 Copyright (c) 2000-2026 the FFmpeg developers":            false,
		"ffmpeg version N-118000-gabcdef Copyright (c) 2000-2026 the FFmpeg developers": false,
	} {
		if got := hasSeekFault(line); got != want {
			t.Errorf("%q: in force %v, want %v", line, got, want)
		}
	}
}
