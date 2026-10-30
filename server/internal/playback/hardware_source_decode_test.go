package playback

import (
	"runtime"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/decoder"
)

func TestConversionGraphUsesActualSourceCodecAndDepthForHardwareDecode(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		t.Skip("NVENC graph is unavailable on this platform")
	}
	base := DeliveryPlan{Profile: DeliveryProfileLegacy, Mode: "hls", VideoAction: "convert", AudioAction: "none", SourceVideoCodec: "hevc", OutputVideoCodec: "h264", OutputBitDepth: 8, OutputContainer: "mpegts_hls", TargetHeight: 720, TargetWidth: 1280, HardwareBackend: string(decoder.BackendNVENC), HardwareNative: true,
		Trace: &DecisionTrace{Video: &TraceVideo{Codec: "hevc", BitDepth: 10, Width: 1920, Height: 1080, FrameRate: 24, DynamicRange: "sdr"}},
	}
	for _, test := range []struct {
		name     string
		cells    map[string]bool
		edit     func(*DeliveryPlan)
		hardware bool
	}{
		{"source HEVC10 proved", map[string]bool{"hevc:10": true}, nil, true},
		{"only output H2648 proved", map[string]bool{"h264:8": true}, nil, false},
		{"source codec but wrong depth", map[string]bool{"hevc:8": true}, nil, false},
		{"unknown source depth", map[string]bool{"hevc:10": true}, func(p *DeliveryPlan) { p.Trace.Video.BitDepth = 0 }, false},
		{"unknown source codec", map[string]bool{"hevc:10": true}, func(p *DeliveryPlan) { p.SourceVideoCodec = "" }, false},
		{"missing source trace", map[string]bool{"hevc:10": true}, func(p *DeliveryPlan) { p.Trace = nil }, false},
		{"source rotation still needs system frames", map[string]bool{"hevc:10": true}, func(p *DeliveryPlan) { p.Trace.Video.Rotation = 90 }, false},
		{"source SAR still needs system frames", map[string]bool{"hevc:10": true}, func(p *DeliveryPlan) { p.Trace.Video.SampleAspectRatio = "4:3" }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := base
			trace, video := *base.Trace, *base.Trace.Video
			trace.Video, plan.Trace = &video, &trace
			plan.ConversionProbe = &decoder.HardwareProbe{Backend: decoder.BackendNVENC, Available: true, NativeFilters: true, DecodeCodecs: test.cells}
			if test.edit != nil {
				test.edit(&plan)
			}
			graph, err := plan.conversionGraph(nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(strings.Join(graph.Input, " "), "-hwaccel"); got != test.hardware {
				t.Fatalf("decode authority followed output or unknown source instead of proven source cell: %+v", graph)
			}
			if graph.Backend != decoder.BackendNVENC || !strings.Contains(strings.Join(graph.Video, " "), "h264_nvenc") {
				t.Fatal("software decode unnecessarily disabled qualified hardware encode", graph)
			}
		})
	}
}
