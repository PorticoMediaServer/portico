package decoder

import (
	"reflect"
	"strings"
	"testing"
)

func hdr10PlusGraphRequest() ConversionRequest {
	qualified, facts := hdr10PlusEncodeFixture()
	return ConversionRequest{ConvertVideo: true, Target: &qualified.Target, Toolchain: facts,
		SourceVideoCodec: qualified.Source.Codec, SourceBitDepth: qualified.Source.BitDepth,
		HDR10PlusIntent: qualified.Intent, HDR10PlusSource: qualified.Source}
}

func assertHDR10PlusGraph(t *testing.T, r ConversionRequest) {
	t.Helper()
	before := r.HDR10PlusSource
	graph, err := BuildConversion(r)
	if err != nil || graph.Backend != BackendSoftware || argValue(graph.Video, "-c:v") != "libx265" || argValue(graph.Video, "-hdr10plus") != "1" {
		t.Fatal("explicit preserve did not reach the final CPU encoder", graph, err)
	}
	if argValue(graph.Video, "-vf") != "" || argValue(graph.Video, "-filter_complex") != "" || len(graph.Input) != 0 || len(graph.ExtraInput) != 0 || graph.VideoMap != "" {
		t.Fatal("unchanged-rendering recipe acquired filters or a device", graph)
	}
	if !reflect.DeepEqual(graph.Stages, []HardwareStage{{StageDecode, ExecutionSoftware}, {StageEncode, ExecutionSoftware}}) || !reflect.DeepEqual(before, r.HDR10PlusSource) {
		t.Fatal("graph claimed a transform or changed measured source facts", graph.Stages, r.HDR10PlusSource)
	}
	if strings.Contains(strings.Join(graph.Video, " "), "dhdr10-info") {
		t.Fatal("graph acquired an external metadata-file dependency", graph.Video)
	}
}

func TestBuildConversionHDR10PlusSameGeometryOnly(t *testing.T) {
	for _, bounds := range [][2]int{{0, 0}, {1920, 1080}, {3840, 2160}} {
		r := hdr10PlusGraphRequest()
		r.Target.Width, r.Target.Height = bounds[0], bounds[1]
		assertHDR10PlusGraph(t, r)
	}
}

func TestBuildConversionHDR10PlusDefaultStaysOff(t *testing.T) {
	r := hdr10PlusGraphRequest()
	r.HDR10PlusIntent = ""
	qualified, err := BuildConversion(r)
	if err != nil || argValue(qualified.Video, "-hdr10plus") != "" || argValue(qualified.Video, "-vf") == "" {
		t.Fatal("empty intent changed the default encoder/filter recipe", qualified, err)
	}
	r.Toolchain.HDR10PlusLibX265Encode = false
	r.HDR10PlusSource = HDR10PlusEncodeSource{}
	ordinary, err := BuildConversion(r)
	if err != nil || !reflect.DeepEqual(ordinary, qualified) {
		t.Fatal("capability or measured facts alone enabled metadata carry", ordinary, qualified, err)
	}
}

func TestBuildConversionHDR10PlusUsesFinalEncoderAfterFallback(t *testing.T) {
	for _, b := range HardwareBackends {
		if !backendSpecs[b].supported() {
			continue
		}
		r := hdr10PlusGraphRequest()
		r.Probe = HardwareProbe{Backend: b, Available: true, Identity: r.Toolchain.Identity, OutputEncoders: map[string]bool{"hevc:10": true}}
		if graph, err := BuildConversion(r); err == nil || argValue(graph.Video, "-hdr10plus") != "" {
			t.Fatal("selected hardware encoder obtained CPU-only metadata options", b, graph, err)
		}
		// An unmeasured HEVC cell falls back before the metadata helper runs.
		r.Probe.OutputEncoders = nil
		assertHDR10PlusGraph(t, r)
		// A capped quality recipe also finalizes its software fallback first.
		r.Probe.OutputEncoders = map[string]bool{"hevc:10": true}
		r.Probe.OutputQuality = map[string]bool{"hevc:10": true}
		r.Target.RateControl, r.Target.VideoBitrateBPS = "quality", 2_000_000
		assertHDR10PlusGraph(t, r)
	}
}

func TestBuildConversionHDR10PlusRejectsUnboundOrTransformedIntent(t *testing.T) {
	for name, change := range map[string]func(*ConversionRequest){
		"unknown intent":     func(r *ConversionRequest) { r.HDR10PlusIntent = "automatic" },
		"copy":               func(r *ConversionRequest) { r.ConvertVideo, r.CopyVideo = false, true },
		"audio only":         func(r *ConversionRequest) { r.ConvertVideo, r.ConvertAudio = false, true },
		"unknown source":     func(r *ConversionRequest) { r.HDR10PlusSource = HDR10PlusEncodeSource{} },
		"source codec":       func(r *ConversionRequest) { r.SourceVideoCodec = "h264" },
		"source depth":       func(r *ConversionRequest) { r.SourceBitDepth = 8 },
		"missing identity":   func(r *ConversionRequest) { r.Toolchain.Identity = "" },
		"unqualified bundle": func(r *ConversionRequest) { r.Toolchain.HDR10PlusLibX265Encode = false },
		"smaller width":      func(r *ConversionRequest) { r.Target.Width = 1280 },
		"smaller height":     func(r *ConversionRequest) { r.Target.Height = 720 },
		"frame rate":         func(r *ConversionRequest) { r.Target.FrameRate = 24 },
		"deinterlace":        func(r *ConversionRequest) { r.Deinterlace = true },
		"subtitle burn":      func(r *ConversionRequest) { r.BurnIn = &BurnIn{} },
		"tone map":           func(r *ConversionRequest) { r.Target.ToneMap = true },
		"different encoder": func(r *ConversionRequest) {
			r.Target.VideoCodec = "h264"
			r.Target.BitDepth = 8
			r.Target.DynamicRange = "sdr"
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := hdr10PlusGraphRequest()
			change(&r)
			if graph, err := BuildConversion(r); err == nil || argValue(graph.Video, "-hdr10plus") != "" {
				t.Fatal("unqualified explicit preservation was silently accepted", graph, err)
			}
		})
	}
}
