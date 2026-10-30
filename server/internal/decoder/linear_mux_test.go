package decoder

import (
	"strings"
	"testing"
)

const linearTestInput = "http://127.0.0.1:43210/input/index.m3u8"
const linearTestOutput = "http://127.0.0.1:43210/output/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef/"

func TestLinearHLSKeepsCopiedCodecMuxRequirements(t *testing.T) {
	for _, tc := range []struct {
		name        string
		plan        LinearEncoding
		tag, strict string
	}{
		{"TrueHD only", LinearEncoding{Audio: true, AudioCodec: "truehd", Fragmented: true}, "", "-2"},
		{"FLAC only", LinearEncoding{Audio: true, AudioCodec: "flac", Fragmented: true}, "", "-2"},
		{"AAC only", LinearEncoding{Audio: true, AudioCodec: "aac", CopyAAC: true, Fragmented: true}, "", ""},
		{"absent audio", LinearEncoding{Video: true, AudioCodec: "truehd", Fragmented: true}, "", ""},
		{"HEVC", LinearEncoding{Video: true, CopyHEVC: true, Fragmented: true}, "hvc1", ""},
		{"DV5", LinearEncoding{Video: true, CopyHEVC: true, CopyDolbyVisionProfile: 5, Fragmented: true}, "dvh1", "unofficial"},
		{"DV7", LinearEncoding{Video: true, CopyHEVC: true, CopyDolbyVisionProfile: 7, Fragmented: true}, "dvhe", "unofficial"},
		{"DV8", LinearEncoding{Video: true, CopyHEVC: true, CopyDolbyVisionProfile: 8, Fragmented: true}, "dvh1", "unofficial"},
		{"DV7 and TrueHD", LinearEncoding{Video: true, Audio: true, AudioCodec: "truehd", CopyHEVC: true, CopyDolbyVisionProfile: 7, Fragmented: true}, "dvhe", "-2"},
		{"ordinary TS", LinearEncoding{Video: true, Audio: true, AudioCodec: "aac", CopyAAC: true}, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args, hardware, err := linearHLSArgs(linearTestInput, linearTestOutput, tc.plan)
			if err != nil || hardware {
				t.Fatal(args, hardware, err)
			}
			if argValue(args, "-tag:v") != tc.tag || argValue(args, "-strict") != tc.strict {
				t.Fatalf("copy mux signalling: %v, want tag=%q strict=%q", args, tc.tag, tc.strict)
			}
			if tc.plan.Video && argValue(args, "-c:v") != "copy" || tc.plan.Audio && argValue(args, "-c:a") != "copy" {
				t.Fatal("copy route acquired an encoder", args)
			}
			if strings.Count(strings.Join(args, " "), "-strict ") > 1 {
				t.Fatal("multiple strictness options can override one another", args)
			}
		})
	}
}

func TestLinearHLSAppliesCopyDolbyVisionGraph(t *testing.T) {
	strip := ConversionTarget{VideoCodec: "hevc", BitDepth: 10, SourceRange: "dolby_vision", DynamicRange: "hdr10", DolbyVisionProfile: 7, DolbyVisionCompatibility: 6, Packaging: "fmp4_hls"}
	rewrite := profile7CopyTarget()
	qualified := &ToolchainFacts{Identity: "unit-qualified", DolbyVisionProfile7To81: true, BitstreamFilters: map[string]bool{"dovi_rpu": true, "filter_units": true, "dovi_profile": true}}
	for _, tc := range []struct {
		name                string
		target              ConversionTarget
		filter, tag, strict string
	}{
		{"base layer", strip, "dovi_rpu=strip=1,filter_units=remove_types=63", "hvc1", ""},
		{"profile 7 to 8.1", rewrite, "dovi_profile=mode=2", "dvh1", "unofficial"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := LinearEncoding{Video: true, Fragmented: true, CopyHEVC: true, Target: &tc.target, Toolchain: qualified}
			args, hardware, err := linearHLSArgs(linearTestInput, linearTestOutput, plan)
			if err != nil || hardware || argValue(args, "-bsf:v") != tc.filter || argValue(args, "-tag:v") != tc.tag || argValue(args, "-strict") != tc.strict {
				t.Fatal("copy graph lost its packet transform or delivered signalling", args, hardware, err)
			}
			if argValue(args, "-c:v") != "copy" || argValue(args, "-vf") != "" {
				t.Fatal("packet rewrite unexpectedly converted the picture", args)
			}
			plan.Toolchain = nil
			if args, _, err = linearHLSArgs(linearTestInput, linearTestOutput, plan); err == nil {
				t.Fatal("unqualified packet transform silently became a copy", args)
			}
		})
	}
}

func TestLinearHLSCarriesGPUFilterRequirement(t *testing.T) {
	reshape := &ConversionTarget{VideoCodec: "hevc", BitDepth: 10, SourceRange: "dolby_vision", DynamicRange: "hdr10", DolbyVisionProfile: 5, Packaging: "fmp4_hls"}
	for _, tc := range []struct {
		name, device, filter, encoder string
		plan                          LinearEncoding
	}{
		{"software", "", "", "libx264", LinearEncoding{Video: true, ConvertVideo: true}},
		{"Vulkan tone map", "vulkan=tone", "libplacebo=", "libx264", LinearEncoding{Video: true, ConvertVideo: true, HDRToSDR: true, Toolchain: &ToolchainFacts{VulkanAvailable: true, Filters: map[string]bool{"libplacebo": true}}}},
		{"OpenCL tone map", "opencl=tone", "tonemap_opencl=", "libx264", LinearEncoding{Video: true, ConvertVideo: true, HDRToSDR: true, Toolchain: &ToolchainFacts{OpenCLAvailable: true, Filters: map[string]bool{"tonemap_opencl": true}}}},
		{"DV5 HDR reshape", "vulkan=tone", "libplacebo=", "libx265", LinearEncoding{Video: true, ConvertVideo: true, Fragmented: true, Target: reshape, Toolchain: &ToolchainFacts{Identity: "unit-qualified", VulkanAvailable: true, Filters: map[string]bool{"libplacebo": true}, Encoders: map[string]bool{"libx265": true}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.plan.HLSInput = true
			args, hardware, err := linearHLSArgs(linearTestInput, linearTestOutput, tc.plan)
			if err != nil || hardware != (tc.device != "") || argValue(args, "-init_hw_device") != tc.device || argValue(args, "-c:v") != tc.encoder {
				t.Fatal("graph lost its GPU requirement", args, hardware, err)
			}
			if tc.filter != "" && !strings.Contains(argValue(args, "-vf"), tc.filter) {
				t.Fatal("test did not build the requested GPU filter", args)
			}
			if argValue(args, "-protocol_whitelist") != "http,tcp,crypto" || argValue(args, "-format_whitelist") != "hls,mpegts,mov,aac,mp3" || argValue(args, "-method") != "PUT" || args[len(args)-1] != linearTestOutput+"index.m3u8" {
				t.Fatal("hardware requirement changed the input/output confinement", args)
			}
			if tc.plan.Fragmented && (argValue(args, "-tag:v") != "hvc1" || argValue(args, "-strict") != "") {
				t.Fatal("converted Dolby Vision retained source sample-entry signalling", args)
			}
		})
	}
}
