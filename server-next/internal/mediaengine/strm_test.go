package mediaengine_test

import (
	"bytes"
	"context"
	"encoding/json"
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
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
	"github.com/PorticoMediaServer/portico/server-next/internal/strm"
)

// A real local HTTP fixture supplies the URL's media. It is never a real provider.
// Both probing and conversion use the existing isolated decoder bridge.
func TestSTRMPlaysThroughExistingSessionPath(t *testing.T) {
	ffmpeg, ffprobe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	if decoder.ProbeConfinement(context.Background()) == nil {
		t.Setenv("PORTICO_DECODER_SANDBOX", "required")
	}
	film := filepath.Join(t.TempDir(), "target.mp4")
	if out, err := exec.Command(ffmpeg, "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=24",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
		"-t", "4", "-pix_fmt", "yuv420p", "-g", "48", "-ac", "2",
		"-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", "-movflags", "+faststart", film).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	content, err := os.ReadFile(film)
	if err != nil {
		t.Fatal(err)
	}
	outside := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "target.mp4", time.Time{}, bytes.NewReader(content))
	}))
	defer outside.Close()
	client, address := strm.New(), outside.URL
	h, d, _ := newHouseWithSTRM(t, ffmpeg, ffprobe, client)
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "Small Film (2020).strm"), []byte(address+"/target?token=secret"), 0600)
	lib, err := d.NewLibrary(context.Background(), db.NewLibrary{Name: "Films", Kind: db.LibraryMovies, MetadataProvider: "local", Folders: []string{root}, Options: db.LibraryOptions{STRM: true}})
	if err != nil {
		t.Fatal(err)
	}
	libraries, err := d.AllLibraries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	scanner := &scan.Scanner{DB: d, Prober: scan.FileProber{}, ReaderProber: scan.RemoteProber{}, STRM: client}
	if stats, err := scanner.Scan(context.Background(), libraries[0]); err != nil || stats.Probed != 1 {
		t.Fatalf("scan: %+v %v", stats, err)
	}
	var page api.ItemPage
	h.call("GET", "/v1/items?library="+lib.PublicID+"&kind=movie", nil, &page, 200)
	if len(page.Items) != 1 || page.Items[0].Title != "Small Film" {
		t.Fatalf("title: %+v", page.Items)
	}
	var session api.PlaybackSession
	h.call("POST", "/v1/playback/sessions", api.PlaybackStart{ItemID: page.Items[0].ID}, &session, 201)
	if session.Presentation.Route != playback.RouteOriginal {
		t.Fatalf("original: %+v", session.Presentation)
	}
	code, _, body := h.media(session.Presentation.URL, "Range", "bytes=100-199")
	if code != 206 || !bytes.Equal(body, content[100:200]) {
		t.Fatalf("relay: %d, %d bytes", code, len(body))
	}
	position := int64(1000)
	h.call("PATCH", "/v1/playback/sessions/"+session.ID, api.PlaybackChange{PositionMs: &position, Quality: &playback.Quality{Mode: playback.QualityFixed, ID: "240p"}}, &session, 200)
	if session.Presentation.Route != playback.RouteConverted {
		t.Fatalf("conversion: %+v", session.Presentation)
	}
	played := h.listen(t, session.Presentation.URL, ffmpeg, ffprobe)
	if played.seconds < 2.8 || played.seconds > 4.2 {
		t.Fatalf("converted %.3f seconds", played.seconds)
	}
	h.call("DELETE", "/v1/playback/sessions/"+session.ID, nil, nil, 204)
	// The switch takes effect before a rescan can remove the version.
	options := libraries[0].Options
	options.STRM = false
	if err := d.SetLibraryOptions(context.Background(), lib.ID, options); err != nil {
		t.Fatal(err)
	}
	response := h.do("POST", "/v1/playback/sessions", api.PlaybackStart{ItemID: page.Items[0].ID})
	defer response.Body.Close()
	var failure struct {
		Error api.Error `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&failure); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(failure)
	if response.StatusCode != 503 || !bytes.Contains(raw, []byte("source_unavailable")) || strings.Contains(string(raw), "media.test") || strings.Contains(string(raw), "secret") {
		t.Fatalf("disabled pointer: %d %s", response.StatusCode, raw)
	}
}
