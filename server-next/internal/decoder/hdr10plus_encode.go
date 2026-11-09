package decoder

import (
	"errors"
	"math"
)

// HDR10PlusEncoding is explicit encode intent, not a tool capability or a claim
// that an output frame has been observed. Empty preserves the default-off graph.
type HDR10PlusEncoding string

const HDR10PlusEncodingPreserve HDR10PlusEncoding = "preserve"

var ErrHDR10PlusEncodeUnavailable = errors.New("hdr10_plus_encode_unavailable")

// HDR10PlusEncodeSource contains measured source facts from the selected,
// current stream. Unknown facts cannot opt into a qualified encode recipe.
type HDR10PlusEncodeSource struct {
	Present                 bool
	Codec, DynamicRange     string
	BitDepth, Width, Height int
	ChromaSubsampling       string
	FrameRate               float64
	FrameRateMode           string
	Interlaced              *bool
	Rotation                int
	SampleAspectRatio       string
}

// HDR10PlusEncodeRequest describes the final selected encoder and actual
// transforms. It grants no source access and does not select an encoder.
type HDR10PlusEncodeRequest struct {
	Intent      HDR10PlusEncoding
	Target      ConversionTarget
	Source      HDR10PlusEncodeSource
	Encoder     string
	Deinterlace bool
	BurnIn      bool
}

// HDR10PlusLibX265Options must be consumed at the final libx265 graph boundary.
// Only the exact bundle-qualified, same-size/constant-rate/no-transform CPU
// recipe is admitted. A BSF, encoder name or requested HDR output grants none.
// No existing graph activates this intent until its owner binds this helper.
func HDR10PlusLibX265Options(r HDR10PlusEncodeRequest, facts *ToolchainFacts) ([]string, error) {
	switch r.Intent {
	case "":
		return nil, nil
	case HDR10PlusEncodingPreserve:
	default:
		return nil, ErrInvalidConfiguration
	}
	t, err := r.Target.Normalized()
	if err != nil {
		return nil, err
	}
	s := r.Source
	if t.VideoCodec != "hevc" || t.BitDepth != 10 || t.DynamicRange != "hdr10" || t.ToneMap || t.CopyDolbyVision ||
		(t.SourceRange != "hdr10" && t.SourceRange != "hdr10_plus") || t.FrameRate != 0 ||
		!s.Present || s.Codec != "hevc" || s.BitDepth != 10 || s.DynamicRange != "hdr10" || s.ChromaSubsampling != "4:2:0" ||
		s.Width < 2 || s.Width > 32768 || s.Height < 2 || s.Height > 4320 || s.Width%2 != 0 || s.Height%2 != 0 ||
		(t.Width > 0 && t.Width < s.Width) || (t.Height > 0 && t.Height < s.Height) ||
		math.IsNaN(s.FrameRate) || math.IsInf(s.FrameRate, 0) || s.FrameRate <= 0 || s.FrameRate > 240 || s.FrameRateMode != "constant" ||
		s.Interlaced == nil || *s.Interlaced || s.Rotation != 0 || s.SampleAspectRatio != "1:1" || r.Deinterlace || r.BurnIn {
		return nil, ErrInvalidConfiguration
	}
	if facts == nil || facts.Identity == "" || !facts.HDR10PlusLibX265Encode || !facts.Encoders["libx265"] || r.Encoder != "libx265" {
		return nil, ErrHDR10PlusEncodeUnavailable
	}
	return []string{"-hdr10plus", "1"}, nil
}
