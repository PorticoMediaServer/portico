package artwork

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io/fs"
	"os"

	xdraw "golang.org/x/image/draw"

	"github.com/PorticoMediaServer/portico/server-next/internal/atomicfile"
)

// AmbientVariant is the clients' tiny, preblurred PNG, separate from sharp sizes.
const AmbientVariant = "ambient-v1"

// ambient uses the original ambient-v1 treatment: at most 64 pixels on the long
// edge, never enlarged, then three separable box-blur passes of radius four.
func ambient(source image.Image) ([]byte, error) {
	w, h := source.Bounds().Dx(), source.Bounds().Dy()
	if edge := max(w, h); edge > 64 {
		w, h = max(1, w*64/edge), max(1, h*64/edge)
	}
	small := image.NewNRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(small, small.Bounds(), source, source.Bounds(), draw.Src, nil)
	a := image.NewRGBA(small.Bounds())
	draw.Draw(a, a.Bounds(), small, small.Bounds().Min, draw.Src)
	b := image.NewRGBA(a.Bounds())
	for pass := 0; pass < 3; pass++ {
		ambientBoxPass(b, a, true)
		ambientBoxPass(a, b, false)
	}
	var out bytes.Buffer
	err := png.Encode(&out, a)
	return out.Bytes(), err
}

func ambientBoxPass(dst, src *image.RGBA, horizontal bool) {
	const radius = 4
	for y := 0; y < src.Rect.Dy(); y++ {
		for x := 0; x < src.Rect.Dx(); x++ {
			var r, g, b, a uint32
			for offset := -radius; offset <= radius; offset++ {
				sx, sy := x, y
				if horizontal {
					sx = min(src.Rect.Dx()-1, max(0, x+offset))
				} else {
					sy = min(src.Rect.Dy()-1, max(0, y+offset))
				}
				c := src.RGBAAt(sx, sy)
				r, g, b, a = r+uint32(c.R), g+uint32(c.G), b+uint32(c.B), a+uint32(c.A)
			}
			const n = 2*radius + 1
			dst.SetRGBA(x, y, color.RGBA{uint8((r + n/2) / n), uint8((g + n/2) / n), uint8((b + n/2) / n), uint8((a + n/2) / n)})
		}
	}
}

// ReadAmbient serves a stored variant. Pictures from before this variant existed
// get it once from their stored small copy, then use the same file on every read.
func (s *Store) ReadAmbient(id string) ([]byte, string, error) {
	if !ValidID(id) {
		return nil, "", ErrNotFound
	}
	place := s.place(id, AmbientVariant)
	data, err := os.ReadFile(place)
	if !errors.Is(err, fs.ErrNotExist) {
		return data, "image/png", err
	}
	decoding <- struct{}{}
	defer func() { <-decoding }()
	// A concurrent arrival or first request may have written it while we waited.
	if data, err = os.ReadFile(place); !errors.Is(err, fs.ErrNotExist) {
		return data, "image/png", err
	}
	data, _, err = s.Read(id, 1, 1)
	if err != nil {
		return nil, "", err
	}
	source, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", err
	}
	data, err = ambient(source)
	if err == nil {
		err = atomicfile.Write(place, data, 0o644)
	}
	return data, "image/png", err
}
