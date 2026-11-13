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
	"time"

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
	var reads, failedReads, version atomic.Int32
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/playlist.m3u":
			fmt.Fprintf(w, "#EXTM3U\n")
			for n := 1; n <= 4; n++ {
				logo := "logo.png"
				if n >= 3 {
					logo = "broken.png"
				}
				fmt.Fprintf(w, "#EXTINF:-1 tvg-id=\"channel-%d\" tvg-logo=\"http://%s/%s?version=%d\",Channel %d\nhttp://streams.invalid/%d.ts\n", n, r.Host, logo, version.Load(), n, n)
			}
		case "/logo.png":
			reads.Add(1)
			if version.Load() == 2 {
				fmt.Fprint(w, "not an image")
				return
			}
			w.Header().Set("Content-Type", "image/png")
			w.Write(pictures[version.Load()])
		default:
			failedReads.Add(1)
			fmt.Fprint(w, "not an image")
		}
	}))
	defer source.Close()
	src := addSource(t, s, owner, LiveSourceSave{Kind: "playlist", Name: new("Cable"), Config: &live.LiveSourceConfig{PlaylistURL: source.URL + "/playlist.m3u"}})
	channels := get[Page[LiveChannel]](t, s, owner, "/v1/live/channels").Items
	if len(channels) != 4 || reads.Load() != 1 || failedReads.Load() != 1 {
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
	if err := s.sources.Refresh(context.Background(), src.ID); err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 1 {
		t.Fatal("an already-held logo was fetched again")
	}
	version.Store(1)
	if err := s.sources.Refresh(context.Background(), src.ID); err != nil {
		t.Fatal(err)
	}
	changed := get[Page[LiveChannel]](t, s, owner, "/v1/live/channels").Items
	if reads.Load() != 2 || changed[0].Logo == channels[0].Logo || changed[0].Logo != changed[1].Logo {
		t.Fatalf("changed logos: %+v, reads=%d", changed, reads.Load())
	}
	fetch(changed[0].Logo)
	version.Store(2)
	if err := s.sources.Refresh(context.Background(), src.ID); err != nil {
		t.Fatal(err)
	}
	if got := get[Page[LiveChannel]](t, s, owner, "/v1/live/channels").Items; got[0].Logo != changed[0].Logo {
		t.Fatal("failed replacement lost its held logo")
	}
	if err := s.sources.Refresh(context.Background(), src.ID); err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 4 {
		t.Fatal("failed replacement was mistaken for an already-held URL")
	}
	fetch(channels[0].Logo) // The old immutable address still names its own bytes.
	if _, err := s.settings.Write(context.Background(), 1, "live", json.RawMessage(`{"importImages":false}`), -1, s.now()); err != nil {
		t.Fatal(err)
	}
	if err := s.sources.Refresh(context.Background(), src.ID); err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 4 {
		t.Fatal("the server fetched images with its image choice off")
	}
	if got := get[Page[LiveChannel]](t, s, owner, "/v1/live/channels").Items; got[0].Logo != changed[0].Logo {
		t.Fatal("turning importing off cleared a held logo")
	}
	if len(get[Page[LiveChannel]](t, s, owner, "/v1/live/channels").Items) != 4 {
		t.Fatal("a bad or disabled logo lost a channel")
	}
}

func TestLogoBudgetLeavesTheNewGuideAndChannelsReadableBeforeItFinishes(t *testing.T) {
	s, owner, _ := seededWith(t, 20, 0)
	if _, err := s.settings.Write(context.Background(), 1, "live", json.RawMessage(`{"importImages":false}`), -1, s.now()); err != nil {
		t.Fatal(err)
	}
	var updated atomic.Bool
	var logos atomic.Int32
	entered := make(chan struct{}, 1)
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		title := "Before"
		if updated.Load() {
			title = "Updated"
		}
		switch r.URL.Path {
		case "/playlist":
			fmt.Fprint(w, "#EXTM3U\n")
			for n := 1; n <= 2; n++ {
				fmt.Fprintf(w, "#EXTINF:-1 tvg-id=\"%d\" tvg-logo=\"http://%s/logo/%d\",%s %d\nhttp://streams.invalid/%d.ts\n", n, r.Host, n, title, n, n)
			}
		case "/guide":
			fmt.Fprintf(w, `<tv><channel id="1"><display-name>One</display-name></channel><programme channel="1" start="%s" stop="%s"><title>%s</title></programme></tv>`,
				s.now().Add(-time.Hour).UTC().Format("20060102150405 +0000"), s.now().Add(time.Hour).UTC().Format("20060102150405 +0000"), title)
		default:
			logos.Add(1)
			select {
			case entered <- struct{}{}:
			default:
			}
			<-r.Context().Done()
		}
	}))
	defer source.Close()
	src := addSource(t, s, owner, LiveSourceSave{Kind: "playlist", Name: new("Aerial"), Config: &live.LiveSourceConfig{PlaylistURL: source.URL + "/playlist", GuideURL: source.URL + "/guide"}})
	updated.Store(true)
	if _, err := s.settings.Write(context.Background(), 1, "live", json.RawMessage(`{"importImages":true}`), -1, s.now()); err != nil {
		t.Fatal(err)
	}
	budget, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.sources.Refresh(budget, src.ID) }()
	defer func() { cancel(); <-done }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("logo pass never began")
	}
	channels := get[LiveChannelPage](t, s, owner, "/v1/live/channels").Items
	if len(channels) != 2 || channels[0].Name != "Updated 1" {
		t.Fatalf("logos delayed the channel rows: %+v", channels)
	}
	path := "/v1/live/guide?from=" + s.now().Add(-time.Hour).UTC().Format(time.RFC3339) + "&to=" + s.now().Add(time.Hour).UTC().Format(time.RFC3339)
	guide := get[LiveGuide](t, s, owner, path)
	if len(guide.Items) == 0 || len(guide.Items[0].Programmes) == 0 || guide.Items[0].Programmes[0].Title != "Updated" {
		t.Fatal("logos delayed the new guide")
	}
	// End the logo budget while its first request is pending. The remaining URL
	// is left for the next refresh; no sleep or 60-second test timeout is needed.
	cancel()
	select {
	case err := <-done:
		done <- err
	case <-time.After(5 * time.Second):
		t.Fatal("the logo pass ignored its budget")
	}
	if logos.Load() != 1 {
		t.Fatalf("budget tried remaining logos: %d", logos.Load())
	}
}
