package mediaengine_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/api"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaengine"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

// The test executable answers as the sandbox helpers, as the server does.
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
	os.Exit(m.Run())
}

// house is a whole server over real HTTP, put together the way cmd/portico-server does it,
// with the owner signed in on the main profile.
type house struct {
	*testing.T
	url   string
	token string
	data  string
}

func (h *house) do(method, path string, body any, header ...string) *http.Response {
	h.Helper()
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	r, err := http.NewRequest(method, h.url+path, reader)
	if err != nil {
		h.Fatal(err)
	}
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if h.token != "" && !strings.HasPrefix(path, "/v1/media/") {
		r.Header.Set("Authorization", "Bearer "+h.token)
	}
	for n := 0; n+1 < len(header); n += 2 {
		r.Header.Set(header[n], header[n+1])
	}
	w, err := http.DefaultClient.Do(r)
	if err != nil {
		h.Fatal(err)
	}
	return w
}

// call sends a request, checks the status and reads the JSON answer into out.
func (h *house) call(method, path string, body, out any, want int) {
	h.Helper()
	w := h.do(method, path, body)
	defer w.Body.Close()
	raw, _ := io.ReadAll(w.Body)
	if w.StatusCode != want {
		h.Fatalf("%s %s answered %d, want %d: %s", method, path, w.StatusCode, want, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			h.Fatalf("%s %s: %v: %s", method, path, err, raw)
		}
	}
}

