package scan_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

// The test executable answers the sandbox's helper request the way the server binary does.
func TestMain(m *testing.M) {
	if handled, err := mediaexec.RunHelper(os.Args[1:]); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if _, err := mediaexec.Configure(mediaexec.Options{}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// A real film is read through a link by the real prober; a file that is not media is told
// apart from it.
func TestFileProberReadsARealFile(t *testing.T) {
	ffmpeg := decodertest.QualifiedFFmpeg(t)
	decodertest.QualifiedFFprobe(t)
	dir := t.TempDir()
	film := filepath.Join(dir, "film.mkv")
	out, err := exec.Command(ffmpeg, "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=25", "-f", "lavfi", "-i",
		"sine=frequency=440:sample_rate=48000", "-t", "2", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-metadata:s:a:0", "language=eng", film).CombinedOutput()
	if err != nil {
		decodertest.Unavailable(t, fmt.Sprintf("fixture encode unavailable: %v %s", err, out))
	}
	link := filepath.Join(dir, "link.mkv")
	if err := os.Symlink(film, link); err != nil {
		t.Fatal(err)
	}
	facts, err := scan.FileProber{}.Probe(ctx, link)
	if err != nil {
		t.Fatal(err)
	}
	if facts.DurationMS < 1900 || facts.DurationMS > 2200 || len(facts.Streams) != 2 {
		t.Fatalf("facts = %+v", facts)
	}
	video, audio := facts.Streams[0], facts.Streams[1]
	if video.Kind != db.StreamVideo || video.Codec != "h264" || video.Width != 320 || video.Height != 180 || video.HDR != "" {
		t.Fatalf("video = %+v", video)
	}
	if audio.Kind != db.StreamAudio || audio.Codec != "aac" || audio.Language != "eng" || audio.SampleRate != 48000 {
		t.Fatalf("audio = %+v", audio)
	}

	text := filepath.Join(dir, "notes.mkv")
	if err := os.WriteFile(text, []byte("not a film"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := (scan.FileProber{}).Probe(ctx, text); err != scan.ErrDamaged && err != scan.ErrNotMedia {
		t.Fatalf("a text file: %v", err)
	}
}
