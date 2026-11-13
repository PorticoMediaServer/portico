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
	"time"
)

func size(t *testing.T, data []byte) (int, int, string) {
	t.Helper()
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return config.Width, config.Height, format
}

// A picture is stored in sharp small/large and ambient sizes when it arrives, and a screen is served the
// stored size that fits what it asked for: nothing is made when it asks. A name that is not a
// picture's is not found and never becomes a path.
func TestAPictureIsStoredInThreeSizesAndServedFromThem(t *testing.T) {
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
	if files, _ := filepath.Glob(filepath.Join(dir, "artwork", "00", "*")); len(files) != 3 {
		t.Errorf("%d files kept for one picture, want its three sizes", len(files))
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

// A picture nothing named when the sweep read the rows, but that is stored again while the
// sweep runs (the same bytes for another title, or a provider's picture coming back), is kept:
// the row that names it again was written after the sweep looked.
func TestASweepKeepsAPictureStoredAgainWhileItRuns(t *testing.T) {
	s := New(t.TempDir())
	var file bytes.Buffer
	png.Encode(&file, image.NewRGBA(image.Rect(0, 0, 4, 6)))
	const kept, gone = "00aa11bb22cc33dd", "ffee11bb22cc33dd"
	old := time.Now().Add(-2 * time.Hour)
	for _, id := range []string{kept, gone} {
		if err := s.Put(id, file.Bytes()); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(s.place(id, "small"), old, old); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := s.Sweep(time.Now().Add(-time.Hour), func(func(string) error) error {
		return s.Put(kept, file.Bytes()) // no row named either when the rows were read
	})
	if err != nil || removed != 1 {
		t.Fatalf("removed %d, %v", removed, err)
	}
	if _, _, err := s.Read(kept, 0, 0); err != nil {
		t.Fatalf("the picture stored again was taken: %v", err)
	}
	if _, _, err := s.Read(gone, 0, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the unused picture stayed: %v", err)
	}
}

// Existing pictures gain the same tiny PNG once, without changing their sharp copies.
func TestAmbientIsPreblurredAndOlderPicturesGetAStoredCopy(t *testing.T) {
	s := New(t.TempDir())
	const id = "1234567890abcdef"
	source := image.NewNRGBA(image.Rect(0, 0, 128, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			source.SetNRGBA(x, y, color.NRGBA{R: 255, A: 255})
		}
	}
	var input bytes.Buffer
	if err := png.Encode(&input, source); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(id, input.Bytes()); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(s.place(id, AmbientVariant))
	if err != nil {
		t.Fatal("arrival did not precompute ambient:", err)
	}
	if w, h, kind := size(t, stored); w != 64 || h != 32 || kind != "png" || len(stored) > 20000 {
		t.Fatalf("ambient: %dx%d %s, %d bytes", w, h, kind, len(stored))
	}
	decoded, _, err := image.Decode(bytes.NewReader(stored))
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, alpha := decoded.At(33, 16).RGBA()
	if alpha == 0 || alpha == 65535 {
		t.Fatalf("ambient did not blur the transparent edge: %d", alpha)
	}
	sharp, _, err := s.Read(id, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a picture stored by the old build: just its small and large files.
	if err := os.Remove(s.place(id, AmbientVariant)); err != nil {
		t.Fatal(err)
	}
	rebuilt, contentType, err := s.ReadAmbient(id)
	if err != nil || contentType != "image/png" {
		t.Fatalf("legacy ambient: %s, %v", contentType, err)
	}
	if !bytes.Equal(stored, rebuilt) {
		t.Fatal("lossless legacy copy changed the ambient output")
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(s.place(id, AmbientVariant), old, old); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ReadAmbient(id); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(s.place(id, AmbientVariant))
	if err != nil || !info.ModTime().Equal(old) {
		t.Fatalf("warm read rebuilt ambient: %v", err)
	}
	after, _, _ := s.Read(id, 1, 1)
	if !bytes.Equal(sharp, after) {
		t.Fatal("ambient altered the sharp picture")
	}
	removed, err := s.Sweep(time.Now().Add(time.Hour), func(func(string) error) error { return nil })
	if err != nil || removed != 1 {
		t.Fatalf("sweep: %d, %v", removed, err)
	}
	if _, err := os.Stat(s.place(id, AmbientVariant)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("sweep left orphan ambient:", err)
	}
}

// The native wash adds colour once, preserves greys, and never changes the web's v1 bytes.
func TestAmbientV2SaturationLeavesV1Unchanged(t *testing.T) {
	s := New(t.TempDir())
	for i, colour := range []color.RGBA{{96, 128, 160, 255}, {128, 128, 128, 255}} {
		id := []string{"1111111111111111", "2222222222222222"}[i]
		source := image.NewRGBA(image.Rect(0, 0, 8, 8))
		for y := 0; y < 8; y++ {
			for x := 0; x < 8; x++ {
				source.SetRGBA(x, y, colour)
			}
		}
		var input bytes.Buffer
		if err := png.Encode(&input, source); err != nil {
			t.Fatal(err)
		}
		if err := s.Put(id, input.Bytes()); err != nil {
			t.Fatal(err)
		}
		v1, _, err := s.ReadAmbient(id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(s.place(id, AmbientVariantV2)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("v2 must be lazy", err)
		}
		v2, kind, err := s.ReadAmbientV2(id)
		if err != nil || kind != "image/png" {
			t.Fatalf("v2: %s %v", kind, err)
		}
		if i == 1 && !bytes.Equal(v1, v2) {
			t.Fatal("v2 changed a grey picture")
		}
		if i == 0 {
			span := func(data []byte) uint32 {
				img, _, err := image.Decode(bytes.NewReader(data))
				if err != nil {
					t.Fatal(err)
				}
				r, g, b, _ := img.At(0, 0).RGBA()
				return max(r, g, b) - min(r, g, b)
			}
			if bytes.Equal(v1, v2) || span(v2) <= span(v1) {
				t.Fatal("v2 did not increase colour saturation")
			}
		}
		after, _, err := s.ReadAmbient(id)
		if err != nil || !bytes.Equal(v1, after) {
			t.Fatal("v2 changed v1's stored bytes", err)
		}
		cached, _, err := s.ReadAmbientV2(id)
		if err != nil || !bytes.Equal(v2, cached) {
			t.Fatal("v2 changed on a warm read", err)
		}
	}
}
