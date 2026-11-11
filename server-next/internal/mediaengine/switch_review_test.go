package mediaengine_test

// Review of #2394 (must-change items). Copy into server-next/internal/mediaengine/.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/api"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

func reviewHouse(t *testing.T) (*house, string) {
	ffmpeg, ffprobe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	h, d := newHouse(t, ffmpeg, ffprobe)
	ctx := context.Background()
	library := filepath.Join(t.TempDir(), "Films")
	film := filepath.Join(library, "Moved (2021)", "Moved (2021).mp4")
	if err := os.MkdirAll(filepath.Dir(film), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=24", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
		"-t", "12", "-pix_fmt", "yuv420p", "-g", "48", "-ac", "2", "-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", film).CombinedOutput(); err != nil {
		t.Fatalf("making the film: %v\n%s", err, out)
	}
	lib, err := d.CreateLibrary(ctx, "Films", db.LibraryMovies, "en", []string{library})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&scan.Scanner{DB: d, Prober: scan.FileProber{}}).Scan(ctx, lib); err != nil {
		t.Fatal(err)
	}
	var page api.ItemPage
	h.call("GET", "/v1/items?library="+lib.PublicID+"&kind=movie", nil, &page, 200)
	return h, page.Items[0].ID
}

// The owner tries a folder and changes their mind while a film plays. Switching back to
// the folder the film is still converting into must not empty it under the film.
func TestSwitchingBackDoesNotEmptyAFolderStillPlayedFrom(t *testing.T) {
	h, item := reviewHouse(t)
	first := filepath.Join(h.data, "transcodes", "1")
	var session api.PlaybackSession
	h.call("POST", "/v1/playback/sessions", api.PlaybackStart{ItemID: item, Quality: &playback.Quality{Mode: playback.QualityFixed, ID: "240p"}}, &session, 201)
	url := session.Presentation.URL
	if code, _, _ := h.media(url); code != 200 {
		t.Fatalf("the conversion answered %d", code)
	}
	if err := h.engine.SetConversion(t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}
	if err := h.engine.SetConversion("", ""); err != nil { // back to what the server started with
		t.Fatal(err)
	}
	list, _ := os.ReadDir(first)
	if code, _, _ := h.media(url); code != 200 || len(list) == 0 {
		t.Fatalf("after switching away and back, the film still playing answered %d with %d entries in its folder", code, len(list))
	}
}

// A "portico-conversions" that is a link (an owner who pointed it at a bigger disk, say)
// must not have the link's target emptied.
func TestALinkedConversionFolderIsNotEmptiedThroughTheLink(t *testing.T) {
	h, _ := reviewHouse(t)
	chosen, elsewhere := t.TempDir(), t.TempDir()
	keep := filepath.Join(elsewhere, "the owner's other disk")
	if err := os.WriteFile(keep, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(chosen, "portico-conversions")); err != nil {
		t.Fatal(err)
	}
	_ = h.engine.SetConversion(chosen, "") // refused or accepted: either way nothing of the owner's goes
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("a file behind the linked conversion folder was deleted: %v", err)
	}
}

// A program that is there but is not a working ffmpeg (here ffprobe, picked by mistake)
// is refused, and the old one stays in force.
func TestAProgramThatIsNotFFmpegIsRefused(t *testing.T) {
	h, _ := reviewHouse(t)
	if err := h.engine.SetConversion("", decodertest.QualifiedFFprobe(t)); err == nil {
		t.Fatal("ffprobe was accepted as the FFmpeg program; every new conversion would be refused")
	}
}
