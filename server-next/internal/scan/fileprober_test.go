package scan_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediatools"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

// The test executable answers the sandbox's helper request the way the server binary does.
func TestMain(m *testing.M) {
	for _, helper := range []func([]string) (bool, error){decoder.RunHelper, mediaexec.RunHelper} {
		if handled, err := helper(os.Args[1:]); handled {
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			os.Exit(0)
		}
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
		"sine=frequency=440:sample_rate=48000", "-t", "2", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-metadata:s:a:0", "language=eng",
		"-metadata", "title=A Small Film", film).CombinedOutput()
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
	if facts.Title != "A Small Film" {
		t.Fatalf("the title written inside the file was read as %q", facts.Title)
	}
	video, audio := facts.Streams[0], facts.Streams[1]
	if video.Kind != db.StreamVideo || video.Codec != "h264" || video.Width != 320 || video.Height != 180 || video.HDR != "" {
		t.Fatalf("video = %+v", video)
	}
	if audio.Kind != db.StreamAudio || audio.Codec != "aac" || audio.Language != "eng" || audio.SampleRate != 48000 {
		t.Fatalf("audio = %+v", audio)
	}

	// A film has no exact audio measurement; a song gets one when the scan reads it, where
	// the scan's worker runs: its real length in frames and what to trim from its start.
	if facts.AudioExact != "" {
		t.Fatalf("a film was measured as music: %s", facts.AudioExact)
	}
	song := filepath.Join(dir, "song.mp3")
	if out, err := exec.Command(ffmpeg, "-v", "error", "-y", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100", "-t", "3",
		"-c:a", "libmp3lame", song).CombinedOutput(); err != nil {
		decodertest.Unavailable(t, fmt.Sprintf("fixture encode unavailable: %v %s", err, out))
	}
	if facts, err = (scan.FileProber{}).Probe(ctx, song); err != nil {
		t.Fatal(err)
	}
	var exact struct {
		Codec                       string
		SampleRate                  int
		DurationFrames, StartFrames int64
	}
	if runtime.GOOS == "linux" {
		if err := json.Unmarshal([]byte(facts.AudioExact), &exact); err != nil || exact.Codec != "mp3" || exact.SampleRate != 44100 ||
			exact.DurationFrames < 44100*2 || exact.DurationFrames > 44100*4 || exact.StartFrames == 0 {
			t.Fatalf("the song's exact measurement is %q (%v)", facts.AudioExact, err)
		}
	}

	text := filepath.Join(dir, "notes.mkv")
	if err := os.WriteFile(text, []byte("not a film"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := (scan.FileProber{}).Probe(ctx, text); err != scan.ErrDamaged && err != scan.ErrNotMedia {
		t.Fatalf("a text file: %v", err)
	}
}

// A film kept as a disc folder is one title. Without the disc reader installed it is listed
// under its folder's name and tried again next scan: it is not put in Files skipped as a
// damaged file, which is what handing its index to the media tool did.
func TestADiscFolderIsNotADamagedFile(t *testing.T) {
	if _, err := exec.LookPath(mediatools.Resolve("portico-disc")); err == nil {
		t.Skip("the disc reader is installed here: a made-up disc would rightly be refused by it")
	}
	f := newFixture(t, db.LibraryMovies)
	f.write("Heat (1995)/BDMV/index.bdmv")
	f.write("Heat (1995)/BDMV/STREAM/00001.m2ts")
	f.s.Prober = scan.FileProber{}
	f.scan()
	if _, ok := f.titles(db.KindMovie)["Heat"]; !ok || len(f.titles(db.KindMovie)) != 1 {
		t.Fatalf("the disc should be the one film Heat; films: %v", keys(f.titles(db.KindMovie)))
	}
	skipped, total, err := f.d.SkippedFiles(ctx, f.lib.ID, 0, 10)
	if err != nil || total != 0 {
		t.Fatalf("Files skipped: %+v, %v", skipped, err)
	}
}
