package mediaengine_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/api"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

// A song played by an app with its own music engine is planned as a music stream: an MP3
// the app decodes is handed over as the file itself, with what to trim for a gapless join
// and a seek index; a FLAC it cannot decode is converted to Opus, exact to the frame. The
// render plan the app is given carries the stream's own addresses.
func TestSongsArePlannedForTheAppsOwnMusicEngine(t *testing.T) {
	ffmpeg, ffprobe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	h, d := newHouse(t, ffmpeg, ffprobe)
	ctx := context.Background()
	library := filepath.Join(t.TempDir(), "Music")
	album := filepath.Join(library, "The Band", "The Album")
	if err := os.MkdirAll(album, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, codec := range map[string][]string{"01 First.mp3": {"-c:a", "libmp3lame", "-b:a", "128k"}, "02 Second.flac": {"-c:a", "flac"}} {
		args := append([]string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100",
			"-t", "6", "-ac", "2", "-metadata", "artist=The Band", "-metadata", "album=The Album", "-metadata", "title=" + strings.TrimSuffix(name[3:], filepath.Ext(name))}, codec...)
		if out, err := exec.Command(ffmpeg, append(args, filepath.Join(album, name))...).CombinedOutput(); err != nil {
			t.Fatalf("making %s: %v\n%s", name, err, out)
		}
	}
	lib, err := d.CreateLibrary(ctx, "Music", db.LibraryMusic, "en", []string{library})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&scan.Scanner{DB: d, Prober: scan.FileProber{}}).Scan(ctx, lib); err != nil {
		t.Fatal(err)
	}
	// The scan measures nothing: the measurement is made at a song's first play and kept
	// with the file. The two songs are this new library's first two files.
	kept := func() (n int) {
		for id := int64(1); id <= 2; id++ {
			file, err := d.FileOnDisk(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if file.AudioExact != "" {
				n++
			}
		}
		return n
	}
	if kept() != 0 {
		t.Fatal("the scan measured a song")
	}
	var page api.ItemPage
	h.call("GET", "/v1/items?library="+lib.PublicID+"&kind=track", nil, &page, 200)
	songs := map[string]string{}
	for _, item := range page.Items {
		songs[item.Title] = item.ID
	}
	if songs["First"] == "" || songs["Second"] == "" {
		t.Fatalf("the library lists %+v", page.Items)
	}

	// The device says what its own music engine decodes: MP3 and Opus, not FLAC.
	var profile struct {
		Document map[string]any `json:"document"`
	}
	raw, _ := os.ReadFile(filepath.Join("..", "streaming", "profiles", "ios.json"))
	if json.Unmarshal(raw, &profile) != nil || profile.Document == nil {
		t.Fatal("the iOS device profile could not be read")
	}
	document := profile.Document
	document["audioDecode"] = []map[string]any{{"codec": "mp3", "containers": []string{"mp3"}}, {"codec": "opus", "containers": []string{"ogg"}}}
	h.call("PUT", "/v1/devices/"+h.device+"/capabilities", document, nil, 204)

	type render struct {
		Mode, URL string
		Codec     string
		Trim      *struct{ StartFrames, EndFrames int64 }
		SeekIndex *struct{ URL string }
		Frames    int64 `json:"durationFrames"`
	}
	play := func(song string) (api.PlaybackSession, render) {
		t.Helper()
		// A play that comes before the file has been measured is planned the ordinary way,
		// and the measurement carries on: a later play has it.
		for deadline := time.Now().Add(60 * time.Second); ; time.Sleep(200 * time.Millisecond) {
			var session api.PlaybackSession
			h.call("POST", "/v1/playback/sessions", api.PlaybackStart{ItemID: songs[song]}, &session, 201)
			var plan render
			if err := json.Unmarshal(session.Presentation.AudioRender, &plan); err == nil {
				return session, plan
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s has no render plan: %+v", song, session.Presentation)
			}
		}
	}

	session, plan := play("First")
	original, _ := os.ReadFile(filepath.Join(album, "01 First.mp3"))
	if plan.Mode != "direct" || plan.URL != session.Presentation.URL || !strings.HasSuffix(plan.URL, "/audio") || plan.Trim == nil ||
		plan.Trim.StartFrames == 0 || plan.Frames < 44100*5 || plan.Frames > 44100*7 || plan.SeekIndex == nil {
		t.Fatalf("the MP3 should be handed over as it is, measured: %+v (trim %+v) at %s", plan, plan.Trim, session.Presentation.URL)
	}
	if code, _, body := h.media(plan.URL); code != 200 || !bytes.Equal(body, original) {
		t.Fatalf("the song answered %d with %d bytes of %d", code, len(body), len(original))
	}
	if code, _, body := h.media(plan.SeekIndex.URL); code != 200 || !strings.Contains(string(body), `"offsets"`) {
		t.Fatalf("the seek index answered %d: %s", code, body)
	}

	if kept() != 1 {
		t.Fatalf("after one song was played %d measurements are kept, want 1", kept())
	}

	session, plan = play("Second")
	if plan.Mode != "converted" || plan.Codec != "opus" || plan.URL != session.Presentation.URL || !strings.HasSuffix(plan.URL, "/audio-converted") ||
		session.Presentation.Route != "converted" {
		t.Fatalf("the FLAC should be converted to Opus: %+v, %+v", plan, session.Presentation)
	}
	code, header, body := h.media(plan.URL)
	if code != 200 || header.Get("Content-Type") != "audio/ogg" || header.Get("X-Audio-Frames") == "" || !bytes.HasPrefix(body, []byte("OggS")) {
		t.Fatalf("the conversion answered %d %v with %d bytes", code, header, len(body))
	}
	h.call("DELETE", "/v1/playback/sessions/"+session.ID, nil, nil, 204)
}
