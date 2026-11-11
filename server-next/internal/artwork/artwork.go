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
	"time"

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
// served as it is. Decoded, a picture takes eight bytes a pixel here, so this is about 200 MB.
const maxPixels = 24_000_000

// decoding holds how many pictures are being made smaller at once. The first screenful of a
// new library asks for forty posters in the same moment, each tens of megabytes once decoded,
// and the route takes no sign-in: without a limit that is the server's whole memory.
var decoding = make(chan struct{}, 2)

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
	decoding <- struct{}{}
	defer func() { <-decoding }()
	// Someone else may have made this copy while this request waited its turn.
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

// Sweep removes the pictures nothing uses any more: every one stored before a time whose id
// used does not name. used calls yield with every id in use, in ascending order; a picture
// stored since the time is kept, as one just stored may not be recorded yet. Smaller copies
// go with their picture. It returns how many pictures it removed.
func (s *Store) Sweep(before time.Time, used func(yield func(id string) error) error) (int, error) {
	prefixes, err := os.ReadDir(s.originals)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var stored []string // the pictures of the folders read so far, not yet compared
	next := 0           // the next folder to read
	var unused []string
	// fill reads folders until a picture at or after id is waiting, or there are no more.
	fill := func(id string) error {
		for next < len(prefixes) && (len(stored) == 0 || stored[len(stored)-1] < id) {
			entries, err := os.ReadDir(filepath.Join(s.originals, prefixes[next].Name()))
			next++
			if err != nil {
				return err
			}
			for _, e := range entries {
				info, err := e.Info()
				if err == nil && ValidID(e.Name()) && info.ModTime().Before(before) {
					stored = append(stored, e.Name())
				}
			}
		}
		return nil
	}
	err = used(func(id string) error {
		if err := fill(id); err != nil {
			return err
		}
		for len(stored) > 0 && stored[0] <= id {
			if stored[0] != id {
				unused = append(unused, stored[0])
			}
			stored = stored[1:]
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	if err := fill("~"); err != nil { // after every id
		return 0, err
	}
	unused = append(unused, stored...)
	for _, id := range unused {
		if err := os.Remove(place(s.originals, id, "")); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return 0, err
		}
		copies, _ := filepath.Glob(place(s.sized, id, ".w*"))
		for _, c := range copies {
			os.Remove(c)
		}
	}
	return len(unused), nil
}
