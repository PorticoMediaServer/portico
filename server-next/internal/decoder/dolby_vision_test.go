package decoder

import (
	"strings"
	"testing"
)

// ReshapesDolbyVision agrees with the graph BuildConversion actually builds:
// true exactly when the tone mapper is libplacebo (COMPAT-04).
func TestProfile5ReshapesOnSoftwareForAGPUHost(t *testing.T) {
	facts := &ToolchainFacts{Identity: "unit", VulkanAvailable: true,
		Filters:  map[string]bool{"zscale": true, "tonemap": true, "libplacebo": true},
		Encoders: map[string]bool{"libx264": true}}
	probe := HardwareProbe{Backend: BackendVAAPI, Available: true, NativeFilters: true, Device: "/dev/dri/renderD128", Identity: "unit"}
	target := ConversionTarget{VideoCodec: "h264", BitDepth: 8, DynamicRange: "sdr", SourceRange: "dolby_vision", DolbyVisionProfile: 5, ToneMap: true, Packaging: "mpegts_hls"}
	graph, err := BuildConversion(ConversionRequest{ConvertVideo: true, ToneMap: true, Target: &target, Probe: probe, Toolchain: facts})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Backend != BackendSoftware || !strings.Contains(strings.Join(graph.Video, " "), "libplacebo") {
		t.Fatalf("profile 5 did not reshape on the software pipeline: %s %v", graph.Backend, graph.Video)
	}
}

func TestProfile5KeepsHDR10ThroughReshapingOnly(t *testing.T) {
	facts := &ToolchainFacts{Identity: "unit", VulkanAvailable: true,
		Filters:  map[string]bool{"zscale": true, "tonemap": true, "libplacebo": true},
		Encoders: map[string]bool{"libx265": true}}
	probe := HardwareProbe{Backend: BackendSoftware, Identity: "unit"}
	target := ConversionTarget{VideoCodec: "hevc", BitDepth: 10, DynamicRange: "hdr10", SourceRange: "dolby_vision", DolbyVisionProfile: 5, Packaging: "fmp4_hls"}
	graph, err := BuildConversion(ConversionRequest{ConvertVideo: true, Target: &target, Probe: probe, Toolchain: facts})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(graph.Video, " ")
	if !strings.Contains(joined, "libplacebo=") || !strings.Contains(joined, "color_trc=smpte2084") || strings.Contains(joined, "tonemapping=") {
		t.Fatalf("profile 5 did not reshape into HDR10 without tone mapping: %s", joined)
	}
}

func TestProfile5HDR10RefusesWithoutReshapeQualification(t *testing.T) {
	target := ConversionTarget{VideoCodec: "hevc", BitDepth: 10, DynamicRange: "hdr10", SourceRange: "dolby_vision", DolbyVisionProfile: 5, Packaging: "fmp4_hls"}
	for name, facts := range map[string]*ToolchainFacts{
		"no Vulkan device":     {Identity: "unit", Filters: map[string]bool{"libplacebo": true}, Encoders: map[string]bool{"libx265": true}},
		"no libplacebo filter": {Identity: "unit", VulkanAvailable: true, Filters: map[string]bool{"zscale": true, "tonemap": true}, Encoders: map[string]bool{"libx265": true}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := BuildConversion(ConversionRequest{ConvertVideo: true, Target: &target, Probe: HardwareProbe{Backend: BackendSoftware, Identity: "unit"}, Toolchain: facts})
			if err == nil || !strings.Contains(err.Error(), "dolby_vision_reshaping_unavailable") {
				t.Fatalf("an unqualified profile 5 HDR10 reshape was admitted: %v", err)
			}
		})
	}
}

func TestReshapesDolbyVisionMatchesTheConversionGraph(t *testing.T) {
	filters := map[string]bool{"zscale": true, "tonemap": true, "libplacebo": true, "tonemap_vaapi": true, "tonemap_cuda": true, "vpp_qsv": true, "scale_vaapi": true, "scale_cuda": true, "scale": true}
	probes := []HardwareProbe{
		{Backend: BackendSoftware},
		{Backend: BackendVAAPI, Available: true, NativeFilters: true, Device: "/dev/dri/renderD128"},
		{Backend: BackendVAAPI, Available: true, Device: "/dev/dri/renderD128"},
		{Backend: BackendNVENC, Available: true, NativeFilters: true},
		{Backend: BackendVideoToolbox, Available: true},
	}
	compared := 0
	for _, vulkan := range []bool{false, true} {
		for _, probe := range probes {
			r := ConversionRequest{ConvertVideo: true, ToneMap: true, Probe: probe, Toolchain: &ToolchainFacts{Filters: filters, VulkanAvailable: vulkan}}
			graph, err := BuildConversion(r)
			if err != nil {
				continue // this backend isn't buildable on this platform
			}
			compared++
			uses := strings.Contains(strings.Join(graph.Video, " "), "libplacebo")
			if got := ReshapesDolbyVision(r); got != uses {
				t.Errorf("vulkan=%t %+v: ReshapesDolbyVision=%t, graph uses libplacebo=%t", vulkan, probe, got, uses)
			}
		}
	}
	if compared < 4 {
		t.Fatalf("only %d combinations built a graph", compared)
	}
	if ReshapesDolbyVision(ConversionRequest{ConvertVideo: true, ToneMap: false, Toolchain: &ToolchainFacts{Filters: filters, VulkanAvailable: true}}) {
		t.Error("no tone mapping, nothing reshaped")
	}
}
