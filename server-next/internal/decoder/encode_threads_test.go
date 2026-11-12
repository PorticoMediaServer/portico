package decoder

import (
	"strings"
	"testing"
)

// A software encode for playback gets the machine's share for the encodes already running:
// all of it alone, less as more start, never fewer than the two every encode had before, and
// no more than the cap. The same on any machine and at any size of picture.
func TestAnEncodeGetsItsShareOfTheMachine(t *testing.T) {
	was := processors
	t.Cleanup(func() { processors = was })
	for _, tc := range []struct {
		cpus    int
		running []int // the threads each next encode gets
	}{
		{16, []int{8, 8, 5, 4, 3, 2, 2, 2, 2}},
		{12, []int{8, 6, 4, 3, 2, 2, 2}},
		{4, []int{4, 2, 2, 2}},
		{2, []int{2, 2}},
		{1, []int{2}},
		{64, []int{8, 8, 8, 8, 8, 8, 8, 8, 7}},
	} {
		processors = func() int { return tc.cpus }
		var releases []func()
		for i, want := range tc.running {
			got, release := ReservePlaybackEncode()
			releases = append(releases, release)
			if got != want {
				t.Fatalf("%d processors, encode %d: %d threads, want %d", tc.cpus, i+1, got, want)
			}
		}
		for _, release := range releases {
			release()
			release() // counted out once, however often it is called
		}
		if got, release := ReservePlaybackEncode(); got != min(max(tc.cpus, 2), 8) {
			t.Fatalf("%d processors: after every encode ended a new one gets %d", tc.cpus, got)
		} else {
			release()
		}
	}
}

// The share reaches each software encoder as its own setting; unset, an encode keeps the two
// threads background work has always had. A hardware encode is never given threads.
func TestTheShareReachesEachSoftwareEncoder(t *testing.T) {
	facts := &ToolchainFacts{Version: "8.1.3", Encoders: map[string]bool{"libx264": true, "libx265": true, "libsvtav1": true},
		Filters: map[string]bool{"scale": true, "format": true, "fps": true, "setsar": true}}
	for _, tc := range []struct {
		codec   string
		depth   int
		threads int
		want    []string
	}{
		{"h264", 8, 0, []string{"-threads 2"}},
		{"h264", 8, 8, []string{"-threads 8"}},
		{"hevc", 10, 0, []string{"-threads 2", "pools=2:frame-threads=2:"}},
		{"hevc", 10, 12, []string{"-threads 12", "pools=12:open-gop"}},
		{"av1", 10, 0, []string{"-threads 2", "lp=2"}},
		{"av1", 10, 6, []string{"-threads 6", "lp=6"}},
	} {
		graph, err := BuildConversion(ConversionRequest{ConvertVideo: true, Toolchain: facts, SoftwareThreads: tc.threads,
			Target: &ConversionTarget{VideoCodec: tc.codec, BitDepth: tc.depth, Width: 1920, Height: 1080, FrameRate: 24, Packaging: "fmp4_hls"}})
		if err != nil {
			t.Fatalf("%s with %d threads: %v", tc.codec, tc.threads, err)
		}
		args := strings.Join(graph.Video, " ")
		for _, want := range tc.want {
			if !strings.Contains(args, want) {
				t.Fatalf("%s with %d threads: no %q in %s", tc.codec, tc.threads, want, args)
			}
		}
	}
}
