package linearinput_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/linearinput"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// The test binary is its own sandbox helper, like the server binary (Linux).
func TestMain(m *testing.M) {
	if handled, err := decoder.RunHelper(os.Args[1:]); handled {
		if err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// Demo, 23 Sep: every live channel failed with "exit status 1". Its source is
// an HLS playlist over HTTPS with relative segment names, and FFmpeg 7.1+
// (extension_picky) refused every segment because the gateway's routes had no
// extension ("URL … is not in allowed_segment_extensions"). The whole path
// runs here as it does in the server: the gateway fetches and rewrites the
// playlist, and the confined ffprobe (sandbox-exec on macOS, bubblewrap on
// Linux) reads it and its segments through the gateway. Skipped where FFmpeg
// or the confinement backend isn't available.
func TestConfinedProbeReadsAnHLSSourceThroughTheGateway(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if !decoder.CheckConfinement(ctx) {
		t.Skip("decoder confinement is unavailable here")
	}
	ffmpeg, err1 := exec.LookPath("ffmpeg")
	ffprobe, err2 := exec.LookPath("ffprobe")
	if err1 != nil || err2 != nil {
		t.Skip("ffmpeg/ffprobe are not installed")
	}
	ffmpeg, _ = filepath.EvalSymlinks(ffmpeg)
	ffprobe, _ = filepath.EvalSymlinks(ffprobe)
	libraries, err := decoder.ResolveLibraries(ffmpeg, ffprobe, nil)
	if err != nil {
		t.Skipf("FFmpeg's libraries can't be resolved: %v", err)
	}
	dir := t.TempDir()
	generate := exec.CommandContext(ctx, ffmpeg, "-nostdin", "-v", "error", "-f", "lavfi", "-i", "testsrc=size=160x90:rate=10", "-t", "3", "-c:v", "mpeg2video", "-f", "hls", "-hls_time", "1", "-hls_list_size", "0", "-hls_segment_filename", filepath.Join(dir, "segment-%03d.ts"), filepath.Join(dir, "index.m3u8"))
	if out, err := generate.CombinedOutput(); err != nil {
		t.Fatalf("generate the source: %v %s", err, out)
	}
	playlist, err := os.ReadFile(filepath.Join(dir, "index.m3u8"))
	if err != nil {
		t.Fatal(err)
	}
	// Two shapes of a real source: relative names with ".ts" (the demo's), and
	// extensionless segment routes (common for IPTV providers).
	extensionless := strings.NewReplacer("segment-000.ts", "chunk/0", "segment-001.ts", "chunk/1", "segment-002.ts", "chunk/2", "segment-003.ts", "chunk/3").Replace(string(playlist))
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/live/index.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = w.Write(playlist)
		case r.URL.Path == "/live/plain.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = w.Write([]byte(extensionless))
		case strings.HasPrefix(r.URL.Path, "/live/chunk/"):
			http.ServeFile(w, r, filepath.Join(dir, "segment-00"+strings.TrimPrefix(r.URL.Path, "/live/chunk/")+".ts"))
		case strings.HasPrefix(r.URL.Path, "/live/segment-"):
			http.ServeFile(w, r, filepath.Join(dir, filepath.Base(r.URL.Path)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer source.Close()
	approval, err := (remotemedia.Policy{}).Approve(ctx, source.URL+"/live/")
	if err != nil {
		t.Fatal(err)
	}
	supervisor := storage.New("").Supervisor
	for _, name := range []string{"index.m3u8", "plain.m3u8"} {
		gateway, err := linearinput.Open(ctx, livechannels.Input{Locator: source.URL + "/live/" + name, Policy: remotemedia.Policy{Approvals: []remotemedia.Approval{approval}, ReadTimeout: 10 * time.Second}})
		if err != nil {
			t.Fatalf("%s: open the gateway: %v", name, err)
		}
		if !gateway.HLS() {
			t.Fatalf("%s: not recognized as HLS", name)
		}
		reservation, err := gateway.Reserve()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := decoder.LinearProbe(ctx, supervisor, "gateway-hls-"+name, ffprobe, gateway.URL(), true, reservation, nil, libraries...)
		_ = gateway.Close()
		if err != nil {
			t.Fatalf("%s: the confined probe failed: %v", name, err)
		}
		var probe struct {
			Streams []struct {
				CodecType string `json:"codec_type"`
			} `json:"streams"`
		}
		if err = json.Unmarshal(raw, &probe); err != nil || len(probe.Streams) == 0 || probe.Streams[0].CodecType != "video" {
			t.Fatalf("%s: probe %s (%v)", name, raw, err)
		}
	}
}

// FFmpeg reloads a live playlist with "Range: bytes=0-". The gateway used to
// forward it: the source answered 206 with the original playlist's length in
// Content-Range, and the gateway sent its rewritten (longer) playlist under that
// range, so the client read it cut mid-URI. FFmpeg 7.1+ refused the truncated
// segment URI ("not in allowed_segment_extensions") and every live channel
// failed on a source that honors ranges (as Go's and nginx's file servers do). A
// playlist is always whole: a 200 with every URI intact, however it's asked for.
func TestAReloadedPlaylistIsServedWhole(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	dir := t.TempDir()
	var playlist strings.Builder
	playlist.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:2\n#EXT-X-MEDIA-SEQUENCE:0\n")
	for i := 0; i < 5; i++ {
		name := "segment-" + string(rune('0'+i)) + ".ts"
		playlist.WriteString("#EXTINF:2.0,\n" + name + "\n")
		if err := os.WriteFile(filepath.Join(dir, name), []byte{0x47}, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "index.m3u8"), []byte(playlist.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	source := httptest.NewServer(http.StripPrefix("/live/", http.FileServer(http.Dir(dir))))
	defer source.Close()
	approval, err := (remotemedia.Policy{}).Approve(ctx, source.URL+"/live/")
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := linearinput.Open(ctx, livechannels.Input{Locator: source.URL + "/live/index.m3u8", Policy: remotemedia.Policy{Approvals: []remotemedia.Approval{approval}, ReadTimeout: 10 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	get := func(rng string) (*http.Response, string) {
		t.Helper()
		req, _ := http.NewRequestWithContext(ctx, "GET", gateway.URL(), nil)
		if rng != "" {
			req.Header.Set("Range", rng)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var body strings.Builder
		_, _ = io.Copy(&body, resp.Body)
		return resp, body.String()
	}
	for _, rng := range []string{"", "bytes=0-", "bytes=0-"} { // the first read, then reloads
		resp, body := get(rng)
		if resp.StatusCode != 200 || resp.Header.Get("Content-Range") != "" {
			t.Fatalf("range %q: %d %q", rng, resp.StatusCode, resp.Header.Get("Content-Range"))
		}
		uris := 0
		for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
			if line != "" && !strings.HasPrefix(line, "#") {
				uris++
				if !strings.HasSuffix(line, ".ts") {
					t.Fatalf("range %q: a segment URI without its extension: %q", rng, line)
				}
			}
		}
		if uris != 5 {
			t.Fatalf("range %q: %d of 5 segments:\n%s", rng, uris, body)
		}
	}
}
