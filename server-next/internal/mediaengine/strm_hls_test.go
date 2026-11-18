package mediaengine_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/api"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
	"github.com/PorticoMediaServer/portico/server-next/internal/strm"
)

// A .strm naming an HLS playlist (a master, read as its best variant) is measured and played
// through the ordinary path. The server fetches the playlist and its pieces; the decoders
// read them from the server's bridge, and the app is given only the server's own stream.
func TestSTRMPlaylistPlaysThroughExistingSessionPath(t *testing.T) {
	ffmpeg, ffprobe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	if decoder.ProbeConfinement(context.Background()) == nil {
		t.Setenv("PORTICO_DECODER_SANDBOX", "required")
	}
	// Under an AES-128 key, so the key is followed as well as the pieces.
	site, keyInfo := t.TempDir(), filepath.Join(t.TempDir(), "key-info")
	if err := os.WriteFile(filepath.Join(site, "film.key"), []byte("0123456789abcdef"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyInfo, []byte("film.key?token=secret\n"+filepath.Join(site, "film.key")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(ffmpeg, "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=24",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "6", "-pix_fmt", "yuv420p", "-g", "48", "-ac", "2",
		"-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", "-f", "hls", "-hls_time", "2", "-hls_playlist_type", "vod", "-hls_key_info_file", keyInfo,
		"-hls_segment_filename", filepath.Join(site, "piece%d.ts"), filepath.Join(site, "film.m3u8")).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	master := "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=150000\nmissing.m3u8\n#EXT-X-STREAM-INF:BANDWIDTH=900000,RESOLUTION=640x360\nfilm.m3u8?token=secret\n"
	if err := os.WriteFile(filepath.Join(site, "master.m3u8"), []byte(master), 0o600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	asked := map[string]int{}
	files := http.FileServer(http.Dir(site))
	outside := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked[r.URL.Path]++
		mu.Unlock()
		files.ServeHTTP(w, r)
	}))
	defer outside.Close()
	client := strm.New()
	h, d, _ := newHouseWithSTRM(t, ffmpeg, ffprobe, client)
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "Small Film (2020).strm"), []byte(outside.URL+"/master.m3u8?token=secret"), 0o600)
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
	// The stream is made from the playlist's pieces, so on a busy machine the start can answer
	// 202 first: send it again, as an app does.
	session := h.startPlayback(api.PlaybackStart{ItemID: page.Items[0].ID})
	if session.Presentation.Route == playback.RouteOriginal || strings.Contains(session.Presentation.URL, outside.URL) {
		t.Fatalf("presentation: %+v", session.Presentation)
	}
	played := h.listen(t, session.Presentation.URL, ffmpeg, ffprobe)
	if played.seconds < 5.5 || played.seconds > 6.5 {
		t.Fatalf("played %.3f seconds by %s", played.seconds, session.Presentation.Route)
	}
	out, err := exec.Command(ffprobe, "-v", "error", "-show_entries", "stream=codec_type", "-of", "csv=p=0", played.file).Output()
	if err != nil || !strings.Contains(string(out), "video") {
		t.Fatalf("no picture: %s %v", out, err)
	}
	repackaged := session.Presentation.Route
	position := int64(1000)
	h.call("PATCH", "/v1/playback/sessions/"+session.ID, api.PlaybackChange{PositionMs: &position, Quality: &playback.Quality{Mode: playback.QualityFixed, ID: "240p"}}, &session, 200)
	if session.Presentation.Route != playback.RouteConverted {
		t.Fatalf("conversion: %+v", session.Presentation)
	}
	converted := h.listen(t, session.Presentation.URL, ffmpeg, ffprobe)
	if converted.seconds < 4.5 || converted.seconds > 6.5 {
		t.Fatalf("converted %.3f seconds", converted.seconds)
	}
	h.call("DELETE", "/v1/playback/sessions/"+session.ID, nil, nil, 204)
	mu.Lock()
	defer mu.Unlock()
	if asked["/missing.m3u8"] != 0 || asked["/piece2.ts"] == 0 || asked["/film.key"] == 0 {
		t.Fatalf("the server read %v", asked)
	}
	t.Logf("played %.3f seconds %s, %.3f converted from 0:01; the server read %v", played.seconds, repackaged, converted.seconds, asked)
}
