package artwork

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func size(t *testing.T, data []byte) (int, int, string) {
	t.Helper()
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return config.Width, config.Height, format
}

// A picture is stored in a small and a large size when it arrives, and a screen is served the
// stored size that fits what it asked for: nothing is made when it asks. A name that is not a
// picture's is not found and never becomes a path.
func TestAPictureIsStoredInTwoSizesAndServedFromThem(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	poster := image.NewRGBA(image.Rect(0, 0, 1000, 1500))
	for n := range poster.Pix {
		poster.Pix[n] = uint8(n)
	}
	for n := 3; n < len(poster.Pix); n += 4 {
		poster.Pix[n] = 255
	}
	var file bytes.Buffer
	jpeg.Encode(&file, poster, nil)
	const id = "00aa11bb22cc33dd"
	if err := s.Put(id, file.Bytes()); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ w, h, width, height int }{{0, 0, 1000, 1500}, {300, 0, 533, 800}, {533, 800, 533, 800}, {534, 0, 1000, 1500},
		{0, 801, 1000, 1500}, {5000, 0, 1000, 1500}} {
		data, kind, err := s.Read(id, c.w, c.h)
		if err != nil || kind != "image/jpeg" {
			t.Fatalf("asked for %dx%d: %s, %v", c.w, c.h, kind, err)
		}
		if w, h, _ := size(t, data); w != c.width || h != c.height {
			t.Errorf("asked for %dx%d: served %dx%d, want %dx%d", c.w, c.h, w, h, c.width, c.height)
		}
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "artwork", "00", "*")); len(files) != 2 {
		t.Errorf("%d files kept for one picture, want its two sizes", len(files))
	}
	os.WriteFile(filepath.Join(dir, "secret"), []byte("x"), 0o600)
	for _, name := range []string{"00aa11bb22cc33de", "../secret", "..%2fsecret", "00AA11BB22CC33DD", ""} {
		if _, _, err := s.Read(name, 0, 0); !errors.Is(err, ErrNotFound) {
			t.Errorf("reading %q: %v, want not found", name, err)
		}
	}
}

// A large picture is made smaller to each size and never larger; a transparent one stays PNG;
// a GIF's first frame and a WebP are read; a profile picture has its own two sizes; HEIC and
// what is not a picture are refused.
func TestPicturesOfEachKindAreNormalised(t *testing.T) {
	s := New(t.TempDir())
	logo := image.NewNRGBA(image.Rect(0, 0, 3000, 2000))
	logo.Set(10, 10, color.NRGBA{255, 0, 0, 255})
	var file bytes.Buffer
	png.Encode(&file, logo)
	if w, h, err := s.PutSized("11aa11bb22cc33dd", file.Bytes(), Artwork); err != nil || w != 1920 || h != 1280 {
		t.Fatalf("a large transparent picture: %dx%d, %v", w, h, err)
	}
	small, kind, _ := s.Read("11aa11bb22cc33dd", 100, 0)
	if w, h, format := size(t, small); w != 800 || h != 533 || format != "png" || kind != "image/png" {
		t.Errorf("its small size: %dx%d %s", w, h, format)
	}

	frames := &gif.GIF{Image: []*image.Paletted{image.NewPaletted(image.Rect(0, 0, 40, 30), color.Palette{color.Black, color.White})}, Delay: []int{0}}
	file.Reset()
	gif.EncodeAll(&file, frames)
	if w, h, err := s.PutSized("22aa11bb22cc33dd", file.Bytes(), ProfilePicture); err != nil || w != 40 || h != 30 {
		t.Errorf("a GIF: %dx%d, %v", w, h, err)
	}
	// A 1 by 1 lossless WebP.
	webp, _ := base64.StdEncoding.DecodeString("UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA==")
	if w, h, err := s.PutSized("33aa11bb22cc33dd", webp, ProfilePicture); err != nil || w != 1 || h != 1 {
		t.Errorf("a WebP: %dx%d, %v", w, h, err)
	}
	face := image.NewRGBA(image.Rect(0, 0, 1000, 1000))
	for n := range face.Pix {
		face.Pix[n] = 200
	}
	file.Reset()
	jpeg.Encode(&file, face, nil)
	if w, h, err := s.PutSized("44aa11bb22cc33dd", file.Bytes(), ProfilePicture); err != nil || w != 512 || h != 512 {
		t.Errorf("a profile picture's large size: %dx%d, %v", w, h, err)
	}
	if data, _, _ := s.Read("44aa11bb22cc33dd", 64, 64); data != nil {
		if w, _, _ := size(t, data); w != 160 {
			t.Errorf("a profile picture's small size is %d wide, want 160", w)
		}
	}
	heic := append([]byte{0, 0, 0, 24}, []byte("ftypheic\x00\x00\x00\x00mif1heic")...)
	for name, data := range map[string][]byte{"HEIC": heic, "text": []byte("not a picture at all")} {
		if err := s.Put("55aa11bb22cc33dd", data); !errors.Is(err, ErrNotAPicture) {
			t.Errorf("%s: %v, want refused", name, err)
		}
	}
}
