package audiofacts

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Canned ffprobe output: the parser's rules without FFmpeg.
func TestParseAppliesITunSMPBAndRejectsInconsistency(t *testing.T) {
	out := func(itun string) string {
		var b strings.Builder
		b.WriteString("packet|duration=1024|side_datum/skip_samples:skip_samples=2112|side_datum/skip_samples:discard_padding=0\n")
		for i := 0; i < 218; i++ {
			b.WriteString("frame|nb_samples=1024\n")
			if i > 0 {
				b.WriteString("packet|duration=1024\n")
			}
		}
		b.WriteString(`stream|codec_name=aac|sample_rate=44100|channels=2|bits_per_sample=0|bits_per_raw_sample=N/A|extradata=\n00000000: 1210                                     ..\n|disposition:default=1` + "\n")
		b.WriteString("format|format_name=mov,mp4,m4a,3gp,3g2,mj2|tag:iTunSMPB=" + itun + "\n")
		return b.String()
	}
	f, err := Parse(strings.NewReader(out(" 00000000 00000840 00000010 0000000000035FB0 00000000")))
	if err != nil {
		t.Fatal(err)
	}
	if f.RawFrames != 223232 || f.StartFrames != 2112 || f.DurationFrames != 221104 || f.EndFrames != 16 || f.TrimSource != "itunsmpb" || f.Container != "mp4" || f.DecoderConfig != "EhA=" {
		t.Fatalf("iTunSMPB: %s config %q", f, f.DecoderConfig)
	}
	// An iTunSMPB total beyond the decoded stream is not believed: FFmpeg's trims stand.
	f, err = Parse(strings.NewReader(out(" 00000000 00000840 00000010 00000000000FFFFF 00000000")))
	if err != nil || f.TrimSource != "measured" || f.StartFrames != 2112 || f.DurationFrames != 223232-2112 {
		t.Fatalf("inconsistent iTunSMPB: %s %v", f, err)
	}
	if _, err = Parse(strings.NewReader("format|format_name=mp3\n")); err != ErrNoAudio {
		t.Fatalf("no stream: %v", err)
	}
}

// Real encodes (FFmpeg on the runner; skipped where it isn't installed): the
// frame count is known by construction, so the measurement must find exactly it,
// through the same arguments production uses (an HTTP input).
func TestMeasuredFactsMatchConstruction(t *testing.T) {
	ffmpeg, e1 := exec.LookPath("ffmpeg")
	ffprobe, e2 := exec.LookPath("ffprobe")
	if e1 != nil || e2 != nil {
		t.Skip("ffmpeg/ffprobe not installed")
	}
	dir := t.TempDir()
	server := httptest.NewServer(http.FileServer(http.Dir(dir)))
	defer server.Close()
	type want struct {
		name, kind      string
		rate            int
		frames          int64
		container, trim string
		start           int64
	}
	cases := []want{
		{"lame.mp3", "mp3", 44100, 221104, "mp3", "lame", 1105},
		{"opus.opus", "opus", 48000, 240658, "ogg", "opus-preskip", 312},
		{"flac16.flac", "flac", 44100, 221104, "flac", "flac", 0},
		{"flac24.flac", "flac24", 96000, 481315, "flac", "flac", 0},
		{"pcm.wav", "wav", 44100, 221104, "wav", "none", 0},
		{"itunes.m4a", "aac", 44100, 221104, "mp4", "itunsmpb", 1024},
		{"album-07.mp3", "mp3", 44100, 110000 + 6*7919, "mp3", "lame", 1105},
	}
	for _, c := range cases {
		expr := "0.5*sin(2*PI*441*n/" + fmt.Sprint(c.rate) + ")"
		args := []string{"-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", fmt.Sprintf("aevalsrc=%s|%s:s=%d:n=4096", expr, expr, c.rate), "-af", fmt.Sprintf("atrim=end_sample=%d", c.frames)}
		switch c.kind {
		case "mp3":
			args = append(args, "-c:a", "libmp3lame", "-b:a", "192k")
		case "opus":
			args = append(args, "-c:a", "libopus", "-b:a", "128k")
		case "flac":
			args = append(args, "-c:a", "flac", "-sample_fmt", "s16")
		case "flac24":
			args = append(args, "-c:a", "flac", "-sample_fmt", "s32", "-bits_per_raw_sample", "24")
		case "wav":
			args = append(args, "-c:a", "pcm_s16le")
		case "aac":
			// FFmpeg's AAC in MP4: an edit list for the priming, no end padding. The
			// iTunSMPB a real Apple encode carries is written here from the known
			// counts (FFmpeg writes delay 1024 and pads to whole frames).
			padding := (1024-(1024+c.frames)%1024)%1024 + 0
			args = append(args, "-c:a", "aac", "-b:a", "192k", "-movflags", "use_metadata_tags", "-metadata", fmt.Sprintf("iTunSMPB= 00000000 %08X %08X %016X 00000000", 1024, padding, c.frames))
		}
		path := filepath.Join(dir, c.name)
		if out, err := exec.Command(ffmpeg, append(args, path)...).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v %s", c.name, err, out)
		}
		var stdout, stderr bytes.Buffer
		cmd := exec.Command(ffprobe, Args(server.URL+"/"+c.name)...)
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("%s: ffprobe %v %s", c.name, err, stderr.String())
		}
		f, err := Parse(&stdout)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if f.DurationFrames != c.frames || f.SampleRate != c.rate || f.Container != c.container || f.TrimSource != c.trim || f.StartFrames != c.start || f.Channels != 2 {
			t.Errorf("%s: %s, want %d frames at %d from %d (%s, %s)", c.name, f, c.frames, c.rate, c.start, c.container, c.trim)
		}
		if c.kind == "flac24" && f.BitDepth != 24 || c.kind == "flac" && f.BitDepth != 16 {
			t.Errorf("%s: bit depth %d", c.name, f.BitDepth)
		}
		if (c.kind == "aac" || c.kind == "opus" || strings.HasPrefix(c.kind, "flac")) && f.DecoderConfig == "" {
			t.Errorf("%s: no decoder config", c.name)
		}
		t.Logf("%s: %s", c.name, f)
	}
	_ = os.Remove
}

func TestPaddingAcrossPacketsAndAudioBitrate(t *testing.T) {
	f, e := Parse(strings.NewReader("packet|size=100|side_datum/skip_samples:discard_padding=20\nframe|nb_samples=1000\npacket|size=100|side_datum/skip_samples:discard_padding=30\nframe|nb_samples=1000\nstream|codec_name=aac|profile=HE-AAC|sample_rate=48000|channels=2\nformat|format_name=mov\n"))
	if e != nil || f.EndFrames != 50 || f.Profile != "HE-AAC" || f.BitRate != 39384 {
		t.Fatalf("%+v %v", f, e)
	}
}

func TestMP4SampleTableTrimsFinalAACPacket(t *testing.T) {
	f, e := Parse(strings.NewReader("packet|side_datum/skip_samples:skip_samples=1024\nframe|nb_samples=1024\nframe|nb_samples=1024\nframe|nb_samples=1024\nstream|codec_name=aac|profile=LC|sample_rate=48000|channels=2|duration_ts=1800|time_base=1/48000\nformat|format_name=mov\n"))
	if e != nil || f.DurationFrames != 1800 || f.EndFrames != 248 {
		t.Fatalf("%+v %v", f, e)
	}
}
