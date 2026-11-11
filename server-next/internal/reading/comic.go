package reading

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	_ "image/gif" // comic pages in GIF can be made smaller too
)

// pageTypes are the pictures a comic's pages may be, by extension.
var pageTypes = map[string]string{".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png", ".gif": "image/gif",
	".webp": "image/webp", ".avif": "image/avif"}

// comic is a comic archive's pages in reading order, and its direction when it says it reads
// right to left.
type comic struct {
	pages     []string
	direction string
}

// readComic lists a comic archive's pictures in natural order ("2" before "10"). Hidden files
// and the folders macOS adds are not pages, and nothing inside an archive inside the archive
// is ever looked at. ComicInfo.xml may say the comic reads right to left.
func readComic(a *archive) (*comic, error) {
	c := &comic{}
	info := ""
	for _, name := range a.names {
		if strings.EqualFold(name, "ComicInfo.xml") {
			info = name
		}
		if _, isPage := pageTypes[strings.ToLower(path.Ext(name))]; !isPage || hidden(name) {
			continue
		}
		// A page too large to send is still listed, so the page numbers stay the book's own;
		// asking for it says it can't be shown.
		c.pages = append(c.pages, name)
	}
	if len(c.pages) == 0 {
		return nil, errDamaged
	}
	slices.SortFunc(c.pages, naturalCompare)
	if info != "" {
		var meta struct {
			Manga string `xml:"Manga"`
		}
		if raw, err := a.read(info, 1<<20); err == nil && decode(raw, &meta) == nil && strings.EqualFold(meta.Manga, "YesAndRightToLeft") {
			c.direction = "rtl"
		}
	}
	return c, nil
}

func hidden(name string) bool {
	for _, segment := range strings.Split(name, "/") {
		if strings.HasPrefix(segment, ".") || segment == "__MACOSX" {
			return true
		}
	}
	return false
}

// naturalCompare orders names as a person would: runs of digits by their value, everything
// else without regard to case.
func naturalCompare(a, b string) int {
	for a != "" && b != "" {
		ad, bd := digitRun(a), digitRun(b)
		if ad > 0 && bd > 0 {
			x, y := strings.TrimLeft(a[:ad], "0"), strings.TrimLeft(b[:bd], "0")
			if c := len(x) - len(y); c != 0 {
				return c
			}
			if c := strings.Compare(x, y); c != 0 {
				return c
			}
			a, b = a[ad:], b[bd:]
			continue
		}
		x, y := strings.ToLower(a[:1]), strings.ToLower(b[:1])
		if c := strings.Compare(x, y); c != 0 {
			return c
		}
		a, b = a[1:], b[1:]
	}
	return len(a) - len(b)
}

func digitRun(s string) int {
	n := 0
	for n < len(s) && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	return n
}

// errPageTooLarge says one page is too large to prepare safely, and errPageDamaged that it
// cannot be read. The rest of the book reads.
var (
	errPageTooLarge = errors.New("This page can't be shown.")
	errPageDamaged  = errors.New("This page can't be shown.")
)

// memoryLimit is the most memory making pages smaller may hold at once, counting each picture
// unpacked, its smaller copy, and that copy until a slow reader has received all of it.
const memoryLimit = 256 << 20

// errBusy says the memory for making pages smaller is all in use and stayed so for
// busyWait: the reader tries the page again shortly.
var errBusy = errors.New("The server is busy. Try again in a moment.")

// busyWait is the longest a page waits for memory to be free. (A variable only for a test.)
var busyWait = 10 * time.Second

// budget counts memory held by page work. A request that would go over it waits for others to
// finish, for busyWait at most; one that could never fit is refused.
type budget struct {
	mu    sync.Mutex
	used  int64
	freed chan struct{}
}

var pageMemory = &budget{freed: make(chan struct{})}

func (b *budget) take(ctx context.Context, n int64) error {
	if n > memoryLimit {
		return errPageTooLarge
	}
	timer := time.NewTimer(busyWait)
	defer timer.Stop()
	for {
		b.mu.Lock()
		if b.used+n <= memoryLimit {
			b.used += n
			b.mu.Unlock()
			return nil
		}
		freed := b.freed
		b.mu.Unlock()
		select {
		case <-freed:
		case <-timer.C:
			return errBusy
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (b *budget) give(n int64) {
	b.mu.Lock()
	b.used -= n
	close(b.freed)
	b.freed = make(chan struct{})
	b.mu.Unlock()
}

// smaller returns a page made no wider than width, or nil when the page is already narrow
// enough or is a kind of picture this server does not redraw, in which case the page is sent
// as it is. Whether the page fits in memory is decided from its header, before it is unpacked.
// The memory the smaller copy holds stays counted until done is called, once it is sent.
func smaller(ctx context.Context, open func() (io.ReadCloser, error), width int) (out []byte, contentType string, done func(), err error) {
	rc, err := open()
	if err != nil {
		return nil, "", nil, err
	}
	config, format, err := image.DecodeConfig(rc)
	rc.Close()
	if err != nil || config.Width <= width || config.Width <= 0 || config.Height <= 0 {
		return nil, "", nil, nil
	}
	height := max(1, config.Height*width/config.Width)
	source, target := int64(config.Width)*int64(config.Height), int64(width)*int64(height)
	// The picture as decoded (up to 8 bytes a pixel, for a 16-bit PNG), the 4-byte copy shrink
	// works from, and the smaller picture.
	need := 12*source + 4*target
	if err := pageMemory.take(ctx, need); err != nil {
		return nil, "", nil, err
	}
	kept := int64(0)
	defer func() { pageMemory.give(need - kept) }()
	if rc, err = open(); err != nil {
		return nil, "", nil, err
	}
	picture, _, err := image.Decode(rc)
	rc.Close()
	if err != nil {
		return nil, "", nil, errPageDamaged
	}
	small := shrink(picture, width)
	picture = nil
	var buf bytes.Buffer
	contentType = "image/jpeg"
	if format == "png" {
		contentType, err = "image/png", png.Encode(&buf, small)
	} else {
		err = jpeg.Encode(&buf, small, &jpeg.Options{Quality: 85})
	}
	if err != nil {
		return nil, "", nil, err
	}
	kept = min(int64(buf.Len()), need)
	return buf.Bytes(), contentType, func() { pageMemory.give(kept) }, nil
}

// shrink scales a picture down to the given width by averaging the pixels each new pixel
// covers. (Copied from internal/artwork.)
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
