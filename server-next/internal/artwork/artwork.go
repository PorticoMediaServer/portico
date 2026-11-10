// Package artwork keeps pictures as files, named by the hash the database knows them by,
// and serves them at the size a screen asks for.
package artwork

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

	"github.com/PorticoMediaServer/portico/server-next/internal/atomicfile"
)

// ErrNotFound: no picture is stored under that name.
var ErrNotFound = errors.New("no such picture")

// Store is the folder of pictures inside the server's data folder. Originals are kept in
// "artwork". Smaller copies are made when first asked for and kept in "cache/artwork",
// which can be deleted at any time.
type Store struct {
	originals, sized string
}

// New returns the store inside the server's data folder.
func New(dataDir string) *Store {
	return &Store{filepath.Join(dataDir, "artwork"), filepath.Join(dataDir, "cache", "artwork")}
}

// ValidID says whether id can name a picture: 16 to 64 lower-case hex digits. Nothing else
// is ever joined to a folder, so a request cannot reach outside the store.
func ValidID(id string) bool {
	if len(id) < 16 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func place(dir, id, suffix string) string { return filepath.Join(dir, id[:2], id+suffix) }

// Put stores a picture under id, replacing nothing if it is already there: the same id is
// the same picture.
func (s *Store) Put(id string, data []byte) error {
	if !ValidID(id) {
		return fmt.Errorf("artwork: %q cannot name a picture", id)
	}
	path := place(s.originals, id, "")
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return atomicfile.Write(path, data, 0o644)
}

// widths are the sizes a smaller copy is made at. A request is served the first that is at
// least as wide as it asked, so a picture has at most this many copies however it is asked for.
var widths = []int{160, 240, 320, 480, 720, 1080, 1920}

// maxPixels is the largest picture that is decoded to make a smaller copy. A larger one is
// served as it is.
const maxPixels = 50_000_000

// Read returns a picture's bytes and content type, made to fit inside w by h when either is
// given (0 means no limit that way). A picture is never enlarged, and one this server cannot
// decode is served whole.
func (s *Store) Read(id string, w, h int) (data []byte, contentType string, err error) {
	if !ValidID(id) {
		return nil, "", ErrNotFound
	}
	original, err := os.ReadFile(place(s.originals, id, ""))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	contentType = http.DetectContentType(original)
	config, format, err := image.DecodeConfig(bytes.NewReader(original))
	if err != nil || (w <= 0 && h <= 0) || config.Width*config.Height > maxPixels || config.Height == 0 {
		return original, contentType, nil
	}
	want := config.Width
	if w > 0 {
		want = min(want, w)
	}
	if h > 0 {
		want = min(want, (h*config.Width+config.Height-1)/config.Height)
	}
	width := 0
	for _, step := range widths {
		if step >= want {
			width = step
			break
		}
	}
	if width == 0 || width >= config.Width {
		return original, contentType, nil
	}
	suffix, sizedType := fmt.Sprintf(".w%d.jpg", width), "image/jpeg"
	if format == "png" {
		suffix, sizedType = fmt.Sprintf(".w%d.png", width), "image/png"
	}
	path := place(s.sized, id, suffix)
	if sized, err := os.ReadFile(path); err == nil {
		return sized, sizedType, nil
	}
	source, _, err := image.Decode(bytes.NewReader(original))
	if err != nil {
		return original, contentType, nil
	}
	small := shrink(source, width)
	var out bytes.Buffer
	if format == "png" {
		err = png.Encode(&out, small)
	} else {
		err = jpeg.Encode(&out, small, &jpeg.Options{Quality: 82})
	}
	if err != nil {
		return nil, "", err
	}
	// Keeping the copy is a saving, not a need: a full disk still serves the picture.
	if os.MkdirAll(filepath.Dir(path), 0o755) == nil {
		atomicfile.Write(path, out.Bytes(), 0o644)
	}
	return out.Bytes(), sizedType, nil
}

// shrink scales a picture down to the given width by averaging the pixels each new pixel covers.
func shrink(source image.Image, width int) *image.RGBA {
	bounds := source.Bounds()
	from := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(from, from.Bounds(), source, bounds.Min, draw.Src)
	height := max(1, bounds.Dy()*width/bounds.Dx())
	to := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		top, bottom := y*bounds.Dy()/height, max((y+1)*bounds.Dy()/height, y*bounds.Dy()/height+1)
		for x := range width {
			left, right := x*bounds.Dx()/width, max((x+1)*bounds.Dx()/width, x*bounds.Dx()/width+1)
			var sum [4]int
			for sy := top; sy < bottom; sy++ {
				row := from.Pix[sy*from.Stride+left*4 : sy*from.Stride+right*4]
				for n, value := range row {
					sum[n%4] += int(value)
				}
			}
			count := (bottom - top) * (right - left)
			at := to.Pix[y*to.Stride+x*4:]
			for n := range 4 {
				at[n] = uint8(sum[n] / count)
			}
		}
	}
	return to
}
