package mediaengine_test

import (
	"context"
	"io"
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

// With the owner's "Convert when needed" on Never, a play that needs converting is refused in
// plain words, and the same film played as the file it is still plays. Saving the setting
// takes effect on the next play, with no restart. And a device whose stored capability
// document this server cannot read is planned for as one that said nothing: it still plays.
func TestConversionOffRefusesOnlyWhatNeedsConverting(t *testing.T) {
	ffmpeg, ffprobe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	h, d := newHouse(t, ffmpeg, ffprobe)
	ctx := context.Background()
	library := filepath.Join(t.TempDir(), "Films")
	film := filepath.Join(library, "Plain (2020)", "Plain (2020).mp4")
	if err := os.MkdirAll(filepath.Dir(film), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=24",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "4", "-pix_fmt", "yuv420p", "-ac", "2", "-c:v", "libx264", "-preset", "ultrafast",
		"-c:a", "aac", "-movflags", "+faststart", film).CombinedOutput(); err != nil {
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
	item := page.Items[0].ID
	lower := api.PlaybackStart{ItemID: item, Quality: &playback.Quality{Mode: playback.QualityFixed, ID: "240p"}}
	start := func(body api.PlaybackStart) (int, string) {
		w := h.do("POST", "/v1/playback/sessions", body)
		raw, _ := io.ReadAll(w.Body)
		w.Body.Close()
		return w.StatusCode, string(raw)
	}

	// Nothing saved: a lower quality is converted, as before there were settings.
	if code, body := start(lower); code != 201 && code != 202 {
		t.Fatalf("with nothing saved a lower quality answered %d: %s", code, body)
	}
	h.call("PUT", "/v1/admin/settings/streaming", map[string]any{"convertWhenNeeded": "never"}, nil, 200)
	if code, body := start(lower); code != 403 || !strings.Contains(body, `"conversion_off"`) {
		t.Fatalf("with converting off a lower quality answered %d: %s", code, body)
	}
	var session api.PlaybackSession
	h.call("POST", "/v1/playback/sessions", api.PlaybackStart{ItemID: item}, &session, 201)
	if session.Presentation.Mode != "direct" {
		t.Fatalf("with converting off the film itself should still play: %+v", session.Presentation)
	}

	// The owner's own conversion folder is put in force when it is saved, with no restart: the
	// server makes its own folder inside it.
	chosen := t.TempDir()
	h.call("PUT", "/v1/admin/settings/streaming", map[string]any{"conversionFolder": chosen}, nil, 200)
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if _, err := os.Stat(filepath.Join(chosen, "portico-conversions")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the saved conversion folder was not put in force")
		}
	}

	// A program that is not there is not put in force, and the owner is told on the settings
	// page itself: the document reads back with one plain line. Taking it out clears the line.
	problem := func() string {
		var document api.SettingsDocument
		h.call("GET", "/v1/admin/settings/streaming", nil, &document, 200)
		return document.Problem
	}
	awaits := func(what string, holds func(string) bool) {
		t.Helper()
		for deadline := time.Now().Add(20 * time.Second); !holds(problem()); time.Sleep(100 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("%s: the settings document says %q", what, problem())
			}
		}
	}
	awaits("a folder that works", func(line string) bool { return line == "" })
	h.call("PUT", "/v1/admin/settings/streaming", map[string]any{"ffmpegProgram": "/nowhere/ffmpeg"}, nil, 200)
	awaits("a program that is not there", func(line string) bool { return strings.Contains(line, "FFmpeg was not found at /nowhere/ffmpeg") })
	h.call("PUT", "/v1/admin/settings/streaming", map[string]any{"ffmpegProgram": ""}, nil, 200)
	awaits("the program taken out again", func(line string) bool { return line == "" })

	// The device's stored document becomes one this server cannot read (a version it does
	// not know). The film still plays, planned for a device that said nothing.
	if err := d.PutDeviceCapabilities(ctx, 1, h.device, `{"version":99,"something":"new"}`, time.Now()); err != nil {
		t.Fatal(err)
	}
	h.call("POST", "/v1/playback/sessions", api.PlaybackStart{ItemID: item}, &session, 201)
	if session.Presentation.Mode != "direct" {
		t.Fatalf("a device whose document cannot be read should be planned from the baseline: %+v", session.Presentation)
	}
}
