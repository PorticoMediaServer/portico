package api

import (
	"bytes"
	"image"
	"image/jpeg"
	"net/http/httptest"
	"testing"
)

// The address an item gives for its poster can be fetched as it is, with no token, in the
// stored size that fits what was asked for; a picture the server does not hold is not found.
func TestAnItemsPictureCanBeFetched(t *testing.T) {
	s, token, _ := seeded(t)
	library := get[Libraries](t, s, token, "/v1/libraries").Items[0].ID
	var address string
	for _, film := range get[ItemPage](t, s, token, "/v1/items?library="+library+"&kind=movie&limit=200").Items {
		if address = film.Images["poster"]; address != "" {
			break
		}
	}
	if address == "" {
		t.Fatal("no seeded film has a poster")
	}
	fetch := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w
	}
	if w := fetch(address); w.Code != 404 {
		t.Fatalf("a picture the server does not hold answered %d", w.Code)
	}
	var poster bytes.Buffer
	jpeg.Encode(&poster, image.NewRGBA(image.Rect(0, 0, 1000, 1500)), nil)
	if err := s.artwork.Put(address[len("/v1/images/"):], poster.Bytes()); err != nil {
		t.Fatal(err)
	}
	w := fetch(address + "?w=300")
	got, _, err := image.DecodeConfig(w.Body)
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/jpeg" || err != nil || got.Width != 533 || got.Height != 800 {
		t.Fatalf("the poster at w=300: %d %s %dx%d %v", w.Code, w.Header().Get("Content-Type"), got.Width, got.Height, err)
	}
	ambient := fetch(address + "?size=ambient-v1")
	config, format, err := image.DecodeConfig(bytes.NewReader(ambient.Body.Bytes()))
	if ambient.Code != 200 || format != "png" || config.Width != 42 || config.Height != 64 || err != nil || ambient.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("ambient: %d, %dx%d %s %v", ambient.Code, config.Width, config.Height, format, err)
	}
	if ambient.Header().Get("ETag") == w.Header().Get("ETag") || !bytes.Contains([]byte(ambient.Header().Get("Cache-Control")), []byte("immutable")) {
		t.Fatal("ambient did not have its own immutable cache identity")
	}
	conditional := httptest.NewRequest("GET", address+"?size=ambient-v1", nil)
	conditional.Header.Set("If-None-Match", ambient.Header().Get("ETag"))
	cached := httptest.NewRecorder()
	s.ServeHTTP(cached, conditional)
	if cached.Code != 304 {
		t.Fatalf("cached ambient: %d", cached.Code)
	}
	if w := fetch(address + "?size=ambient-v2"); w.Code != 400 {
		t.Fatalf("unknown variant answered %d", w.Code)
	}
	for variant, edge := range map[string]int{"small": 800, "large": 1500} {
		w := fetch(address + "?size=" + variant)
		cfg, _, err := image.DecodeConfig(w.Body)
		if w.Code != 200 || err != nil || cfg.Height != edge {
			t.Fatalf("sharp %s: %d, height %d, %v", variant, w.Code, cfg.Height, err)
		}
	}
	if w := fetch(address + "?w=wide"); w.Code != 400 {
		t.Errorf("a size that is not a number answered %d", w.Code)
	}
	if w := fetch("/v1/images/..%2Fmain.db"); w.Code != 404 {
		t.Errorf("a name that is not a picture's answered %d", w.Code)
	}
}
