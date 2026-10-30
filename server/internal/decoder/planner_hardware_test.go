package decoder

import (
	"runtime"
	"strings"
	"testing"
)

// This checks every vendor's argument dialect on any host, without pretending
// an encoder or GPU was qualified here. Production platform gates stay intact.
func TestSelectedHEVCTargetArgumentsForEveryVendor(t *testing.T) {
	for _, backend := range HardwareBackends {
		t.Run(string(backend), func(t *testing.T) {
			saved := backendSpecs[backend]
			spec := saved
			spec.platforms = []string{runtime.GOOS}
			backendSpecs[backend] = spec
			defer func() { backendSpecs[backend] = saved }()
			probe := HardwareProbe{Backend: backend, Available: true, OutputEncoders: map[string]bool{"hevc:10": true}, OutputQuality: map[string]bool{"hevc:10": true}}
			target := ConversionTarget{VideoCodec: "hevc", BitDepth: 10, DynamicRange: "hlg", SourceRange: "hlg", Height: 1080, AudioCodec: "flac", AudioChannels: 8, AudioSampleRate: 96000, AudioBitDepth: 24, RateControl: "quality", Packaging: "fmp4_hls"}
			graph, err := BuildConversion(ConversionRequest{Target: &target, ConvertVideo: true, ConvertAudio: true, Probe: probe, Toolchain: targetFacts()})
			if err != nil || graph.Backend != backend || argValue(graph.Video, "-c:v") != "hevc_"+string(backend) || argValue(graph.Video, "-color_trc") != "arib-std-b67" || argValue(graph.Video, "-tag:v") != "hvc1" {
				t.Fatal(graph, err)
			}
			qualityKey := map[HardwareBackend]string{BackendNVENC: "-cq", BackendQSV: "-global_quality", BackendVAAPI: "-qp", BackendAMF: "-qp_i", BackendVideoToolbox: "-q:v"}[backend]
			if argValue(graph.Video, qualityKey) == "" || !strings.Contains(argValue(graph.Video, "-force_key_frames"), "6") {
				t.Fatal(graph.Video)
			}
			if backend == BackendNVENC && argValue(graph.Video, "-forced-idr") != "1" {
				t.Fatal(graph.Video)
			}
			if (backend == BackendQSV || backend == BackendAMF) && argValue(graph.Video, "-forced_idr") != "1" {
				t.Fatal(graph.Video)
			}
			if argValue(graph.Audio, "-c:a") != "flac" || argValue(graph.Audio, "-ac") != "8" || argValue(graph.Audio, "-ar") != "96000" || argValue(graph.Audio, "-bits_per_raw_sample") != "24" {
				t.Fatal(graph.Audio)
			}
			// A ceiling cannot be dropped by a hardware quality path.
			target.VideoBitrateBPS = 2000000
			graph, err = BuildConversion(ConversionRequest{Target: &target, ConvertVideo: true, Probe: probe, Toolchain: targetFacts()})
			if err != nil || graph.Backend != BackendSoftware || argValue(graph.Video, "-maxrate") != "2000000" || argValue(graph.Video, "-c:v") != "libx265" {
				t.Fatal(graph, err)
			}
		})
	}
}
