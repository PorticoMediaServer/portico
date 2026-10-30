package decoder

import (
	"bytes"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/audiofacts"
)

// Spec §18.1 converted mode is exact: the recipe cuts the source's raw decoded
// frames at the facts' trim, resamples, and pads or cuts to exactly the plan's
// frame count. The output, measured back, has exactly those frames: FLAC with
// no trim, Opus with its 312-frame pre-skip. A seek (fromFrame) is the same
// recipe from a later source frame. (Unconfined FFmpeg here; the confinement is
// the same as every analysis run's.)
func TestAudioConversionIsExact(t *testing.T) {
	ffmpeg, e1 := exec.LookPath("ffmpeg")
	ffprobe, e2 := exec.LookPath("ffprobe")
	if e1 != nil || e2 != nil {
		t.Skip("ffmpeg/ffprobe not installed")
	}
	dir := t.TempDir()
	server := httptest.NewServer(http.FileServer(http.Dir(dir)))
	defer server.Close()
	measure := func(name string) audiofacts.Facts {
		t.Helper()
		var out, stderr bytes.Buffer
		cmd := exec.Command(ffprobe, audiofacts.Args(server.URL+"/"+name)...)
		cmd.Stdout, cmd.Stderr = &out, &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("%s: %v %s", name, err, stderr.String())
		}
		f, err := audiofacts.Parse(&out)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return f
	}
	// Sources with priming and padding: LAME MP3 at 44.1 kHz and Opus at 48 kHz.
	for _, src := range []struct {
		name, codec string
		rate        int
	}{{"src.mp3", "libmp3lame", 44100}, {"src.opus", "libopus", 48000}} {
		frames := int64(3*src.rate + 777)
		expr := fmt.Sprintf("0.5*sin(2*PI*441*n/%d)", src.rate)
		if out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", fmt.Sprintf("aevalsrc=%s|%s:s=%d:n=4096", expr, expr, src.rate), "-af", fmt.Sprintf("atrim=end_sample=%d", frames), "-c:a", src.codec, filepath.Join(dir, src.name)).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v %s", src.name, err, out)
		}
		facts := measure(src.name)
		if facts.DurationFrames != frames {
			t.Fatalf("%s facts %s", src.name, facts)
		}
		for _, target := range []struct {
			kind          string
			rate, depth   int
			bitrate       int
			from          int64
			wantStartTrim int64
		}{{"audio_flac", src.rate, 16, 0, 0, 0}, {"audio_flac", 48000, 24, 0, 1000, 0}, {"audio_opus", 48000, 0, 96000, 0, 312}, {"audio_opus", 48000, 0, 64000, 4800, 312}} {
			out := int64(float64(frames)*float64(target.rate)/float64(src.rate) + 0.5)
			skip := int64(float64(target.from)*float64(src.rate)/float64(target.rate) + 0.5)
			spec := AnalysisSpec{Kind: target.kind, MaxDurationUS: 10_000_000, AudioStart: facts.StartFrames + skip, AudioFrames: facts.DurationFrames - skip, AudioOut: out - target.from, AudioRate: target.rate, AudioChannels: 2, AudioBitDepth: target.depth, AudioBitrate: target.bitrate}
			args, err := AnalysisArgs(server.URL+"/"+src.name, spec)
			if err != nil {
				t.Fatal(err)
			}
			name := fmt.Sprintf("%s-%s-%d-%d.out", src.name, target.kind, target.rate, target.from)
			file, err := os.Create(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			cmd := exec.Command(ffmpeg, args...)
			cmd.Stdout, cmd.Stderr = file, &stderr
			err = cmd.Run()
			file.Close()
			if err != nil {
				t.Fatalf("%s: %v %s", name, err, stderr.String())
			}
			got := measure(name)
			if got.DurationFrames != out-target.from || got.StartFrames != target.wantStartTrim || got.SampleRate != target.rate || got.Channels != 2 {
				t.Errorf("%s: %s, want %d frames trimmed from %d", name, got, out-target.from, target.wantStartTrim)
			}
			t.Logf("%s: %s", name, got)
		}
	}
}

