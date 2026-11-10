package artwork

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
)

// A stored poster is served whole, or at the first listed width that fits what a screen asked
// for, and a name that is not a picture's is not found and never becomes a path.
func TestAPictureIsServedAtTheSizeAsked(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	poster := image.NewRGBA(image.Rect(0, 0, 1000, 1500))
	for n := range poster.Pix {
		poster.Pix[n] = uint8(n)
	}
	poster.Set(0, 0, color.White)
	var file bytes.Buffer
	jpeg.Encode(&file, poster, nil)
	const id = "00aa11bb22cc33dd"
	if err := s.Put(id, file.Bytes()); err != nil {
		t.Fatal(err)
	}
	whole, kind, err := s.Read(id, 0, 0)
	if err != nil || kind != "image/jpeg" || !bytes.Equal(whole, file.Bytes()) {
		t.Fatalf("the whole picture: %d bytes of %s, %v", len(whole), kind, err)
	}
	for _, c := range []struct{ w, h, width, height int }{{300, 0, 320, 480}, {0, 240, 160, 240}, {320, 100, 160, 240}, {5000, 0, 1000, 1500}} {
		data, _, err := s.Read(id, c.w, c.h)
		if err != nil {
			t.Fatal(err)
		}
		got, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || got.Width != c.width || got.Height != c.height {
			t.Errorf("asked to fit %dx%d: got %dx%d (%v), want %dx%d", c.w, c.h, got.Width, got.Height, err, c.width, c.height)
		}
	}
	if copies, _ := filepath.Glob(filepath.Join(dir, "cache", "artwork", "00", "*")); len(copies) != 2 {
		t.Errorf("%d smaller copies were kept, want one for each of the two sizes served", len(copies))
	}
	os.WriteFile(filepath.Join(dir, "secret"), []byte("x"), 0o600)
	for _, name := range []string{"00aa11bb22cc33de", "../secret", "..%2fsecret", "00AA11BB22CC33DD", ""} {
		if _, _, err := s.Read(name, 0, 0); !errors.Is(err, ErrNotFound) {
			t.Errorf("reading %q: %v, want not found", name, err)
		}
	}
}
