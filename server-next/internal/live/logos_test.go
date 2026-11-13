package live

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/artwork"
)

func TestChannelLogoFromAPublicSourceCannotReachLoopback(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer target.Close()
	client := logoClient("playlist", LiveSourceConfig{PlaylistURL: "https://public-source.invalid/channels"})
	defer client.CloseIdleConnections()
	service := &Service{pictures: artwork.New(t.TempDir())}
	if _, err := service.channelLogo(context.Background(), client, target.URL); err == nil || hits.Load() != 0 {
		t.Fatalf("loopback was reached: %v, hits=%d", err, hits.Load())
	}
}

func TestChannelLogoRedirectCannotReachAnotherPrivateHost(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer target.Close()
	// The first hop stands in at the owner's source host; localhost is a different
	// destination and must pass the real guarded dial, including on a redirect.
	origin := httptest.NewServer(http.RedirectHandler(strings.Replace(target.URL, "127.0.0.1", "localhost", 1), http.StatusFound))
	defer origin.Close()
	client := logoClient("playlist", LiveSourceConfig{PlaylistURL: origin.URL + "/playlist"})
	defer client.CloseIdleConnections()
	service := &Service{pictures: artwork.New(t.TempDir())}
	if _, err := service.channelLogo(context.Background(), client, origin.URL); err == nil || hits.Load() != 0 {
		t.Fatalf("private redirect was reached: %v, hits=%d", err, hits.Load())
	}
}

func TestChannelLogoAllowsTheLocalTunersOwnHost(t *testing.T) {
	var picture bytes.Buffer
	if err := png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 32, 32))); err != nil {
		t.Fatal(err)
	}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(picture.Bytes()) }))
	defer origin.Close()
	client := logoClient("hdhomerun", LiveSourceConfig{TunerURL: origin.URL})
	defer client.CloseIdleConnections()
	service := &Service{pictures: artwork.New(t.TempDir())}
	hash, err := service.channelLogo(context.Background(), client, origin.URL+"/logo.png")
	if err != nil || hash == "" {
		t.Fatalf("the tuner's own logo was refused: %s %v", hash, err)
	}
}
