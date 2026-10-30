package mediaanalysis

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/decoder"
)

// TestScanCostPerFile measures, on generated media shaped like a film, an
// episode and an album track, what a Default (Basic) scan and a Complete scan
// cost per file: the decoder runs each Complete stage makes, as the analysis
// worker makes them (through an HTTP endpoint, as the bridge serves files),
// and the work done on their output. It needs ffmpeg and takes a minute, so
// it runs only when asked:
//
//	PORTICO_SCAN_COST=1 go test ./internal/mediaanalysis -run ScanCost -v
func TestScanCostPerFile(t *testing.T) {
	if os.Getenv("PORTICO_SCAN_COST") == "" {
		t.Skip("set PORTICO_SCAN_COST=1 to measure scan cost")
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	ffprobe, _ := exec.LookPath("ffprobe")
	dir := t.TempDir()
	type sample struct {
		name, file string
		seconds    int
		args       []string
	}
	samples := []sample{
		{"film (1080p H.264, 5.1 AAC)", "film.mkv", 180, []string{"-f", "lavfi", "-i", "testsrc2=size=1920x1080:rate=24", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-filter_complex", "[1:a]pan=5.1|c0=c0|c1=c0|c2=c0|c3=c0|c4=c0|c5=c0[a]", "-map", "0:v", "-map", "[a]", "-c:v", "libx264", "-preset", "ultrafast", "-crf", "28", "-c:a", "aac", "-b:a", "384k"}},
		{"episode (720p H.264, stereo AAC)", "episode.mkv", 120, []string{"-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=24", "-f", "lavfi", "-i", "sine=frequency=330:sample_rate=48000", "-map", "0:v", "-map", "1:a", "-ac", "2", "-c:v", "libx264", "-preset", "ultrafast", "-crf", "28", "-c:a", "aac", "-b:a", "192k"}},
		{"music (FLAC, 44.1 kHz stereo)", "track.flac", 240, []string{"-f", "lavfi", "-i", "sine=frequency=220:sample_rate=44100", "-ac", "2", "-c:a", "flac"}},
	}
	server := httptest.NewServer(http.FileServer(http.Dir(dir)))
	defer server.Close()
	ctx := context.Background()
	run := func(t *testing.T, input string, spec decoder.AnalysisSpec, consume func(io.Reader) error) time.Duration {
		t.Helper()
		args, err := decoder.AnalysisArgs(input, spec)
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		cmd := exec.CommandContext(ctx, ffmpeg, args...)
		out, _ := cmd.StdoutPipe()
		var stderr strings.Builder
		cmd.Stderr = &stderr
		if err = cmd.Start(); err != nil {
			t.Fatal(err)
		}
		consumeErr := consume(bufio.NewReaderSize(out, 1<<16))
		_, _ = io.Copy(io.Discard, out)
		if err = cmd.Wait(); err != nil || consumeErr != nil {
			t.Fatalf("%s: %v %v %s", spec.Kind, err, consumeErr, stderr.String())
		}
		return time.Since(start)
	}
	drain := func(r io.Reader) error { _, e := io.Copy(io.Discard, r); return e }
	for _, s := range samples {
		path := filepath.Join(dir, s.file)
		gen := append([]string{"-hide_banner", "-v", "error", "-y"}, s.args...)
		gen = append(gen, "-t", fmt.Sprint(s.seconds), path)
		if out, err := exec.Command(ffmpeg, gen...).CombinedOutput(); err != nil {
			t.Fatalf("generate %s: %v %s", s.file, err, out)
		}
		input := server.URL + "/" + s.file
		duration := int64(s.seconds) * 1000000
		video := !strings.HasSuffix(s.file, ".flac")
		start := time.Now()
		if ffprobe != "" {
			if out, err := exec.Command(ffprobe, "-v", "error", "-show_format", "-show_streams", "-of", "json", input).CombinedOutput(); err != nil {
				t.Fatalf("probe: %v %s", err, out)
			}
		}
		basic := time.Since(start)
		var trickplay, segments, thorough time.Duration
		if video {
			trickplay = run(t, input, decoder.AnalysisSpec{Kind: "trickplay", MaxDurationUS: duration, IntervalUS: 10000000, MaxFrames: 1000}, drain)
			// Intro and credits detection reads regions, not the whole file.
			plan := planSegments("episode", SegmentModeThorough, duration, true, true)
			for _, r := range []region{plan.Opening, plan.Closing} {
				segments += run(t, input, decoder.AnalysisSpec{Kind: "segment_audio", StartUS: r.StartUS, RegionUS: r.LengthUS, MaxDurationUS: duration}, drain)
			}
			thorough = segments + run(t, input, decoder.AnalysisSpec{Kind: "segment_frames", StartUS: plan.Frames.StartUS, RegionUS: plan.Frames.LengthUS, IntervalUS: 1000000, MaxFrames: int(plan.Frames.LengthUS/1000000) + 2, MaxDurationUS: duration}, drain)
		}
		// One decode serves the waveform and the loudness (before, each decoded the file).
		var summaries map[string]any
		shared := run(t, input, decoder.AnalysisSpec{Kind: "pcm_shared", MaxDurationUS: duration}, func(r io.Reader) error {
			var e error
			summaries, e = analyzeSharedAudio(r, duration, []string{"waveform", "loudness"})
			return e
		})
		t.Logf("%s, %d s: Default %v | Complete %v (trickplay %v, shared audio %v) | segment detection standard %v, thorough %v | loudness %v",
			s.name, s.seconds, basic.Round(time.Millisecond), (trickplay + shared).Round(time.Millisecond), trickplay.Round(time.Millisecond), shared.Round(time.Millisecond), segments.Round(time.Millisecond), thorough.Round(time.Millisecond), summaries["loudness"].(map[string]any)["integratedLUFS"])
	}
}
