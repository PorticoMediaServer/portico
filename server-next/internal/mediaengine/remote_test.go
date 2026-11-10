package mediaengine_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/api"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
	"github.com/PorticoMediaServer/portico/server-next/internal/remote/remotetest"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

// A film on a WebDAV source the owner added is scanned through the source, offered with what
// was read from it, and played: as it is by byte range, and converted at a lower quality.
// Where this host has the decoder's isolated bridge the sandbox is required throughout.
func TestAFilmOnRemoteStoragePlays(t *testing.T) {
	ffmpeg, ffprobe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	if decoder.ProbeConfinement(context.Background()) == nil {
		t.Setenv("PORTICO_DECODER_SANDBOX", "required")
	}
	film := filepath.Join(t.TempDir(), "film.mp4")
	if out, err := exec.Command(ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=24", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
		"-t", "12", "-pix_fmt", "yuv420p", "-g", "48", "-ac", "2", "-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac",
		"-movflags", "+faststart", film).CombinedOutput(); err != nil {
		t.Fatalf("making the film: %v\n%s", err, out)
	}
	original, err := os.ReadFile(film)
	if err != nil {
		t.Fatal(err)
	}
	dav := remotetest.NewDAV(t, map[string]string{"Media/Films/Small Film (2020)/Small Film (2020).mp4": string(original)})
	h, d, sources := newHouseWith(t, ffmpeg, ffprobe)
	ctx := context.Background()

	var source api.RemoteSource
	h.call("POST", "/v1/admin/remote-sources", api.RemoteSourceCreate{Kind: "webdav", Name: "NAS",
		WebDAV: &api.WebDAVSettings{Address: dav.URL, User: dav.User, Password: dav.Password}}, &source, 200)
	row, err := d.RemoteSource(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := d.CreateLibrary(ctx, "Films", db.LibraryMovies, "en", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddRemoteFolder(ctx, lib.ID, row.ID, "Media/Films"); err != nil {
		t.Fatal(err)
	}
	libraries, err := d.AllLibraries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scanner := &scan.Scanner{DB: d, Remote: sources.Open, ReaderProber: scan.RemoteProber{}}
	stats, err := scanner.Scan(ctx, libraries[0])
	if err != nil || stats.ItemsNew != 1 || stats.Probed != 1 {
		t.Fatalf("scan: %+v, %v", stats, err)
	}
	var page api.ItemPage
	h.call("GET", "/v1/items?library="+lib.PublicID+"&kind=movie", nil, &page, 200)
	if len(page.Items) != 1 || page.Items[0].Title != "Small Film" {
		t.Fatalf("the library lists %+v", page.Items)
	}
	var options api.PlaybackOptions
	h.call("GET", "/v1/items/"+page.Items[0].ID+"/playback-options", nil, &options, 200)
	if v := options.Versions[0]; len(v.Video) != 1 || v.Video[0].Height != 720 || len(v.Audio) != 1 {
		t.Fatalf("the version is %+v", v)
	}

	var session api.PlaybackSession
	h.call("POST", "/v1/playback/sessions", api.PlaybackStart{ItemID: page.Items[0].ID}, &session, 201)
	first := session.Presentation
	if first.Mode != "direct" || first.Route != playback.RouteOriginal {
		t.Fatalf("the film should play as it is: %+v", first)
	}
	code, header, body := h.media(first.URL, "Range", "bytes=1000-1999")
	if code != http.StatusPartialContent || !bytes.Equal(body, original[1000:2000]) ||
		header.Get("Content-Range") != fmt.Sprintf("bytes 1000-1999/%d", len(original)) {
		t.Fatalf("a byte range answered %d %v with %d bytes", code, header, len(body))
	}

	position := int64(0)
	h.call("PATCH", "/v1/playback/sessions/"+session.ID, api.PlaybackChange{PositionMs: &position,
		Quality: &playback.Quality{Mode: playback.QualityFixed, ID: "240p"}}, &session, 200)
	second := session.Presentation
	if second.Route != playback.RouteConverted || !strings.HasSuffix(second.URL, "/master.m3u8") {
		t.Fatalf("a lower quality should be a conversion: %+v", second)
	}
	base := second.URL[:strings.LastIndex(second.URL, "/")+1]
	_, _, master := h.media(second.URL)
	var variant string
	for _, line := range strings.Split(string(master), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			variant = line
			break
		}
	}
	code, _, playlist := h.media(base + variant)
	var segment string
	for _, line := range strings.Split(string(playlist), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			segment = line
			break
		}
	}
	if code != 200 || segment == "" {
		t.Fatalf("the picture's playlist answered %d:\n%s", code, playlist)
	}
	if code, _, body = h.media(base + segment); code != 200 || len(body) == 0 {
		t.Fatalf("the first converted segment answered %d with %d bytes", code, len(body))
	}
	h.call("DELETE", "/v1/playback/sessions/"+session.ID, nil, nil, 204)
}
