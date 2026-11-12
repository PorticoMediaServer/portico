package mediaengine_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/api"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

// With "Convert when needed" on Never, a song is not converted for an app with its own music
// engine either: a FLAC the app's engine cannot decode is refused as conversion_off, or
// played some way that converts nothing.
func TestReviewConversionOffAlsoHoldsForTheAppsOwnMusicEngine(t *testing.T) {
	ffmpeg, ffprobe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	h, d := newHouse(t, ffmpeg, ffprobe)
	ctx := context.Background()
	library := filepath.Join(t.TempDir(), "Music")
	album := filepath.Join(library, "The Band", "The Album")
	if err := os.MkdirAll(album, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100",
		"-t", "6", "-ac", "2", "-metadata", "artist=The Band", "-metadata", "album=The Album", "-metadata", "title=Second", "-c:a", "flac",
		filepath.Join(album, "02 Second.flac")).CombinedOutput(); err != nil {
		t.Fatalf("making the song: %v\n%s", err, out)
	}
	lib, err := d.CreateLibrary(ctx, "Music", db.LibraryMusic, "en", []string{library})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&scan.Scanner{DB: d, Prober: scan.FileProber{}}).Scan(ctx, lib); err != nil {
		t.Fatal(err)
	}
	var page api.ItemPage
	h.call("GET", "/v1/items?library="+lib.PublicID+"&kind=track", nil, &page, 200)
	song := page.Items[0].ID
	var document map[string]any
	profile, _ := os.ReadFile(filepath.Join("..", "streaming", "profiles", "ios.json"))
	var file struct{ Document json.RawMessage }
	if json.Unmarshal(profile, &file) != nil || json.Unmarshal(file.Document, &document) != nil {
		t.Fatal("the iPhone's device profile could not be read")
	}
	document["audioDecode"] = []map[string]any{{"codec": "mp3", "containers": []string{"mp3"}}, {"codec": "opus", "containers": []string{"ogg"}}}
	h.call("PUT", "/v1/devices/"+h.device+"/capabilities", document, nil, 204)

	// Nothing saved: once the song is measured it is converted to Opus for the app's engine.
	for deadline := time.Now().Add(60 * time.Second); ; time.Sleep(200 * time.Millisecond) {
		var session api.PlaybackSession
		h.call("POST", "/v1/playback/sessions", api.PlaybackStart{ItemID: song}, &session, 201)
		h.call("DELETE", "/v1/playback/sessions/"+session.ID, nil, nil, 204)
		if session.Presentation.Route == "converted" && len(session.Presentation.AudioRender) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the song was never planned for the music engine: %+v", session.Presentation)
		}
	}
	h.call("PUT", "/v1/admin/settings/streaming", map[string]any{"convertWhenNeeded": "never"}, nil, 200)
	w := h.do("POST", "/v1/playback/sessions", api.PlaybackStart{ItemID: song})
	raw, _ := io.ReadAll(w.Body)
	w.Body.Close()
	var session api.PlaybackSession
	_ = json.Unmarshal(raw, &session)
	if w.StatusCode == 201 && (session.Presentation.Route == "converted") {
		t.Fatalf("with converting set to Never the song was still converted: %s", raw)
	}
	t.Logf("with Never the start answered %d: %.300s", w.StatusCode, raw)
}
