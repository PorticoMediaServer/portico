package decoder

import (
	"errors"
	"math"
	"reflect"
	"testing"
)

func hdr10PlusEncodeFixture() (HDR10PlusEncodeRequest, *ToolchainFacts) {
	progressive := false
	return HDR10PlusEncodeRequest{
		Intent:  HDR10PlusEncodingPreserve,
		Target:  ConversionTarget{VideoCodec: "hevc", BitDepth: 10, DynamicRange: "hdr10", SourceRange: "hdr10_plus"},
		Source:  HDR10PlusEncodeSource{Present: true, Codec: "hevc", DynamicRange: "hdr10", BitDepth: 10, Width: 1920, Height: 1080, ChromaSubsampling: "4:2:0", FrameRate: 24, FrameRateMode: "constant", Interlaced: &progressive, SampleAspectRatio: "1:1"},
		Encoder: "libx265",
	}, &ToolchainFacts{Identity: "synthetic-unit-qualified-hdr10plus", HDR10PlusLibX265Encode: true, Encoders: map[string]bool{"libx265": true}}
}

func TestHDR10PlusEncodeOptionsRequireExplicitQualifiedRecipe(t *testing.T) {
	r, facts := hdr10PlusEncodeFixture()
	before := r
	options, err := HDR10PlusLibX265Options(r, facts)
	if err != nil || !reflect.DeepEqual(options, []string{"-hdr10plus", "1"}) || !reflect.DeepEqual(r, before) {
		t.Fatal("qualified opt-in recipe changed", options, err)
	}
	// A ceiling that cannot resize the source retains the same-size recipe.
	r.Target.Width, r.Target.Height = 3840, 2160
	if _, err = HDR10PlusLibX265Options(r, facts); err != nil {
		t.Fatal("non-resizing ceiling refused", err)
	}
	r.Intent = ""
	if options, err = HDR10PlusLibX265Options(r, nil); err != nil || len(options) != 0 {
		t.Fatal("default-off emitted encoder options", options, err)
	}
	for _, intent := range []HDR10PlusEncoding{"auto", "0", "1", "copy"} {
		r.Intent = intent
		if options, err = HDR10PlusLibX265Options(r, facts); !errors.Is(err, ErrInvalidConfiguration) || len(options) != 0 {
			t.Fatal("unintegrated intent accepted", intent, options, err)
		}
	}
}

func TestHDR10PlusEncodeOptionsDoNotGrantByEncoderName(t *testing.T) {
	r, qualified := hdr10PlusEncodeFixture()
	for _, facts := range []*ToolchainFacts{nil, {},
		{Identity: "name-only", Encoders: map[string]bool{"libx265": true}},
		{HDR10PlusLibX265Encode: true, Encoders: map[string]bool{"libx265": true}},
		{Identity: "missing-encoder", HDR10PlusLibX265Encode: true},
	} {
		if options, err := HDR10PlusLibX265Options(r, facts); !errors.Is(err, ErrHDR10PlusEncodeUnavailable) || len(options) != 0 {
			t.Fatal("unqualified tools acquired opt-in", facts, options, err)
		}
	}
	for _, encoder := range []string{"hevc_vaapi", "hevc_qsv", "hevc_nvenc", "libx264", ""} {
		r.Encoder = encoder
		if options, err := HDR10PlusLibX265Options(r, qualified); !errors.Is(err, ErrHDR10PlusEncodeUnavailable) || len(options) != 0 {
			t.Fatal("another encoder acquired the CPU qualification", encoder, options, err)
		}
	}
}

func TestHDR10PlusEncodeOptionsRefuseUnknownOrTransformedSource(t *testing.T) {
	for name, change := range map[string]func(*HDR10PlusEncodeRequest){
		"unobserved":             func(r *HDR10PlusEncodeRequest) { r.Source.Present = false },
		"other source codec":     func(r *HDR10PlusEncodeRequest) { r.Source.Codec = "av1" },
		"unknown depth":          func(r *HDR10PlusEncodeRequest) { r.Source.BitDepth = 0 },
		"unknown range":          func(r *HDR10PlusEncodeRequest) { r.Source.DynamicRange = "" },
		"unknown chroma":         func(r *HDR10PlusEncodeRequest) { r.Source.ChromaSubsampling = "" },
		"changed chroma":         func(r *HDR10PlusEncodeRequest) { r.Source.ChromaSubsampling = "4:4:4" },
		"unknown width":          func(r *HDR10PlusEncodeRequest) { r.Source.Width = 0 },
		"odd width":              func(r *HDR10PlusEncodeRequest) { r.Source.Width++ },
		"odd height":             func(r *HDR10PlusEncodeRequest) { r.Source.Height++ },
		"width transform":        func(r *HDR10PlusEncodeRequest) { r.Target.Width = 1280 },
		"height transform":       func(r *HDR10PlusEncodeRequest) { r.Target.Height = 720 },
		"cadence filter":         func(r *HDR10PlusEncodeRequest) { r.Target.FrameRate = 24 },
		"unknown cadence":        func(r *HDR10PlusEncodeRequest) { r.Source.FrameRateMode = "" },
		"variable cadence":       func(r *HDR10PlusEncodeRequest) { r.Source.FrameRateMode = "variable" },
		"unknown rate":           func(r *HDR10PlusEncodeRequest) { r.Source.FrameRate = 0 },
		"nan rate":               func(r *HDR10PlusEncodeRequest) { r.Source.FrameRate = math.NaN() },
		"infinite rate":          func(r *HDR10PlusEncodeRequest) { r.Source.FrameRate = math.Inf(1) },
		"unknown scan":           func(r *HDR10PlusEncodeRequest) { r.Source.Interlaced = nil },
		"interlaced":             func(r *HDR10PlusEncodeRequest) { interlaced := true; r.Source.Interlaced = &interlaced },
		"rotation":               func(r *HDR10PlusEncodeRequest) { r.Source.Rotation = 90 },
		"unknown pixel aspect":   func(r *HDR10PlusEncodeRequest) { r.Source.SampleAspectRatio = "" },
		"pixel aspect transform": func(r *HDR10PlusEncodeRequest) { r.Source.SampleAspectRatio = "16:15" },
		"deinterlace":            func(r *HDR10PlusEncodeRequest) { r.Deinterlace = true },
		"burn in":                func(r *HDR10PlusEncodeRequest) { r.BurnIn = true },
		"another output codec":   func(r *HDR10PlusEncodeRequest) { r.Target.VideoCodec = "av1" },
		"another output depth":   func(r *HDR10PlusEncodeRequest) { r.Target.BitDepth = 8 },
		"another output range":   func(r *HDR10PlusEncodeRequest) { r.Target.DynamicRange = "sdr" },
		"unknown input range":    func(r *HDR10PlusEncodeRequest) { r.Target.SourceRange = "" },
	} {
		t.Run(name, func(t *testing.T) {
			r, facts := hdr10PlusEncodeFixture()
			change(&r)
			if options, err := HDR10PlusLibX265Options(r, facts); err == nil || len(options) != 0 {
				t.Fatal("unqualified transformation acquired opt-in", options, err)
			}
		})
	}
}
