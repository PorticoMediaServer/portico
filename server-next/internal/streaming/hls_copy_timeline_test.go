package streaming

import (
	"math"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/decodertest"
)

func TestBuildCopyTimelineFollowsKeyframes(t *testing.T) {
	// Keyframes every four seconds in a forty second title: the keyframe at or
	// before each six second line.
	timeline, err := buildCopyTimeline(40, []float64{4, 12, 16, 24, 28, 36}, []bool{true, true, true, true, true, true})
	if err != nil {
		t.Fatal(err)
	}
	want := []float64{0, 4, 12, 16, 24, 28, 36, 40}
	if len(timeline.Boundaries) != len(want) {
		t.Fatal(timeline.Boundaries)
	}
	for i := range want {
		if math.Abs(timeline.Boundaries[i]-want[i]) > 1e-9 {
			t.Fatal(timeline.Boundaries)
		}
	}
	if timeline.indexAt(0) != 0 || timeline.indexAt(3.9) != 0 || timeline.indexAt(4) != 1 || timeline.indexAt(27) != 4 || timeline.indexAt(39.9) != 6 || timeline.indexAt(500) != 6 {
		t.Fatal("position to segment mapping is wrong")
	}
	manifest := string(copyTimelineManifest(timeline))
	if !strings.Contains(manifest, "#EXT-X-TARGETDURATION:8\n") || !strings.Contains(manifest, "#EXTINF:4.000000,\nsegment-000000.ts\n#EXTINF:8.000000,\nsegment-000001.ts") ||
		strings.Count(manifest, "#EXTINF") != 7 ||
		!strings.HasSuffix(manifest, "#EXT-X-ENDLIST\n") {
		t.Fatal(manifest)
	}

	// Ten second groups of pictures: two grid lines share a keyframe and the
	// playlist simply has fewer, longer segments.
	timeline, err = buildCopyTimeline(40, []float64{0, 10, 10, 20, 30, 30}, []bool{true, true, true, true, true, true})
	if err != nil || len(timeline.Boundaries) != 5 {
		t.Fatal(timeline, err)
	}
	// The picture starts a few milliseconds after the file (audio priming): the
	// first keyframe is where segment zero's picture begins, not a cut of its own.
	timeline, err = buildCopyTimeline(35, []float64{0.021, 10.021, 10.021, 20.021, 20.021}, []bool{true, true, true, true, true})
	if err != nil || len(timeline.Boundaries) != 4 || timeline.Boundaries[1] != 10.021 {
		t.Fatal(timeline, err)
	}
	// Anything doubtful refuses the whole timeline.
	for name, c := range map[string]struct {
		k  []float64
		ok []bool
	}{
		"not a keyframe":      {[]float64{4, 12}, []bool{true, false}},
		"after its grid line": {[]float64{7, 12}, []bool{true, true}},
		"backwards":           {[]float64{6, 4}, []bool{true, true}},
		"backwards past fold": {[]float64{0.5, 0.3}, []bool{true, true}},
		"sparse keyframes":    {[]float64{0, 0, 0, 0, 0, 0}, []bool{true, true, true, true, true, true}},
	} {
		if _, err := buildCopyTimeline(40, c.k, c.ok); err == nil {
			t.Fatal("accepted:", name)
		}
	}
}

// manifestBoundaries reads a copy playlist back into its cut list.
func manifestBoundaries(t *testing.T, manifest string) []float64 {
	t.Helper()
	boundaries := []float64{0}
	for _, line := range strings.Split(manifest, "\n") {
		value, ok := strings.CutPrefix(line, "#EXTINF:")
		if !ok {
			continue
		}
		d, err := strconv.ParseFloat(strings.TrimSuffix(value, ","), 64)
		if err != nil {
			t.Fatal(manifest)
		}
		boundaries = append(boundaries, boundaries[len(boundaries)-1]+d)
	}
	return boundaries
}

