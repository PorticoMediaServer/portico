package decoder

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/testtier"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/audiofacts"
)

// All bundled suite inputs are synthesised tones, not copied recordings.
func TestMusicLosslessFormatsR29(t *testing.T) {
	testtier.Media(t, "encodes and decodes actual bounded music fixtures through FFmpeg")
	ff, e := exec.LookPath("ffmpeg")
	if e != nil {
		t.Skip("ffmpeg unavailable")
	}
	probe, e := exec.LookPath("ffprobe")
	if e != nil {
		t.Skip("ffprobe unavailable")
	}
	cases := []struct {
		name, codec, format, ext string
		rate, depth              int
	}{
		{"flac32", "flac", "s32", "flac", 44100, 32},
		{"hires192", "flac", "s32", "flac", 192000, 24},
		{"aiff24", "pcm_s24be", "s32", "aiff", 96000, 24},
		{"alac24", "alac", "s32p", "m4a", 88200, 24},
		{"wavpack", "wavpack", "s32p", "wv", 44100, 24},
		{"tta", "tta", "s32", "tta", 44100, 24},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "source."+tc.ext)
			tone := fmt.Sprintf("aevalsrc=0.17*sin(2*PI*997*t)+0.03*sin(2*PI*7001*t):s=%d:d=0.1", tc.rate)
			args := []string{"-v", "error", "-f", "lavfi", "-i", tone, "-threads", "1", "-c:a", tc.codec, "-sample_fmt", tc.format, "-bits_per_raw_sample", fmt.Sprint(tc.depth), "-strict", "-2", path}
			if out, e := exec.Command(ff, args...).CombinedOutput(); e != nil {
				t.Fatalf("fixture generation: %v %s", e, out)
			}
			facts := exerciseMusicFormat(t, ff, probe, path, tc.codec, tc.rate, true)
			if facts.BitDepth != tc.depth {
				t.Fatalf("fixture intended %dbit but measured %dbit", tc.depth, facts.BitDepth)
			}
		})
	}
}

func exerciseMusicFormat(t *testing.T, ff, probe, path, codec string, rate int, lossless bool, expectedFrames ...int64) audiofacts.Facts {
	t.Helper()
	file, e := os.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	source, e := io.ReadAll(io.LimitReader(file, 16<<20+1))
	file.Close()
	if e != nil || len(source) > 16<<20 {
		t.Fatal("fixture exceeds bounded suite input", e)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "source", time.Time{}, bytes.NewReader(source))
	}))
	defer server.Close()
	raw, e := exec.Command(probe, audiofacts.Args(server.URL)...).Output()
	if e != nil {
		t.Fatal(e)
	}
	facts, e := audiofacts.Parse(bytes.NewReader(raw))
	if e == nil {
		facts, e = audiofacts.CompleteCodecHeaders(facts, source[:min(len(source), 1<<20)])
	}
	if e != nil || facts.Codec != codec || facts.SampleRate != rate {
		t.Fatal(facts, e)
	}
	if len(expectedFrames) != 0 && facts.DurationFrames != expectedFrames[0] {
		t.Fatal("audible frames differ from fixture construction", facts.DurationFrames, expectedFrames[0])
	}
	depth := 16
	if lossless && facts.BitDepth > 16 {
		depth = 24
		if facts.BitDepth > 24 {
			depth = 32
		}
	}
	args, e := AnalysisArgs(server.URL, AnalysisSpec{Kind: "audio_flac", MaxDurationUS: 1000000, AudioStart: facts.StartFrames, AudioFrames: facts.DurationFrames, AudioOut: facts.DurationFrames, AudioRate: rate, AudioSourceRate: rate, AudioChannels: facts.Channels, AudioSourceDepth: facts.BitDepth, AudioBitDepth: depth})
	if e != nil {
		t.Fatal(e)
	}
	encoded, e := exec.Command(ff, args...).Output()
	if e != nil {
		t.Fatal(e)
	}
	decode := func(input []byte) []byte {
		cmd := exec.Command(ff, "-v", "error", "-i", "pipe:0", "-map", "0:a:0", "-c:a", "pcm_s32le", "-f", "s32le", "pipe:1")
		cmd.Stdin = bytes.NewReader(input)
		out, e := cmd.Output()
		if e != nil {
			t.Fatal(e)
		}
		return out
	}
	delivered := decode(encoded)
	if len(delivered) != int(facts.DurationFrames)*facts.Channels*4 {
		t.Fatal("converted audible length changed", len(delivered), facts)
	}
	if lossless && !bytes.Equal(decode(source), delivered) {
		t.Fatal("lossless source samples changed")
	}
	sum := sha256.Sum256(source)
	t.Logf("%s %dHz %dbit %dch source sha256=%x -> FLAC %dbit, %d audible frames", codec, facts.SampleRate, facts.BitDepth, facts.Channels, sum, depth, facts.DurationFrames)
	return facts
}

// The bundled inputs are procedural tones with construction counts and tool
// provenance. Optional external recordings stay outside Git and require an
// explicit absolute-path/hash/rights manifest.
func TestMusicAPEAndMusepackFormatsR29(t *testing.T) {
	testtier.Media(t, "encodes and decodes actual bounded music fixtures through FFmpeg")
	manifest := os.Getenv("PORTICO_MUSIC_FORMAT_FIXTURES")
	bundled := manifest == ""
	if manifest == "" {
		manifest = filepath.Join("testdata", "music-formats-r29", "manifest.json")
	}
	raw, e := os.ReadFile(manifest)
	if e != nil {
		t.Fatal(e)
	}
	var fixtures []struct {
		Codec, Path, SHA256, Rights string
		SampleRate                  int
		Frames                      int64
	}
	if json.Unmarshal(raw, &fixtures) != nil || len(fixtures) > 8 {
		t.Fatal("invalid private fixture manifest")
	}
	ff, e := exec.LookPath("ffmpeg")
	if e != nil {
		t.Skip("ffmpeg unavailable: actual format decode not verified")
	}
	probe, e := exec.LookPath("ffprobe")
	if e != nil {
		t.Skip("ffprobe unavailable: actual format measurement not verified")
	}
	seen := map[string]bool{}
	for _, f := range fixtures {
		if bundled {
			f.Path, e = filepath.Abs(filepath.Join(filepath.Dir(manifest), f.Path))
			if e != nil {
				t.Fatal(e)
			}
		}
		if f.Codec != "ape" && f.Codec != "mpc7" && f.Codec != "mpc8" || !filepath.IsAbs(f.Path) || len(f.SHA256) != 64 || f.Rights != "private-user-supplied" && f.Rights != "verified-free-fixture" {
			t.Fatal("fixture provenance incomplete")
		}
		t.Run(f.Codec, func(t *testing.T) {
			file, e := os.Open(f.Path)
			if e != nil {
				t.Fatal(e)
			}
			h := sha256.New()
			_, e = io.Copy(h, file)
			file.Close()
			if e != nil || hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
				t.Fatal("fixture checksum mismatch", e)
			}
			if f.Frames <= 0 {
				t.Fatal("fixture construction frame count missing")
			}
			exerciseMusicFormat(t, ff, probe, f.Path, f.Codec, f.SampleRate, f.Codec == "ape", f.Frames)
		})
		seen[f.Codec] = true
	}
	for _, codec := range []string{"ape", "mpc7", "mpc8"} {
		if !seen[codec] {
			t.Errorf("required %s fixture missing", codec)
		}
	}
}
