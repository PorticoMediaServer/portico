package metadata

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"

	"github.com/PorticoMediaServer/portico/server/internal/imagework"
)

// Every arrival is encoded once into two slots. Small covers cards and lists;
// large is the full display picture at the owner's chosen edge.
const (
	artworkSmallEdge = 800
	artworkSmallCap  = 150_000
	artworkLargeEdge = 1920
	// A channel logo is one rendition, not a card.
	artworkLogoEdge = 256
	artworkLogoCap  = 96_000
)

// artworkLargeCap is the encoded-byte cap for each permitted large edge.
var artworkLargeCap = map[int]int{1280: 300_000, 1920: 500_000, 2560: 800_000}

// artworkMaxBytes bounds any stored artwork file: the largest cap's allowance.
const artworkMaxBytes = 800_000 * 3 / 2

// artworkJPEGQualities is the whole quality ladder. Opaque art is never encoded
// below the last rung, and dimensions are never reduced to save bytes.
var artworkJPEGQualities = []int{90, 82, 75}

// errArtworkCapUnreachable refuses a picture that exceeds its cap's 1.5x
// allowance even at the quality floor. Nothing smaller or softer is published.
var errArtworkCapUnreachable = errors.New("cap_unreachable")

// artworkCapError is errArtworkCapUnreachable for one slot and format, so the
// refusal can be counted where it is recorded.
type artworkCapError struct {
	slot artworkSlot
	png  bool
}

func (e *artworkCapError) Error() string        { return errArtworkCapUnreachable.Error() }
func (e *artworkCapError) Is(target error) bool { return target == errArtworkCapUnreachable }

// errArtworkEncodedTooLarge stops an encoder once its output passes the
// allowance, so its buffer never grows past the bytes the slot can publish.
var errArtworkEncodedTooLarge = errors.New("artwork encoding exceeded its allowance")

type artworkSlot int

const (
	artworkSlotSmall artworkSlot = iota
	artworkSlotLarge
	artworkSlotLogo
)

func (slot artworkSlot) String() string {
	return [...]string{"small", "large", "logo"}[slot]
}

// artworkEncoded is one encoded slot. An outlier is over its cap but inside
// the 1.5x allowance at the quality floor.
type artworkEncoded struct {
	data          []byte
	mime          string
	width, height int
	outlier       bool
}

// artworkSlotLimits returns the edge and cap for one slot. The large slot
// follows the captured encoder policy; without one it uses the default edge.
func artworkSlotLimits(ctx context.Context, slot artworkSlot) (int, int) {
	switch slot {
	case artworkSlotSmall:
		return artworkSmallEdge, artworkSmallCap
	case artworkSlotLogo:
		return artworkLogoEdge, artworkLogoCap
	}
	if policy, ok := ctx.Value(artworkEncodingPolicyKey{}).(artworkEncodingPolicy); ok {
		return policy.edge, policy.ceiling
	}
	return artworkLargeEdge, artworkLargeCap[artworkLargeEdge]
}

// encodeArtworkSlot encodes one slot from the decoded source: aspect kept,
// never enlarged, never reduced for bytes. Opaque art is JPEG on the quality
// ladder; art with transparency is lossless PNG. A result over the cap is kept
// only within 1.5x the cap, and is counted as an outlier.
func encodeArtworkSlot(ctx context.Context, source image.Image, slot artworkSlot) (artworkEncoded, error) {
	edge, limit := artworkSlotLimits(ctx, slot)
	return encodeArtworkWithin(ctx, source, slot, edge, limit)
}

func encodeArtworkWithin(ctx context.Context, source image.Image, slot artworkSlot, edge, limit int) (artworkEncoded, error) {
	bounds := source.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w < 1 || h < 1 {
		return artworkEncoded{}, errors.New("dimension_limit")
	}
	if max(w, h) > edge {
		w = max(1, w*edge/max(bounds.Dx(), bounds.Dy()))
		h = max(1, h*edge/max(bounds.Dx(), bounds.Dy()))
	}
	scaled := image.NewNRGBA(image.Rect(0, 0, w, h))
	if err := imagework.CatmullRom(ctx, scaled, source); err != nil {
		return artworkEncoded{}, err
	}
	if err := ctx.Err(); err != nil {
		return artworkEncoded{}, err
	}
	allowance := limit * 3 / 2
	// The one output buffer is charged at the allowance up front and never
	// grows: an encoder that passes it is stopped, not buffered further.
	out := &artworkBoundedBuffer{limit: allowance, b: make([]byte, 0, allowance)}
	opaque := scaled.Opaque()
	if !opaque {
		err := encodeArtworkPNG(out, scaled)
		if errors.Is(err, errArtworkEncodedTooLarge) {
			return artworkEncoded{}, &artworkCapError{slot, true}
		}
		if err != nil {
			return artworkEncoded{}, err
		}
		return artworkEncoded{bytes.Clone(out.b), "image/png", w, h, len(out.b) > limit}, nil
	}
	floor := artworkJPEGQualities[len(artworkJPEGQualities)-1]
	for _, quality := range artworkJPEGQualities {
		out.b = out.b[:0]
		err := jpeg.Encode(out, scaled, &jpeg.Options{Quality: quality})
		overflow := errors.Is(err, errArtworkEncodedTooLarge)
		if err != nil && !overflow {
			return artworkEncoded{}, err
		}
		// The first rung within the cap wins. Only the floor may publish over
		// the cap, and then only inside the allowance.
		if !overflow && (len(out.b) <= limit || quality == floor) {
			return artworkEncoded{bytes.Clone(out.b), "image/jpeg", w, h, len(out.b) > limit}, nil
		}
	}
	return artworkEncoded{}, &artworkCapError{slot, false}
}

// encodeArtworkPNG writes lossless PNG at best compression. When the picture
// has at most 256 distinct colours it is written with exactly those colours as
// a palette, which decodes to the same pixels.
func encodeArtworkPNG(out *artworkBoundedBuffer, picture *image.NRGBA) error {
	encoder := png.Encoder{CompressionLevel: png.BestCompression}
	if paletted := artworkExactPalette(picture); paletted != nil {
		return encoder.Encode(out, paletted)
	}
	return encoder.Encode(out, picture)
}

func artworkExactPalette(picture *image.NRGBA) *image.Paletted {
	bounds := picture.Bounds()
	index := map[color.NRGBA]uint8{}
	palette := color.Palette{}
	paletted := image.NewPaletted(bounds, nil)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			c := picture.NRGBAAt(x, y)
			i, ok := index[c]
			if !ok {
				if len(palette) == 256 {
					return nil
				}
				i = uint8(len(palette))
				index[c] = i
				palette = append(palette, c)
			}
			paletted.SetColorIndex(x, y, i)
		}
	}
	paletted.Palette = palette
	return paletted
}

// artworkBoundedBuffer is a write buffer that refuses to grow past limit.
type artworkBoundedBuffer struct {
	limit int
	b     []byte
}

func (o *artworkBoundedBuffer) Write(p []byte) (int, error) {
	if len(o.b)+len(p) > o.limit {
		return 0, errArtworkEncodedTooLarge
	}
	o.b = append(o.b, p...)
	return len(p), nil
}