// sourceCutList is the cut list a copy session must publish for a fixture,
// worked out independently of the product's seek-based index: every video
// keyframe is listed, then each six second line takes the keyframe at or before
// it, with neighbours under a second apart folded together. Times are measured
// from the container's start, as the product measures them. The exact keyframe
// times differ between FFmpeg versions (whether audio priming moves the start),
// so the test derives them from the fixture rather than hard-coding them.
func sourceCutList(t *testing.T, input string) []float64 {
	t.Helper()
	probe := decodertest.QualifiedFFprobe(t)
	out, err := exec.Command(probe, "-v", "error", "-show_entries", "format=start_time", "-of", "csv=p=0", input).Output()
	if err != nil {
		t.Fatal(err)
	}
	durationOutput, err := exec.Command(probe, "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", input).Output()
	if err != nil {
		t.Fatal(err)
	}
	duration, err := strconv.ParseFloat(strings.TrimSpace(string(durationOutput)), 64)
	if err != nil || !(duration > 0) {
		t.Fatal("invalid fixture duration", string(durationOutput), err)
	}
	start := 0.0
	if text := strings.TrimSpace(string(out)); text != "" && text != "N/A" {
		if start, err = strconv.ParseFloat(text, 64); err != nil {
			t.Fatal(text)
		}
	}
	out, err = exec.Command(probe, "-v", "error", "-select_streams", "v:0", "-show_entries", "packet=pts_time,flags", "-of", "csv=p=0", input).Output()
	if err != nil {
		t.Fatal(err)
	}
	var keyframes []float64
	for _, row := range strings.Fields(string(out)) {
		stamp, flags, _ := strings.Cut(row, ",")
		if v, err := strconv.ParseFloat(stamp, 64); err == nil && strings.HasPrefix(flags, "K") {
			keyframes = append(keyframes, v-start)
		}
	}
	sort.Float64s(keyframes)
	boundaries := []float64{0}
	for k := 1; float64(k)*HLSSegmentSeconds < duration; k++ {
		line := float64(k) * HLSSegmentSeconds
		at := math.NaN()
		for _, v := range keyframes {
			if v <= line+1e-6 {
				at = v
			}
		}
		if math.IsNaN(at) {
			t.Fatal("fixture has no keyframe at or before", line)
		}
		if last := boundaries[len(boundaries)-1]; at < last+1 || at >= duration-1 {
			continue
		}
		boundaries = append(boundaries, at)
	}
	return append(boundaries, duration)
}

// checkCopyPlaylist asserts that a copy playlist states exactly the source's
// own cut list, with no segment shorter than a second.
func checkCopyPlaylist(t *testing.T, manifest, input string) []float64 {
	t.Helper()
	got := manifestBoundaries(t, manifest)
	if len(got) < 2 {
		t.Fatal("the playlist lists no segments:\n" + manifest)
	}
	want := sourceCutList(t, input)
	if len(got) != len(want) {
		t.Fatalf("the playlist does not describe the source's own keyframes: want cuts %v\n%s", want, manifest)
	}
	longest := 0.0
	for i := range want {
		if math.Abs(got[i]-want[i]) > 0.002 {
			t.Fatalf("the playlist does not describe the source's own keyframes: want cuts %v\n%s", want, manifest)
		}
		if i > 0 {
			d := got[i] - got[i-1]
			if d < 1-0.002 {
				t.Fatalf("segment %d is %.3fs long:\n%s", i-1, d, manifest)
			}
			longest = math.Max(longest, d)
		}
	}
	if !strings.Contains(manifest, "#EXT-X-TARGETDURATION:"+strconv.Itoa(int(math.Ceil(longest)))+"\n") {
		t.Fatal("target duration is not the longest segment rounded up:\n" + manifest)
	}
	return got
}

// videoSpan reads the first and last video presentation time in one segment.
func videoSpan(t *testing.T, path string) (float64, float64) {
	t.Helper()
	out, err := exec.Command(decodertest.QualifiedFFprobe(t), "-v", "error", "-select_streams", "v:0", "-show_entries", "packet=pts_time", "-of", "csv=p=0", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	var stamps []float64
	for _, line := range strings.Fields(string(out)) {
		if v, err := strconv.ParseFloat(strings.TrimSuffix(line, ","), 64); err == nil {
			stamps = append(stamps, v)
		}
	}
	if len(stamps) == 0 {
		t.Fatal("segment holds no video", path)
	}
	sort.Float64s(stamps)
	return stamps[0], stamps[len(stamps)-1]
}
