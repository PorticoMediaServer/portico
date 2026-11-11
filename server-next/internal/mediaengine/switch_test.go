package mediaengine_test

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/api"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

// The owner changes the conversion folder while a film is being converted. The film that
// is playing carries on from the old folder; the next stream is written to the new one, in
// a folder of the server's own, and the old folder is cleared once nothing plays from it.
// A folder that cannot be used is refused and changes nothing.
func TestTheConversionFolderChangesWithoutARestart(t *testing.T) {
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

	entries := func(folder string) int {
		list, _ := os.ReadDir(folder)
		return len(list)
	}
	empties := func(folder string) {
		t.Helper()
		for deadline := time.Now().Add(20 * time.Second); entries(folder) != 0; time.Sleep(100 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("%s still holds %d entries", folder, entries(folder))
			}
		}
	}
	first, chosen := filepath.Join(h.data, "transcodes", "1"), t.TempDir()
	keep := filepath.Join(chosen, "the owner's own file")
	if err := os.WriteFile(keep, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(chosen, "portico-conversions", "2")

	var session api.PlaybackSession
	h.call("POST", "/v1/playback/sessions", api.PlaybackStart{ItemID: page.Items[0].ID, Quality: &playback.Quality{Mode: playback.QualityFixed, ID: "240p"}}, &session, 201)
	before := session.Presentation
	if code, _, _ := h.media(before.URL); code != 200 || entries(first) == 0 {
		t.Fatalf("the conversion answered %d with %d entries in its folder", code, entries(first))
	}

	// Refused, and nothing changes: not a full path, and a place no folder can be made in.
	for _, bad := range []string{"conversions", filepath.Join(keep, "inside a file")} {
		if err := h.engine.SetConversion(bad, ""); err == nil {
			t.Fatalf("%q was accepted as the conversion folder", bad)
		}
	}
	if err := h.engine.SetConversion(chosen, ""); err != nil {
		t.Fatal(err)
	}
	// What was playing still plays, from the folder it started in.
	if code, _, _ := h.media(before.URL); code != 200 || entries(first) == 0 {
		t.Fatalf("after the change the playing stream answered %d with %d entries in its folder", code, entries(first))
	}

	// The next stream of the same session is written to the new folder, and the old one is cleared.
	h.call("PATCH", "/v1/playback/sessions/"+session.ID, api.PlaybackChange{Quality: &playback.Quality{Mode: playback.QualityFixed, ID: "480p"}}, &session, 200)
	after := session.Presentation
	if after.URL == before.URL || !strings.HasSuffix(after.URL, ".m3u8") {
		t.Fatalf("a new quality should be a new stream: %+v", after)
	}
	if code, _, _ := h.media(after.URL); code != 200 || entries(second) == 0 {
		t.Fatalf("the new stream answered %d with %d entries in the new folder", code, entries(second))
	}
	if code, _, _ := h.media(before.URL); code != http.StatusNotFound {
		t.Fatalf("the replaced address answered %d", code)
	}
	empties(first)

	h.call("DELETE", "/v1/playback/sessions/"+session.ID, nil, nil, 204)
	empties(second)
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("the owner's own file in the chosen folder: %v", err)
	}
}
