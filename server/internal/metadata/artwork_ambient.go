package metadata

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"

	"github.com/PorticoMediaServer/portico/server/internal/imagework"
)

// ArtworkAmbientEdge is an explicit preblurred representation, never a
// foreground width bucket. HTTP selects it only with size=ambient-v1.
const ArtworkAmbientEdge = 64
const artworkAmbientBytes = 20_000
const artworkAmbientRevision = "ambient-v1"

type ambientContextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r ambientContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

// Input is an already ingested display object. Bounds are checked before
// allocating decoded pixels, and the caller holds the shared imagework gate.
func encodeAmbientArtwork(ctx context.Context, raw []byte) ([]byte, int, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, 0, err
	}
	if len(raw) < 1 || len(raw) > artworkMaxBytes {
		return nil, 0, 0, ErrRepairInput
	}
	cfg, format, err := image.DecodeConfig(ambientContextReader{ctx, bytes.NewReader(raw)})
	if ctx.Err() != nil {
		return nil, 0, 0, ctx.Err()
	}
	if err != nil || (format != "jpeg" && format != "png") || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 10000 || cfg.Height > 10000 || int64(cfg.Width)*int64(cfg.Height) > 24000000 {
		return nil, 0, 0, ErrRepairInput
	}
	decoded, _, err := image.Decode(ambientContextReader{ctx, bytes.NewReader(raw)})
	if err != nil {
		return nil, 0, 0, err
	}
	if decoded.Bounds().Dx() != cfg.Width || decoded.Bounds().Dy() != cfg.Height {
		return nil, 0, 0, ErrRepairInput
	}
	ambient, err := encodeAmbientImage(ctx, decoded)
	if err != nil {
		return nil, 0, 0, err
	}
	return ambient.data, ambient.width, ambient.height, nil
}

// encodeAmbientImage makes the preblurred ambient effect from a decoded
// arrival, in the same pass that encodes its slots.
func encodeAmbientImage(ctx context.Context, decoded image.Image) (artworkEncoded, error) {
	w, h := decoded.Bounds().Dx(), decoded.Bounds().Dy()
	if edge := max(w, h); edge > ArtworkAmbientEdge {
		w, h = max(1, w*ArtworkAmbientEdge/edge), max(1, h*ArtworkAmbientEdge/edge)
	}
	small := image.NewNRGBA(image.Rect(0, 0, w, h))
	if err := imagework.CatmullRom(ctx, small, decoded); err != nil {
		return artworkEncoded{}, err
	}
	a := image.NewRGBA(small.Bounds())
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			a.Set(x, y, small.At(x, y))
		}
	}
	b := image.NewRGBA(a.Bounds())
	for pass := 0; pass < 3; pass++ {
		if err := ambientBoxPass(ctx, b, a, true); err != nil {
			return artworkEncoded{}, err
		}
		if err := ambientBoxPass(ctx, a, b, false); err != nil {
			return artworkEncoded{}, err
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, a); err != nil {
		return artworkEncoded{}, err
	}
	if err := ctx.Err(); err != nil {
		return artworkEncoded{}, err
	}
	if out.Len() > artworkAmbientBytes {
		return artworkEncoded{}, ErrRepairInput
	}
	return artworkEncoded{data: out.Bytes(), mime: "image/png", width: w, height: h}, nil
}

func ambientBoxPass(ctx context.Context, dst, src *image.RGBA, horizontal bool) error {
	const radius = 4
	for y := 0; y < src.Rect.Dy(); y++ {
		if err := ctx.Err(); err != nil {
			return err
		}
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
	return nil
}
