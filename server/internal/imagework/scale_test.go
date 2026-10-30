package imagework

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"runtime"
	"testing"

	"golang.org/x/image/draw"
)

func TestCatmullRomMatchesUpstreamPixelsAndEncoding(t *testing.T) {
	bounds := image.Rect(7, 11, 54, 72)
	palette := color.Palette{color.Transparent, color.Black, color.White, color.NRGBA{193, 42, 17, 79}}
	images := []draw.Image{
		image.NewRGBA(bounds), image.NewNRGBA(bounds), image.NewRGBA64(bounds), image.NewNRGBA64(bounds),
		image.NewGray(bounds), image.NewGray16(bounds), image.NewAlpha(bounds), image.NewAlpha16(bounds),
		image.NewPaletted(bounds, palette), image.NewCMYK(bounds),
	}
	for _, ratio := range []image.YCbCrSubsampleRatio{image.YCbCrSubsampleRatio444, image.YCbCrSubsampleRatio422, image.YCbCrSubsampleRatio420, image.YCbCrSubsampleRatio440, image.YCbCrSubsampleRatio411, image.YCbCrSubsampleRatio410} {
		img := image.NewYCbCr(bounds, ratio)
		for i := range img.Y {
			img.Y[i] = byte(i*37 + 51)
		}
		for i := range img.Cb {
			img.Cb[i], img.Cr[i] = byte(i*13+47), byte(i*19+73)
		}
		compareScale(t, fmt.Sprintf("ycbcr-%d", ratio), img)
		alpha := image.NewNYCbCrA(bounds, ratio)
		copy(alpha.Y, img.Y)
		copy(alpha.Cb, img.Cb)
		copy(alpha.Cr, img.Cr)
		for i := range alpha.A {
			alpha.A[i] = byte(i*43 + 19)
		}
		compareScale(t, fmt.Sprintf("ycbcra-%d", ratio), alpha)
	}
	var random uint32 = 731
	for _, img := range images {
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				random = random*1664525 + 1013904223
				img.Set(x, y, color.NRGBA64{uint16(random), uint16(random >> 16), uint16(random * 19), uint16(random * 73)})
			}
		}
		compareScale(t, fmt.Sprintf("%T", img), img)
		// Exercise images with an actual non-tight pixel stride.
		if sub, ok := img.(interface {
			SubImage(image.Rectangle) image.Image
		}); ok {
			compareScale(t, fmt.Sprintf("%T/sub", img), sub.SubImage(image.Rect(13, 17, 38, 55)))
		}
	}
}

func compareScale(t *testing.T, name string, src image.Image) {
	t.Helper()
	for _, size := range []image.Point{{1, 1}, {2, 7}, {13, 17}, {37, 49}, {71, 83}} {
		t.Run(fmt.Sprintf("%s/%dx%d", name, size.X, size.Y), func(t *testing.T) {
			dr := image.Rect(3, 5, 3+size.X, 5+size.Y)
			want, got := image.NewNRGBA(dr), image.NewNRGBA(dr)
			draw.CatmullRom.Scale(want, dr, src, src.Bounds(), draw.Src, nil)
			if err := CatmullRom(context.Background(), got, src); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(want.Pix, got.Pix) {
				for i := range want.Pix {
					if want.Pix[i] != got.Pix[i] {
						t.Fatalf("pixel byte %d: got %d, want %d", i, got.Pix[i], want.Pix[i])
					}
				}
			}
			for _, encode := range []func(*bytes.Buffer, image.Image) error{
				func(b *bytes.Buffer, img image.Image) error { return png.Encode(b, img) },
				func(b *bytes.Buffer, img image.Image) error { return jpeg.Encode(b, img, &jpeg.Options{Quality: 90}) },
			} {
				var actual, expected bytes.Buffer
				if err := encode(&actual, got); err != nil {
					t.Fatal(err)
				}
				if err := encode(&expected, want); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(actual.Bytes(), expected.Bytes()) {
					t.Fatal("encoded representation differs")
				}
			}
		})
	}
}

type proceduralImage struct {
	bounds image.Rectangle
	read   func()
}

func (p proceduralImage) Bounds() image.Rectangle { return p.bounds }
func (p proceduralImage) ColorModel() color.Model { return color.RGBA64Model }
func (p proceduralImage) At(x, y int) color.Color { return p.RGBA64At(x, y) }
func (p proceduralImage) RGBA64At(x, y int) color.RGBA64 {
	if p.read != nil {
		p.read()
	}
	return color.RGBA64{uint16(x * 31), uint16(y * 19), uint16((x + y) * 17), 0xffff}
}

func TestCatmullRomExtremeAxesBoundCoefficientAndRowStorage(t *testing.T) {
	// These exceed production's dimension limit deliberately: the working
	// storage policy must not rely on a caller's validation to remain bounded.
	for _, bounds := range []image.Rectangle{image.Rect(0, 0, 200000, 1), image.Rect(0, 0, 1, 200000)} {
		src := proceduralImage{bounds: bounds}
		dst := image.NewNRGBA(image.Rect(0, 0, 1, 1))
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		if err := CatmullRom(context.Background(), dst, src); err != nil {
			t.Fatal(err)
		}
		runtime.ReadMemStats(&after)
		if allocated := after.TotalAlloc - before.TotalAlloc; allocated > scaleRowBytes+scaleWeightBytes+(256<<10) {
			t.Fatalf("extreme %v allocated %d bytes", bounds.Size(), allocated)
		}
	}
}

func TestCatmullRomCancellationDuringScaling(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reads := 0
	src := proceduralImage{bounds: image.Rect(0, 0, 4000, 6000), read: func() {
		reads++
		if reads == 100 {
			cancel()
		}
	}}
	dst := image.NewNRGBA(image.Rect(0, 0, 400, 600))
	if err := CatmullRom(ctx, dst, src); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled scale: %v", err)
	}
	if reads >= 4000*6000 {
		t.Fatal("cancellation waited for the entire image")
	}
}

func BenchmarkCatmullRomWorkingMemory(b *testing.B) {
	src := proceduralImage{bounds: image.Rect(0, 0, 4800, 5000)}
	for _, upstream := range []bool{true, false} {
		name := "bounded"
		if upstream {
			name = "upstream"
		}
		b.Run(name, func(b *testing.B) {
			// This is the actual aspect-preserving 1920-edge representation of
			// a valid 24MP source. Source pixels are procedural to measure the
			// scaler alone; this does not measure or bound codec allocation.
			dst := image.NewNRGBA(image.Rect(0, 0, 1843, 1920))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if upstream {
					draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
				} else if err := CatmullRom(context.Background(), dst, src); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
