package playback

import (
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
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
	if !strings.Contains(manifest, "#EXT-X-TARGETDURATION:8\n") || !strings.Contains(manifest, "#EXTINF:4.000000,\nsegment-000000.ts\n#EXTINF:8.000000,\nsegment-000001.ts") || strings.Count(manifest, "#EXTINF") != 7 || !strings.HasSuffix(manifest, "#EXT-X-ENDLIST\n") {
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

// copyFixture is a session whose picture is copied from a source with keyframes
// that do not sit on the six second grid.
func copyFixture(t *testing.T, gop int, seconds int) (context.Context, *Service, *HLS, Session, string, string) {
	t.Helper()
	binary := decodertest.QualifiedFFmpeg(t)
	decodertest.QualifiedFFprobe(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	root := t.TempDir()
	input := filepath.Join(root, "fixture.mkv")
	out, err := exec.CommandContext(ctx, binary, "-nostdin", "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=160x90:rate=24", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", strconv.Itoa(seconds), "-c:v", "libx264", "-preset", "ultrafast", "-g", strconv.Itoa(gop), "-keyint_min", strconv.Itoa(gop), "-sc_threshold", "0", "-pix_fmt", "yuv420p", "-c:a", "aac", input).CombinedOutput()
	if err != nil {
		cancel()
		t.Fatalf("fixture: %v %s", err, out)
	}
	facts, err := (assets.Probe{}).Inspect(ctx, input)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	info, _ := os.Stat(input)
	db, err := persistence.Open(filepath.Join(root, "db"))
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	mustAudioExec(t, db, `INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','owner',X'00','profile',1)`)
	insertFixtureSession(t, db, "login", "owner", "profile", "owner", "2099-01-01T00:00:00Z")
	_, item, _ := catalogFixture(t, db, "lib", root, compactcatalog.Movie, "item", "Movie", compactcatalog.Asset{Path: input, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(), Container: facts.Container, VideoCodec: facts.VideoCodec, AudioCodec: facts.AudioCodec, Width: facts.Width, Height: facts.Height, Duration: facts.Duration})
	h, err := NewHLS(ctx, db, filepath.Join(root, "hls"), binary)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	cfg := DefaultDeliveryConfiguration()
	cfg.PlayedRetentionSeconds = 0
	h.ConfigureSettings(fixedSettings{cfg})
	s := New(db)
	s.ConfigureHLS(h)
	s.ConfigureDelivery(fixedSettings{cfg}, nil, nil, "")
	p := identity.Principal{Hash: "login", Viewer: identity.Viewer{AccountID: "owner", ProfileID: "profile", Authority: "local", Role: "owner"}, Epoch: 1}
	session, err := s.Create(p, item, "automatic", "copy")
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			h.mu.Lock()
			n := len(h.active)
			h.mu.Unlock()
			if n == 0 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		db.Close()
	})
	return ctx, s, h, session, strings.TrimSuffix(strings.TrimPrefix(session.StreamURL, "/v1/media/"), "/master.m3u8"), input
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

// A copied picture is cut on the source's keyframes, the playlist says so, and a
// converter restarted in the middle produces the same segments as one that ran
// from the start. With the fixed grid none of the three was true.
func TestCopySessionPublishesAndFollowsTheSourceTimeline(t *testing.T) {
	ctx, s, h, session, grant, input := copyFixture(t, 96, 40)
	if session.Mode != "hls" {
		t.Fatal(session.Mode)
	}
	if err := s.Ready(ctx, session); err != nil {
		t.Fatal(err)
	}
	manifestPath, err := s.HLSFileContext(ctx, grant, "master.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(manifestPath)
	manifest := string(raw)
	// Keyframes every four seconds against a six second grid: cuts alternate
	// four and eight seconds apart, so a fixed grid cannot pass for them.
	boundaries := checkCopyPlaylist(t, manifest, input)
	if strings.Count(manifest, "#EXTINF") != 7 || !strings.Contains(manifest, "#EXTINF:4.0") || !strings.Contains(manifest, "#EXTINF:8.0") {
		t.Fatal("the fixture's keyframes did not produce four and eight second cuts:\n" + manifest)
	}
	last := len(boundaries) - 2
	check := func(index int) {
		t.Helper()
		path, err := s.HLSFileContext(ctx, grant, hlsSegmentFile(index))
		if err != nil {
			t.Fatal(index, err)
		}
		first, last := videoSpan(t, path)
		if math.Abs(first-boundaries[index]) > 0.06 || last >= boundaries[index+1] {
			t.Fatalf("segment %d spans %.3f–%.3f, the playlist says %.0f–%.0f", index, first, last, boundaries[index], boundaries[index+1])
		}
	}
	// From the start, in order.
	for i := 0; i < 3; i++ {
		check(i)
	}
	// Wait for that window to finish, then discard what it made past segment 2 so
	// the next requests are genuinely served by restarted converters.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		n := len(h.active)
		h.mu.Unlock()
		if n == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	for i := 3; i <= last; i++ {
		_ = os.Remove(filepath.Join(filepath.Dir(manifestPath), hlsSegmentFile(i)))
	}
	// A seek to the end, then back to the middle: each window starts somewhere
	// else and must cut the same segments.
	check(last)
	check(3)
	check(4)
	if _, err = s.HLSFileContext(ctx, grant, hlsSegmentFile(last+1)); err == nil {
		t.Fatal("a segment past the end of the source timeline was accepted")
	}
	// No half-written segment is ever visible: staging directories are private.
	entries, _ := os.ReadDir(filepath.Dir(manifestPath))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if _, ok := hlsSegmentIndex(entry.Name()); !ok && entry.Name() != "master.m3u8" && entry.Name() != "generation" {
			t.Fatal("unexpected artifact in the session directory:", entry.Name())
		}
	}
}

// Keyframes further apart than the grid: fewer, longer segments, still exact.
func TestCopySessionWithLongGroupsOfPictures(t *testing.T) {
	ctx, s, _, session, grant, input := copyFixture(t, 240, 35)
	if err := s.Ready(ctx, session); err != nil {
		t.Fatal(err)
	}
	manifestPath, _ := s.HLSFileContext(ctx, grant, "master.m3u8")
	raw, _ := os.ReadFile(manifestPath)
	// Keyframes ten seconds apart: grid lines 6→0, 12→10, 18→10, 24→20, and 30→30
	// or 20 depending on whether the file starts before its picture. Either way
	// fewer, longer segments than the grid, and none of them empty.
	boundaries := checkCopyPlaylist(t, string(raw), input)
	if n := strings.Count(string(raw), "#EXTINF"); n < 3 || n > 4 || boundaries[2] < 19.9 || boundaries[2] > 20.1 {
		t.Fatal(string(raw))
	}
	path, err := s.HLSFileContext(ctx, grant, hlsSegmentFile(2))
	if err != nil {
		t.Fatal(err)
	}
	if first, last := videoSpan(t, path); math.Abs(first-boundaries[2]) > 0.06 || last >= boundaries[3] {
		t.Fatal(first, last, boundaries)
	}
}

func TestCopyTimelineBeyondTwoHoursUsesEveryProbeBatch(t *testing.T) {
	binary := decodertest.QualifiedFFmpeg(t)
	probe := decodertest.QualifiedFFprobe(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "two-hours.mp4")
	if out, e := exec.CommandContext(ctx, binary, "-v", "error", "-f", "lavfi", "-i", "color=s=64x36:r=1", "-t", "7300", "-c:v", "libx264", "-preset", "ultrafast", "-g", "5", "-keyint_min", "5", "-sc_threshold", "0", path).CombinedOutput(); e != nil {
		t.Fatalf("%v %s", e, out)
	}
	timeline, e := probeCopyTimeline(ctx, localTimelineProbe(probe, &assets.PlaybackInput{Argument: path}), localPacketTimelineProbe(probe, &assets.PlaybackInput{Argument: path}), 7300)
	if e != nil {
		t.Fatal(e)
	}
	if timeline.count() <= 1000 {
		t.Fatalf("second batch lost: %d", timeline.count())
	}
	for _, at := range []float64{5994, 6000, 6006, 7200, 7296} {
		i := timeline.indexAt(at)
		start := timeline.Boundaries[i]
		if start > at || at-start > 10 {
			t.Fatalf("clock %.0f maps to %.0f", at, start)
		}
	}
	if timeline.Boundaries[len(timeline.Boundaries)-1] != 7300 {
		t.Fatal("end lost")
	}
}
