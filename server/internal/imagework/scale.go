// Copyright 2015 The Go Authors. All rights reserved.
// Contribution and rounding arithmetic follows golang.org/x/image/draw.
// Use of this source code is governed by the BSD-style license in SCALE_LICENSE.

package imagework

import (
	"context"
	"errors"
	"image"
	"image/color"
	"math"

	"golang.org/x/image/draw"
)

// CatmullRom reproduces x/image/draw's separable Catmull-Rom, Src operation
// into an NRGBA image. Only the horizontally filtered rows needed by the
// current output row are retained. The upstream scaler retains an entire
// destination-width by source-height array of four float64s per pixel.
// This bounds working storage, not the source decoder or destination image.
func CatmullRom(ctx context.Context, dst *image.NRGBA, src image.Image) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	sr, dr := src.Bounds(), dst.Bounds()
	sw, sh, dw, dh := sr.Dx(), sr.Dy(), dr.Dx(), dr.Dy()
	if sw <= 0 || sh <= 0 || dw <= 0 || dh <= 0 {
		return errors.New("invalid image dimensions")
	}
	// Coefficients are optional cached work. Even an extreme source axis does
	// not allocate a contribution table proportional to that axis.
	horizontal := make([]scaleSpan, dw)
	weights := make([][]scaleWeight, dw)
	remaining := scaleWeightBytes / 16
	for x := range horizontal {
		if err := ctx.Err(); err != nil {
			return err
		}
		span, err := makeScaleSpan(ctx, sw, dw, x)
		if err != nil {
			return err
		}
		horizontal[x] = span
		if span.end-span.start <= remaining {
			cached := make([]scaleWeight, 0, span.end-span.start)
			for sx := span.start; sx < span.end; sx++ {
				if weight := span.weight(sx); weight != 0 {
					cached = append(cached, scaleWeight{sx, weight})
				}
			}
			weights[x] = cached
			remaining -= cap(cached)
		}
	}
	rows := min(sh, max(1, scaleRowBytes/(dw*32)))
	cache := make([][4]float64, rows*dw)
	rowIDs := make([]int, rows)
	for i := range rowIDs {
		rowIDs[i] = -1
	}
	accumulator := make([][4]float64, dw)
	read := rgbaReader(src)
	opaqueHorizontal := false
	switch typed := src.(type) {
	case *image.Gray:
		opaqueHorizontal = true
	case *image.YCbCr:
		opaqueHorizontal = typed.SubsampleRatio == image.YCbCrSubsampleRatio444 || typed.SubsampleRatio == image.YCbCrSubsampleRatio422 || typed.SubsampleRatio == image.YCbCrSubsampleRatio420 || typed.SubsampleRatio == image.YCbCrSubsampleRatio440
	}
	var result color.RGBA64
	for y := 0; y < dh; y++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		vertical, err := makeScaleSpan(ctx, sh, dh, y)
		if err != nil {
			return err
		}
		clear(accumulator)
		for sy := vertical.start; sy < vertical.end; sy++ {
			weight := vertical.weight(sy)
			if weight == 0 {
				continue
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			slot := sy % rows
			row := cache[slot*dw : (slot+1)*dw]
			if rowIDs[slot] != sy {
				for x, span := range horizontal {
					var p [4]float64
					add := func(sx int, weight float64) {
						c := read(sr.Min.X+sx, sr.Min.Y+sy)
						p[0] += float64(float64(c.R) * weight)
						p[1] += float64(float64(c.G) * weight)
						p[2] += float64(float64(c.B) * weight)
						p[3] += float64(float64(c.A) * weight)
					}
					if weights[x] != nil {
						for _, c := range weights[x] {
							add(c.coord, c.weight)
						}
					} else {
						for sx := span.start; sx < span.end; sx++ {
							if sx%1024 == 0 {
								if err := ctx.Err(); err != nil {
									return err
								}
							}
							if weight := span.weight(sx); weight != 0 {
								add(sx, weight)
							}
						}
					}
					for i := range p {
						row[x][i] = p[i] * (span.inverse / 0xffff)
					}
					if opaqueHorizontal {
						row[x][3] = 1
					}
				}
				rowIDs[slot] = sy
			}
			for x, p := range row {
				for i := range p {
					accumulator[x][i] += float64(p[i] * weight)
				}
			}
		}
		for x, p := range accumulator {
			result.R = scaleUint(min(p[0], p[3]) * vertical.inverse)
			result.G = scaleUint(min(p[1], p[3]) * vertical.inverse)
			result.B = scaleUint(min(p[2], p[3]) * vertical.inverse)
			result.A = scaleUint(p[3] * vertical.inverse)
			dst.SetRGBA64(dr.Min.X+x, dr.Min.Y+y, result)
		}
	}
	return ctx.Err()
}

const scaleRowBytes = 2 << 20
const scaleWeightBytes = 1 << 20

type scaleWeight struct {
	coord  int
	weight float64
}

type scaleSpan struct {
	start, end int
	center     float64
	argScale   float64
	inverse    float64
}

func (s scaleSpan) weight(coord int) float64 {
	t := math.Abs((s.center - float64(coord)) * s.argScale)
	if t >= draw.CatmullRom.Support {
		return 0
	}
	return draw.CatmullRom.At(t)
}

func makeScaleSpan(ctx context.Context, source, target, position int) (scaleSpan, error) {
	scale := float64(source) / float64(target)
	halfWidth, argScale := draw.CatmullRom.Support, 1.0
	if scale > 1 {
		halfWidth *= scale
		argScale = 1 / scale
	}
	center := float64((float64(position)+0.5)*scale) - 0.5
	s := scaleSpan{start: max(0, int(math.Floor(center-halfWidth))), end: min(source, int(math.Ceil(center+halfWidth))), center: center, argScale: argScale}
	var total float64
	for coord := s.start; coord < s.end; coord++ {
		if coord%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return scaleSpan{}, err
			}
		}
		total += s.weight(coord)
	}
	s.inverse = 1 / total
	return s, nil
}

func rgbaReader(src image.Image) func(int, int) color.RGBA64 {
	if typed, ok := src.(image.RGBA64Image); ok {
		return typed.RGBA64At
	}
	return func(x, y int) color.RGBA64 {
		r, g, b, a := src.At(x, y).RGBA()
		return color.RGBA64{uint16(r), uint16(g), uint16(b), uint16(a)}
	}
}

func scaleUint(f float64) uint16 {
	i := int32(float64(0xffff*f) + 0.5)
	if i > 0xffff {
		return 0xffff
	}
	if i > 0 {
		return uint16(i)
	}
	return 0
}
