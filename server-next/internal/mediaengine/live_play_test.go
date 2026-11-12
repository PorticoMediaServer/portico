package mediaengine_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/api"
	"github.com/PorticoMediaServer/portico/server-next/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server-next/internal/live"
)

// An over-the-air channel (MPEG-2 picture, MPEG audio, in MPEG-TS) watched in a browser,
// which plays neither, end to end over HTTP: the channel is measured, planned by the same
// planner as a title, and converted for that device; the session names a live playlist whose
// segments play as H.264; stopping the session ends the playlist's address.
func TestAnOverTheAirChannelPlaysInABrowser(t *testing.T) {
	ffmpeg, ffprobe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	h, _ := newHouse(t, ffmpeg, ffprobe)
	air := filepath.Join(t.TempDir(), "air.ts")
	if out, err := exec.Command(ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=25", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
		"-t", "60", "-pix_fmt", "yuv420p", "-ac", "2", "-c:v", "mpeg2video", "-q:v", "6", "-c:a", "mp2", "-f", "mpegts", air).CombinedOutput(); err != nil {
		t.Fatalf("making the channel: %v\n%s", err, out)
	}
	data, err := os.ReadFile(air)
	if err != nil {
		t.Fatal(err)
	}
	// The channel's source: a playlist naming one stream, sent at four times real time.
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/air.m3u" {
			fmt.Fprintf(w, "#EXTM3U\n#EXTINF:-1 tvg-chno=\"7\",Air Seven\nhttp://%s/air.ts\n", r.Host)
			return
		}
		rate, since := float64(len(data))/60*4, time.Now()
		for sent := 0; sent < len(data) && r.Context().Err() == nil; sent += 188 * 64 {
			if _, err := w.Write(data[sent:min(sent+188*64, len(data))]); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			if ahead := time.Duration(float64(sent)/rate*float64(time.Second)) - time.Since(since); ahead > 0 {
				time.Sleep(ahead)
			}
		}
	}))
	t.Cleanup(func() { source.CloseClientConnections(); source.Close() })
	var src api.LiveSource
	h.call("POST", "/v1/admin/live/sources", api.LiveSourceSave{Kind: "playlist", Name: new("Antenna"), TunerCount: new(1),
		Config: &live.LiveSourceConfig{PlaylistURL: source.URL + "/air.m3u"}}, &src, 201)
	var channels api.Page[api.LiveChannel]
	deadline := time.Now().Add(30 * time.Second)
	for len(channels.Items) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the source's channel never came in")
		}
		time.Sleep(200 * time.Millisecond)
		h.call("GET", "/v1/live/channels", nil, &channels, 200)
	}

	var session api.PlaybackSession
	h.call("POST", "/v1/playback/sessions", api.PlaybackStart{Channel: channels.Items[0].ID}, &session, 201)
	p := session.Presentation
	if p.Mode != "live" || p.Route != "converted" || !strings.HasSuffix(p.URL, "/index.m3u8") {
		t.Fatalf("an MPEG-2 channel in a browser: %+v", p)
	}
	var playlist string
	deadline = time.Now().Add(60 * time.Second)
	for strings.Count(playlist, "#EXTINF") < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("the live playlist never had two segments:\n%s", playlist)
		}
		code, _, raw := h.media(p.URL)
		if code != 200 {
			t.Fatalf("the live playlist answered %d: %s", code, raw)
		}
		playlist = string(raw)
		time.Sleep(500 * time.Millisecond)
	}
	if strings.Count(playlist, "#EXT-X-PROGRAM-DATE-TIME:") != strings.Count(playlist, "#EXTINF") || strings.Contains(playlist, "#EXT-X-ENDLIST") {
		t.Fatalf("the live playlist:\n%s", playlist)
	}
	// The first segment, after its init when the playlist names one, as a player joins them.
	var first, init string
	for _, line := range strings.Split(playlist, "\n") {
		if strings.HasPrefix(line, "#EXT-X-MAP:URI=") && init == "" {
			init = strings.Trim(strings.TrimPrefix(line, "#EXT-X-MAP:URI="), `"`)
		}
		if line != "" && !strings.HasPrefix(line, "#") {
			first = line
			break
		}
	}
	base := strings.TrimSuffix(p.URL, "index.m3u8")
	var joined []byte
	for _, name := range []string{init, first} {
		if name == "" {
			continue
		}
		code, _, raw := h.media(base + name)
		if code != 200 || len(raw) == 0 {
			t.Fatalf("%s answered %d with %d bytes", name, code, len(raw))
		}
		joined = append(joined, raw...)
	}
	played := filepath.Join(t.TempDir(), "played"+map[bool]string{true: ".mp4", false: ".ts"}[init != ""])
	if err := os.WriteFile(played, joined, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(ffprobe, "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=codec_name", "-of", "csv=p=0", played).Output()
	codec := strings.TrimSpace(string(out))
	if err != nil || codec == "" || codec == "mpeg2video" {
		t.Fatalf("the segment's picture is %q (%v); the container is %s", codec, err, p.Container)
	}
	t.Logf("an MPEG-2 channel plays in the browser as %s in %s", codec, p.Container)
	h.call("DELETE", "/v1/playback/sessions/"+session.ID, nil, nil, 204)
	if code, _, _ := h.media(p.URL); code != 404 {
		t.Fatalf("a stopped session's playlist answered %d", code)
	}
}
