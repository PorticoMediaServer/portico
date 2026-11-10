package api

import (
	"bytes"
	"image"
	"image/jpeg"
	"net/http/httptest"
	"testing"
)

// The address an item gives for its poster can be fetched as it is, with no token, at the
// size asked for; a picture the server does not hold is not found.
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
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/jpeg" || err != nil || got.Width != 320 || got.Height != 480 {
		t.Fatalf("the poster at w=300: %d %s %dx%d %v", w.Code, w.Header().Get("Content-Type"), got.Width, got.Height, err)
	}
	if w := fetch(address + "?w=wide"); w.Code != 400 {
		t.Errorf("a size that is not a number answered %d", w.Code)
	}
	if w := fetch("/v1/images/..%2Fmain.db"); w.Code != 404 {
		t.Errorf("a name that is not a picture's answered %d", w.Code)
	}
}
