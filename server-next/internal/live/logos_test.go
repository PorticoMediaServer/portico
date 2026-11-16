package live

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
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

// SVG logos are common: one is stored as a picture, drawn with its shapes and its text. One
// with something that cannot be drawn faithfully (here a picture inside it) is refused, so
// the channel shows its monogram rather than a wrong logo.
func TestAnSVGChannelLogoIsStoredAsAPicture(t *testing.T) {
	drawings := map[string]string{
		"/logo.svg": `<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg" width="256" height="256" viewBox="0 0 256 256">` +
			`<metadata>made by hand</metadata><rect width="256" height="256" rx="40" fill="#c0392b"/>` +
			`<text x="128" y="160" font-family="Helvetica, Arial, sans-serif" font-size="104" font-weight="700" text-anchor="middle" fill="#ffffff">NN</text></svg>`,
		"/inside.svg": `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" viewBox="0 0 10 10">` +
			`<image width="10" height="10" xlink:href="data:image/png;base64,AAAA"/></svg>`,
	}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		io.WriteString(w, drawings[r.URL.Path])
	}))
	defer origin.Close()
	client := logoClient("hdhomerun", LiveSourceConfig{TunerURL: origin.URL})
	defer client.CloseIdleConnections()
	service := &Service{pictures: artwork.New(t.TempDir())}
	hash, err := service.channelLogo(context.Background(), client, origin.URL+"/logo.svg")
	if err != nil {
		t.Fatalf("the SVG logo was refused: %v", err)
	}
	stored, _, err := service.pictures.Read(hash, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	picture, _, err := image.Decode(bytes.NewReader(stored))
	if err != nil {
		t.Fatal(err)
	}
	b := picture.Bounds()
	if _, _, _, a := picture.At(b.Min.X+1, b.Min.Y+1).RGBA(); a != 0 {
		t.Errorf("the rounded corner is filled")
	}
	if r, g, bl, _ := picture.At(b.Min.X+b.Dx()/10, b.Min.Y+b.Dy()/2).RGBA(); max(r>>8, 0xc0)-min(r>>8, 0xc0) > 2 || max(g>>8, 0x39)-min(g>>8, 0x39) > 2 || max(bl>>8, 0x2b)-min(bl>>8, 0x2b) > 2 {
		t.Errorf("the background is %x %x %x", r>>8, g>>8, bl>>8)
	}
	white := 0
	for y := b.Min.Y + b.Dy()*90/256; y < b.Min.Y+b.Dy()*160/256; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if r, g, bl, _ := picture.At(x, y).RGBA(); r>>8 > 0xf0 && g>>8 > 0xf0 && bl>>8 > 0xf0 {
				white++
			}
		}
	}
	if white < b.Dx()*b.Dy()/100 {
		t.Errorf("the text was not drawn: %d white pixels", white)
	}
	if hash, err := service.channelLogo(context.Background(), client, origin.URL+"/inside.svg"); err == nil {
		t.Errorf("a drawing with a picture inside was stored as %s", hash)
	}
}