// B-10.11: the AAC target (Apple's own renderer under a bitrate limit) is
// AAC-LC in ADTS, which states no priming of its own: the plan's trim
// (playback.AACPriming, 1024) must be exactly where the track starts in the
// decoded stream, and every frame of the track must follow it.
func TestAudioConversionAACStartsAfterItsPriming(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	server := httptest.NewServer(http.FileServer(http.Dir(dir)))
	defer server.Close()
	// A FLAC source: 3 s of silence-free tone, so the priming is the only quiet part.
	const rate, frames = 44100, int64(3*44100 + 333)
	expr := fmt.Sprintf("0.5*sin(2*PI*441*n/%d)+0.3", rate)
	if out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", fmt.Sprintf("aevalsrc=%s|%s:s=%d:n=4096", expr, expr, rate), "-af", fmt.Sprintf("atrim=end_sample=%d", frames), "-c:a", "flac", filepath.Join(dir, "src.flac")).CombinedOutput(); err != nil {
		t.Fatalf("source: %v %s", err, out)
	}
	for _, bad := range []AnalysisSpec{
		{Kind: "audio_aac", MaxDurationUS: 10_000_000, AudioFrames: frames, AudioOut: frames, AudioRate: 12345, AudioChannels: 2, AudioBitrate: 128000},
		{Kind: "audio_aac", MaxDurationUS: 10_000_000, AudioFrames: frames, AudioOut: frames, AudioRate: 44100, AudioChannels: 9, AudioBitrate: 128000},
		{Kind: "audio_aac", MaxDurationUS: 10_000_000, AudioFrames: frames, AudioOut: frames, AudioRate: 44100, AudioChannels: 2, AudioBitrate: 8000},
	} {
		if _, err := AnalysisArgs(server.URL+"/src.flac", bad); err == nil {
			t.Fatalf("an AAC recipe outside its bounds was accepted: %+v", bad)
		}
	}
	spec := AnalysisSpec{Kind: "audio_aac", MaxDurationUS: 10_000_000, AudioFrames: frames, AudioOut: frames, AudioRate: rate, AudioChannels: 2, AudioBitrate: 128000}
	args, err := AnalysisArgs(server.URL+"/src.flac", spec)
	if err != nil {
		t.Fatal(err)
	}
	encoded := filepath.Join(dir, "out.aac")
	file, err := os.Create(encoded)
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd := exec.Command(ffmpeg, args...)
	cmd.Stdout, cmd.Stderr = file, &stderr
	err = cmd.Run()
	file.Close()
	if err != nil {
		t.Fatalf("convert: %v %s", err, stderr.String())
	}
	// Every decoded frame, as a raw decoder (Core Audio with no priming method) gives it.
	pcm, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-flags2", "+skip_manual", "-f", "aac", "-i", encoded, "-ac", "1", "-f", "f32le", "-c:a", "pcm_f32le", "pipe:1").Output()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	samples := make([]float32, len(pcm)/4)
	for i := range samples {
		bits := uint32(pcm[4*i]) | uint32(pcm[4*i+1])<<8 | uint32(pcm[4*i+2])<<16 | uint32(pcm[4*i+3])<<24
		samples[i] = math.Float32frombits(bits)
	}
	const priming = 1024
	if int64(len(samples)) < priming+frames {
		t.Fatalf("decoded %d frames, fewer than the priming and the track (%d)", len(samples), priming+frames)
	}
	mean := func(from, to int) float64 {
		total := 0.0
		for _, v := range samples[from:to] {
			total += float64(v)
		}
		return total / float64(to-from)
	}
	// The tone rides on +0.3: the priming is near 0, the track near 0.3 from its first frames.
	if before, after := mean(0, priming-128), mean(priming+128, priming+2048); before > 0.05 || after < 0.25 {
		t.Fatalf("the track does not start at the priming: before %.3f after %.3f", before, after)
	}
	t.Logf("decoded %d frames for %d (+%d priming)", len(samples), frames, priming)
}
