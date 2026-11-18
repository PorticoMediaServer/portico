package mediaengine_test

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/api"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

// A library folder reached through a link (on macOS /var and /tmp are links; on Linux, a
// folder linked to another disk) plays, and so does a film that is a link to a file kept
// outside the library: the scan lists both, so both play (package libraryfile).
func TestFilmsThroughLinksPlay(t *testing.T) {
	ffmpeg, ffprobe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	h, d := newHouse(t, ffmpeg, ffprobe)
	ctx := context.Background()
	root := t.TempDir()
	real := filepath.Join(root, "disk", "Films")
	elsewhere := filepath.Join(root, "elsewhere")
	for _, dir := range []string{real, elsewhere} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	make := func(path string) {
		t.Helper()
		if out, err := exec.Command(ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-y",
			"-f", "lavfi", "-i", "testsrc2=size=320x180:rate=24", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
			"-t", "2", "-pix_fmt", "yuv420p", "-ac", "2", "-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", "-movflags", "+faststart", path).CombinedOutput(); err != nil {
			t.Fatalf("making %s: %v\n%s", path, err, out)
		}
	}
	make(filepath.Join(real, "Linked Folder (2021).mp4"))
	make(filepath.Join(elsewhere, "kept.mp4"))
	if err := os.Symlink(filepath.Join(elsewhere, "kept.mp4"), filepath.Join(real, "Linked File (2022).mp4")); err != nil {
		t.Fatal(err)
	}
	library := filepath.Join(root, "Films")
	if err := os.Symlink(real, library); err != nil {
		t.Fatal(err)
	}
	lib, err := d.CreateLibrary(ctx, "Films", db.LibraryMovies, "en", []string{library})
	if err != nil {
		t.Fatal(err)
	}
	if stats, err := (&scan.Scanner{DB: d}).Scan(ctx, lib); err != nil || stats.ItemsNew != 2 {
		t.Fatalf("scan: %+v, %v", stats, err)
	}
	var page api.ItemPage
	h.call("GET", "/v1/items?library="+lib.PublicID+"&kind=movie", nil, &page, 200)
	if len(page.Items) != 2 {
		t.Fatalf("the library lists %+v", page.Items)
	}
	for _, item := range page.Items {
		var session api.PlaybackSession
		h.call("POST", "/v1/playback/sessions", api.PlaybackStart{ItemID: item.ID}, &session, 201)
		if code, _, body := h.media(session.Presentation.URL, "Range", "bytes=0-999"); code != http.StatusPartialContent || len(body) != 1000 {
			t.Fatalf("%s: a byte range answered %d with %d bytes", item.Title, code, len(body))
		}
		h.call("DELETE", "/v1/playback/sessions/"+session.ID, nil, nil, 204)
	}
}