// media fetches one file of a stream, waiting while the server says it is still preparing.
func (h *house) media(path string, header ...string) (int, http.Header, []byte) {
	h.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		w := h.do("GET", path, nil, header...)
		raw, err := io.ReadAll(w.Body)
		w.Body.Close()
		if err != nil {
			h.Fatalf("GET %s: %v", path, err)
		}
		if w.StatusCode != http.StatusServiceUnavailable || time.Now().After(deadline) {
			return w.StatusCode, w.Header, raw
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func newHouse(t *testing.T, ffmpeg, ffprobe string) (*house, *db.DB) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	data := t.TempDir()
	d, err := db.Open(ctx, filepath.Join(data, "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := api.New(ctx, d, data, "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	previous := decoder.CurrentToolchain()
	engine, err := mediaengine.New(ctx, d, mediaengine.Options{Folder: filepath.Join(data, "transcodes"), FFmpeg: ffmpeg, FFprobe: ffprobe})
	if err != nil {
		t.Fatal(err)
	}
	handler.Playback().SetEngine(engine, engine)
	handler.ServeMediaWith(engine.ServeMedia)
	go engine.Run(ctx)
	server := httptest.NewServer(handler)
	t.Cleanup(func() {
		server.Close()
		wait, done := context.WithTimeout(context.Background(), 20*time.Second)
		defer done()
		if err := engine.Shutdown(wait); err != nil {
			t.Errorf("stopping the streams: %v", err)
		}
		cancel()
		decoder.RestoreToolchain(previous)
		d.Close()
	})
	h := &house{T: t, url: server.URL, data: data}

	code, err := os.ReadFile(filepath.Join(data, "setup-code.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var owner api.SignInResponse
	h.call("POST", "/v1/setup/owner", api.SetupOwnerRequest{SetupCode: strings.TrimSpace(string(code)), Username: "owner",
		Password: "a long enough test password", Device: api.DeviceInfo{InstallationID: strings.Repeat("a", 32), Name: "Test player"}}, &owner, 200)
	h.token = owner.Session.AccessToken
	var session api.Session
	h.call("POST", "/v1/profiles/"+owner.Profiles[0].ID+"/select", api.ProfileSelect{}, &session, 200)
	h.token = session.AccessToken

	// The device says what it plays, as a browser would.
	profiles, err := os.ReadFile(filepath.Join("..", "streaming", "testdata", "client-profiles.json"))
	if err != nil {
		t.Fatal(err)
	}
	var documents map[string]json.RawMessage
	if err := json.Unmarshal(profiles, &documents); err != nil {
		t.Fatal(err)
	}
	h.call("PUT", "/v1/devices/"+session.DeviceID+"/capabilities", documents["chromium"], nil, 204)
	return h, d
}

// A film file in a library folder is scanned, and a signed-in profile then plays it over
// HTTP: first the file itself by byte range, then, at a lower quality, a conversion as an
// HLS playlist with its first segments. Changing quality kills the first address, stopping
// kills the second, and nothing is left running or on disk.
func TestAScannedFilmPlaysAsItIsAndConverted(t *testing.T) {
	ffmpeg, ffprobe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	h, d := newHouse(t, ffmpeg, ffprobe)
	ctx := context.Background()

	library := filepath.Join(t.TempDir(), "Films")
	film := filepath.Join(library, "Small Film (2020)", "Small Film (2020).mp4")
	if err := os.MkdirAll(filepath.Dir(film), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=24", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
		"-map", "0:v", "-map", "1:a", "-map", "1:a", "-metadata:s:a:0", "language=eng", "-metadata:s:a:1", "language=jpn",
		"-t", "12", "-pix_fmt", "yuv420p", "-g", "48", "-ac", "2", "-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac",
		"-movflags", "+faststart", film).CombinedOutput(); err != nil {
		t.Fatalf("making the film: %v\n%s", err, out)
	}
	original, err := os.ReadFile(film)
	if err != nil {
		t.Fatal(err)
	}

	lib, err := d.CreateLibrary(ctx, "Films", db.LibraryMovies, "en", []string{library})
	if err != nil {
		t.Fatal(err)
	}
	stats, err := (&scan.Scanner{DB: d, Prober: mediaengine.Prober{FFprobe: ffprobe}}).Scan(ctx, lib)
	if err != nil || stats.ItemsNew != 1 || stats.Probed != 1 {
		t.Fatalf("scan: %+v, %v", stats, err)
	}
	var page api.ItemPage
	h.call("GET", "/v1/items?library="+lib.PublicID+"&kind=movie", nil, &page, 200)
	if len(page.Items) != 1 || page.Items[0].Title != "Small Film" {
		t.Fatalf("the library lists %+v", page.Items)
	}
	item := page.Items[0].ID

	// What the scan read from the file is what the player is offered.
	var options api.PlaybackOptions
	h.call("GET", "/v1/items/"+item+"/playback-options", nil, &options, 200)
	version := options.Versions[0]
	if v := version; len(v.Video) != 1 || v.Video[0].Codec != "h264" || v.Video[0].Height != 720 || len(v.Audio) != 2 ||
		v.Audio[0].Codec != "aac" || v.Audio[1].Language != "jpn" || v.DurationMs < 11_000 || v.DurationMs > 13_000 {
		t.Fatalf("the version is %+v", v)
	}
	// The profile's own settings reach the planner: with Japanese preferred, that track plays.
	if options.Preferred.AudioID != version.Audio[0].ID {
		t.Fatalf("the first audio track should play by default: %+v", options.Preferred)
	}
	h.call("PATCH", "/v1/preferences", api.PreferencesEdit{Scope: "profile-server", DeviceClass: "web",
		Values: map[string]any{"playback.preferredAudioLanguages": []string{"ja"}}}, nil, 200)
	h.call("GET", "/v1/items/"+item+"/playback-options", nil, &options, 200)
	if options.Preferred.AudioID != version.Audio[1].ID {
		t.Fatalf("with Japanese preferred the Japanese track should play: %+v", options.Preferred)
	}
	h.call("PATCH", "/v1/preferences", api.PreferencesEdit{Scope: "profile-server", DeviceClass: "web",
		Values: map[string]any{"playback.preferredAudioLanguages": nil}}, nil, 200)

	// The file as it is, by byte range.
	var session api.PlaybackSession
	h.call("POST", "/v1/playback/sessions", api.PlaybackStart{ItemID: item}, &session, 201)
	first := session.Presentation
	if first.Mode != "direct" || first.Route != playback.RouteOriginal || !strings.HasSuffix(first.URL, "/original") {
		t.Fatalf("the film should play as it is: %+v", first)
	}
	code, header, body := h.media(first.URL, "Range", "bytes=1000-1999")
	if code != http.StatusPartialContent || !bytes.Equal(body, original[1000:2000]) ||
		header.Get("Content-Range") != fmt.Sprintf("bytes 1000-1999/%d", len(original)) || header.Get("Content-Type") != "video/mp4" {
		t.Fatalf("a byte range answered %d %v with %d bytes", code, header, len(body))
	}
	if code, _, body = h.media(first.URL); code != 200 || !bytes.Equal(body, original) {
		t.Fatalf("the whole file answered %d with %d bytes of %d", code, len(body), len(original))
	}
	h.call("PUT", "/v1/playback/sessions/"+session.ID+"/progress", api.PlaybackProgress{State: "playing", PositionMs: 2000}, nil, 200)

	// A lower quality: the same session, a conversion at a new address. The old one is dead.
	position := int64(2000)
	h.call("PATCH", "/v1/playback/sessions/"+session.ID, api.PlaybackChange{PositionMs: &position,
		Quality: &playback.Quality{Mode: playback.QualityFixed, ID: "240p"}}, &session, 200)
	second := session.Presentation
	if second.Mode != "stream" || second.Route != playback.RouteConverted || second.Generation != first.Generation+1 ||
		second.URL == first.URL || !strings.HasSuffix(second.URL, "/master.m3u8") {
		t.Fatalf("a lower quality should be a conversion at a new address: %+v", second)
	}
	if code, _, _ = h.media(first.URL); code != http.StatusNotFound {
		t.Fatalf("the replaced address answered %d", code)
	}
	base := second.URL[:strings.LastIndex(second.URL, "/")+1]
	code, _, master := h.media(second.URL)
	if code != 200 || !strings.HasPrefix(string(master), "#EXTM3U") {
		t.Fatalf("the playlist answered %d: %s", code, master)
	}
	// The master playlist names the picture's playlist; that one names the segments.
	names := func(playlist []byte) (out []string) {
		for _, line := range strings.Split(string(playlist), "\n") {
			line = strings.TrimSpace(line)
			if start := strings.Index(line, `URI="`); strings.HasPrefix(line, "#EXT-X-MAP") && start >= 0 {
				out = append(out, strings.SplitN(line[start+5:], `"`, 2)[0])
			} else if line != "" && !strings.HasPrefix(line, "#") {
				out = append(out, line)
			}
		}
		return out
	}
	variants := names(master)
	if len(variants) == 0 {
		t.Fatalf("the master playlist names nothing:\n%s", master)
	}
	code, _, playlist := h.media(base + variants[0])
	segments := names(playlist)
	if code != 200 || len(segments) < 2 {
		t.Fatalf("the picture's playlist answered %d:\n%s", code, playlist)
	}
	converted := filepath.Join(t.TempDir(), "converted")
	var joined []byte
	for _, segment := range segments[:min(len(segments), 3)] {
		code, _, body := h.media(base + segment)
		if code != 200 || len(body) == 0 {
			t.Fatalf("%s answered %d with %d bytes", segment, code, len(body))
		}
		joined = append(joined, body...)
	}
	// What was fetched is a picture ffprobe reads, at the height asked for.
	if err := os.WriteFile(converted, joined, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(ffprobe, "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=codec_name,height", "-of", "csv=p=0", converted).CombinedOutput()
	if codec, height, _ := strings.Cut(strings.TrimSpace(string(out)), ","); err != nil || codec == "" || height != "240" {
		t.Fatalf("the converted segments are %q (%v), want a picture 240 high", out, err)
	}

	// Stop: the address dies with the session and the conversion is cleared away.
	h.call("DELETE", "/v1/playback/sessions/"+session.ID, nil, nil, 204)
	if code, _, _ = h.media(second.URL); code != http.StatusNotFound {
		t.Fatalf("the stopped address answered %d", code)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		left, _ := os.ReadDir(filepath.Join(h.data, "transcodes"))
		if len(left) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the conversion left %d entries behind", len(left))
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// A film whose file the scan has listed but not yet read plays all the same: the engine
// measures the file when it is asked for. Nobody waits for the reading pass.
func TestAFilmPlaysBeforeTheScanHasReadIt(t *testing.T) {
	ffmpeg, ffprobe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	h, d := newHouse(t, ffmpeg, ffprobe)
	ctx := context.Background()
	library := filepath.Join(t.TempDir(), "Films")
	film := filepath.Join(library, "Unread Film (2021).mp4")
	if err := os.MkdirAll(library, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=640x360:rate=24", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
		"-t", "4", "-pix_fmt", "yuv420p", "-ac", "2", "-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", "-movflags", "+faststart", film).CombinedOutput(); err != nil {
		t.Fatalf("making the film: %v\n%s", err, out)
	}
	original, err := os.ReadFile(film)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := d.CreateLibrary(ctx, "Films", db.LibraryMovies, "en", []string{library})
	if err != nil {
		t.Fatal(err)
	}
	// The folder walk only: no file is read.
	stats, err := (&scan.Scanner{DB: d}).Scan(ctx, lib)
	if err != nil || stats.ItemsNew != 1 || stats.Probed != 0 {
		t.Fatalf("scan: %+v, %v", stats, err)
	}
	var page api.ItemPage
	h.call("GET", "/v1/items?library="+lib.PublicID+"&kind=movie", nil, &page, 200)
	if len(page.Items) != 1 {
		t.Fatalf("the library lists %+v", page.Items)
	}
	var session api.PlaybackSession
	h.call("POST", "/v1/playback/sessions", api.PlaybackStart{ItemID: page.Items[0].ID}, &session, 201)
	if session.Presentation.Mode != "direct" {
		t.Fatalf("the unread film should play as it is: %+v", session.Presentation)
	}
	if code, _, body := h.media(session.Presentation.URL, "Range", "bytes=0-999"); code != http.StatusPartialContent || !bytes.Equal(body, original[:1000]) {
		t.Fatalf("a byte range of the unread film answered %d with %d bytes", code, len(body))
	}
	h.call("DELETE", "/v1/playback/sessions/"+session.ID, nil, nil, 204)
}
