// Package artwork keeps pictures as files, named by the hash the database knows them by. Each
// picture is stored in small, large and preblurred ambient sizes when it arrives: a provider's picture
// downloaded, a picture beside the media read, an upload accepted. A screen is served the
// stored size that fits what it asked for. Older pictures gain ambient on first use.
package artwork

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif" // the first frame of a GIF
	"image/jpeg"
	"image/png"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "golang.org/x/image/webp"

	"github.com/PorticoMediaServer/portico/server-next/internal/atomicfile"
)

// ErrNotFound: no picture is stored under that name.
var ErrNotFound = errors.New("no such picture")

// ErrNotAPicture: the bytes are not a JPEG, PNG, GIF or WebP this server can read, or the
// picture is larger than any it decodes.
var ErrNotAPicture = errors.New("not a picture this server can read")

// Sizes are the two sizes a kind of picture is kept in: the longest edge in pixels of each,
// and the number of bytes its JPEG aims to stay within.
type Sizes struct {
	Small, Large       int
	SmallCap, LargeCap int
}

// The sizes, as the old server kept them: library artwork small for cards and lists and large
// for a full screen; profile pictures small for a list of profiles and large for a profile's
// own page.
var (
	Artwork        = Sizes{Small: 800, Large: 1920, SmallCap: 150_000, LargeCap: 500_000}
	ProfilePicture = Sizes{Small: 160, Large: 512, SmallCap: 30_000, LargeCap: 120_000}
)

// qualities are the JPEG qualities tried in turn; the first that stays within the size's cap
// is kept, and the last is kept whatever its size. A picture is never made smaller in pixels
// to save bytes.
var qualities = []int{90, 82, 75}

// Store is the folder of pictures inside the server's data folder.
type Store struct{ dir string }

// New returns the store inside the server's data folder.
func New(dataDir string) *Store { return &Store{filepath.Join(dataDir, "artwork")} }

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

func (s *Store) place(id, size string) string { return filepath.Join(s.dir, id[:2], id+"."+size) }

// maxPixels is the largest picture decoded. Decoded, a picture takes four or eight bytes a
// pixel, so this is at most about 200 MB.
const maxPixels = 24_000_000

// decoding holds how many pictures are being decoded at once. A scan of a new library brings
// many at once, each tens of megabytes decoded: without a limit that is the server's whole memory.
var decoding = make(chan struct{}, 2)

// Put stores library artwork under id in its two sizes. The same id is the same picture: one
// already stored is left as it is.
func (s *Store) Put(id string, data []byte) error {
	_, _, err := s.PutSized(id, data, Artwork)
	return err
}

// PutSized stores a picture under id in the two given sizes and answers the large one's
// width and height. ErrNotAPicture refuses what cannot be read. A picture already stored is
// marked as just stored, so a sweep running now does not take it before the row that names it
// again is written.
func (s *Store) PutSized(id string, data []byte, sizes Sizes) (width, height int, err error) {
	if !ValidID(id) {
		return 0, 0, fmt.Errorf("artwork: %q cannot name a picture", id)
	}
	if w, h, err := s.Stored(id); err == nil {
		now := time.Now()
		return w, h, os.Chtimes(s.place(id, "small"), now, now)
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width*config.Height > maxPixels {
		return 0, 0, ErrNotAPicture
	}
	decoding <- struct{}{}
	defer func() { <-decoding }()
	source, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return 0, 0, ErrNotAPicture
	}
	from := image.NewRGBA(image.Rect(0, 0, config.Width, config.Height))
	draw.Draw(from, from.Bounds(), source, source.Bounds().Min, draw.Src)
	if err := os.MkdirAll(filepath.Dir(s.place(id, "small")), 0o755); err != nil {
		return 0, 0, err
	}
	// Write the large and ambient sizes before small: small marks a completed arrival.
	for _, size := range []struct {
		name      string
		edge, cap int
	}{{"large", sizes.Large, sizes.LargeCap}, {"small", sizes.Small, sizes.SmallCap}} {
		scaled := shrink(from, size.edge)
		if size.name == "large" {
			width, height = scaled.Bounds().Dx(), scaled.Bounds().Dy()
		}
		if size.name == "small" {
			data, err := ambient(scaled)
			if err != nil {
				return 0, 0, err
			}
			if err := atomicfile.Write(s.place(id, AmbientVariant), data, 0o644); err != nil {
				return 0, 0, err
			}
		}
		encoded, err := encode(scaled, size.cap)
		if err != nil {
			return 0, 0, err
		}
		if err := atomicfile.Write(s.place(id, size.name), encoded, 0o644); err != nil {
			return 0, 0, err
		}
	}
	return width, height, nil
}

