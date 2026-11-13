package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/live"
)

func TestSourceChannelLogosAreHeldByTheServerAndRespectItsImageChoice(t *testing.T) {
	s, owner, _ := seededWith(t, 20, 1)
	var pictures [2][]byte
	for n := range pictures {
		picture := image.NewRGBA(image.Rect(0, 0, 64, 32))
		picture.Set(0, 0, color.RGBA{R: uint8(80 + n*100), A: 255})
		var buf bytes.Buffer
		if err := png.Encode(&buf, picture); err != nil {
			t.Fatal(err)
		}
		pictures[n] = buf.Bytes()
	}
	var reads, version atomic.Int32
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/playlist.m3u":
			fmt.Fprintf(w, "#EXTM3U\n")
			for n := 1; n <= 3; n++ {
				logo := "logo.png"
				if n == 3 {
					logo = "broken.png"
				}
				fmt.Fprintf(w, "#EXTINF:-1 tvg-id=\"channel-%d\" tvg-logo=\"http://%s/%s\",Channel %d\nhttp://streams.invalid/%d.ts\n", n, r.Host, logo, n, n)
			}
		case "/logo.png":
			reads.Add(1)
			w.Header().Set("Content-Type", "image/png")
			w.Write(pictures[version.Load()])
		default:
			fmt.Fprint(w, "not an image")
		}
	}))
	defer source.Close()
	src := addSource(t, s, owner, LiveSourceSave{Kind: "playlist", Name: new("Cable"), Config: &live.LiveSourceConfig{PlaylistURL: source.URL + "/playlist.m3u"}})
	channels := get[Page[LiveChannel]](t, s, owner, "/v1/live/channels").Items
	if len(channels) != 3 || reads.Load() != 1 {
		t.Fatalf("channels=%+v, image reads=%d", channels, reads.Load())
	}
	if !strings.HasPrefix(channels[0].Logo, "/v1/images/") || channels[1].Logo != channels[0].Logo || channels[2].Logo != "" {
		t.Fatalf("logos did not become server-held artwork: %+v", channels)
	}
	if strings.Contains(fmt.Sprint(channels), source.URL) {
		t.Fatal("a provider address reached the viewer")
	}
	fetch := func(address string) {
		t.Helper()
		response := httptest.NewRecorder()
		s.ServeHTTP(response, httptest.NewRequest("GET", address+"?w=64", nil))
		if _, _, err := image.DecodeConfig(response.Body); response.Code != 200 || err != nil {
			t.Fatalf("image route: %d %v", response.Code, err)
		}
	}
	fetch(channels[0].Logo)
	version.Store(1)
	if err := s.sources.Refresh(context.Background(), src.ID); err != nil {
		t.Fatal(err)
	}
	changed := get[Page[LiveChannel]](t, s, owner, "/v1/live/channels").Items
	if reads.Load() != 2 || changed[0].Logo == channels[0].Logo || changed[0].Logo != changed[1].Logo {
		t.Fatalf("changed logos: %+v, reads=%d", changed, reads.Load())
	}
	fetch(changed[0].Logo)
	fetch(channels[0].Logo) // The old immutable address still names its own bytes.
	if _, err := s.settings.Write(context.Background(), 1, "live", json.RawMessage(`{"importImages":false}`), -1, s.now()); err != nil {
		t.Fatal(err)
	}
	if err := s.sources.Refresh(context.Background(), src.ID); err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 2 {
		t.Fatal("the server fetched images with its image choice off")
	}
	if len(get[Page[LiveChannel]](t, s, owner, "/v1/live/channels").Items) != 3 {
		t.Fatal("a bad or disabled logo lost a channel")
	}
}