// encode makes a picture's file: lossless PNG when it has transparency, otherwise JPEG at the
// first quality within the cap.
func encode(picture *image.RGBA, limit int) ([]byte, error) {
	var out bytes.Buffer
	if !picture.Opaque() {
		err := png.Encode(&out, picture)
		return out.Bytes(), err
	}
	for _, quality := range qualities {
		out.Reset()
		if err := jpeg.Encode(&out, picture, &jpeg.Options{Quality: quality}); err != nil {
			return nil, err
		}
		if out.Len() <= limit {
			break
		}
	}
	return out.Bytes(), nil
}

// Stored answers the width and height of a stored picture's large size.
func (s *Store) Stored(id string) (width, height int, err error) {
	if !ValidID(id) {
		return 0, 0, ErrNotFound
	}
	if _, err := os.Stat(s.place(id, "small")); err != nil {
		return 0, 0, ErrNotFound
	}
	f, err := os.Open(s.place(id, "large"))
	if err != nil {
		return 0, 0, ErrNotFound
	}
	defer f.Close()
	config, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0, err
	}
	return config.Width, config.Height, nil
}

// Read returns a stored picture's bytes and content type: the small size when it is at least
// as large as w by h asks (0 means no limit that way), else the large size.
func (s *Store) Read(id string, w, h int) (data []byte, contentType string, err error) {
	if !ValidID(id) {
		return nil, "", ErrNotFound
	}
	data, err = os.ReadFile(s.place(id, "small"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (w <= 0 && h <= 0) || w > config.Width || h > config.Height {
		if data, err = os.ReadFile(s.place(id, "large")); err != nil {
			return nil, "", err
		}
	}
	return data, http.DetectContentType(data), nil
}

// shrink scales a picture down so its longest edge is at most edge, averaging the pixels each
// new pixel covers. A picture already that small is kept as it is; none is enlarged.
func shrink(from *image.RGBA, edge int) *image.RGBA {
	bounds := from.Bounds()
	longest := max(bounds.Dx(), bounds.Dy())
	if longest <= edge {
		return from
	}
	width, height := max(1, bounds.Dx()*edge/longest), max(1, bounds.Dy()*edge/longest)
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
// stored since the time is kept, as one just stored may not be recorded yet. All three sizes go. It returns how many pictures it removed.
func (s *Store) Sweep(before time.Time, used func(yield func(id string) error) error) (int, error) {
	prefixes, err := os.ReadDir(s.dir)
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
			entries, err := os.ReadDir(filepath.Join(s.dir, prefixes[next].Name()))
			next++
			if err != nil {
				return err
			}
			for _, e := range entries {
				info, err := e.Info()
				// One entry a picture: its small size, the last file to be written.
				id, small := strings.CutSuffix(e.Name(), ".small")
				if err == nil && small && ValidID(id) && info.ModTime().Before(before) {
					stored = append(stored, id)
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
	removed := 0
	for _, id := range unused {
		// Stored again while the sweep ran: it may be named by a row the sweep did not see.
		if info, err := os.Stat(s.place(id, "small")); err != nil || !info.ModTime().Before(before) {
			continue
		}
		removed++
		for _, size := range []string{"small", "large", AmbientVariant} {
			if err := os.Remove(s.place(id, size)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return 0, err
			}
		}
	}
	return removed, nil
}
