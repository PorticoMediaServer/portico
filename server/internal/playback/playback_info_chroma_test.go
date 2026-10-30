package playback

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/decoder"
)

func TestPlaybackComparisonEncodedChromaMatchesGraph(t *testing.T) {
	for _, tc := range []struct {
		codec, dynamicRange, pixel string
		depth                      int
	}{
		{"h264", "sdr", "yuv420p", 8},
		{"hevc", "sdr", "yuv420p", 8},
		{"hevc", "hdr10", "yuv420p10le", 10},
		{"av1", "sdr", "yuv420p", 8},
		{"av1", "hlg", "yuv420p10le", 10},
	} {
		t.Run(tc.codec+tc.dynamicRange, func(t *testing.T) {
			target := decoder.ConversionTarget{VideoCodec: tc.codec, BitDepth: tc.depth, DynamicRange: tc.dynamicRange}
			graph, err := decoder.BuildConversion(decoder.ConversionRequest{Target: &target, ConvertVideo: true, Toolchain: plannerToolchain(), SourceVideoCodec: "hevc", SourceBitDepth: 10})
			if err != nil {
				t.Fatal(err)
			}
			pixel := ""
			for i, arg := range graph.Video {
				if arg == "-pix_fmt" && i+1 < len(graph.Video) {
					pixel = graph.Video[i+1]
				}
			}
			plan := DeliveryPlan{VideoAction: "convert", OutputVideoCodec: tc.codec, ConversionTarget: &target, Trace: &DecisionTrace{Video: &TraceVideo{Codec: "hevc", ChromaSubsampling: "4:4:4"}}}
			info := PlaybackInfoForPlan(plan, nil)
			if pixel != tc.pixel || info.Delivered.Video.ChromaSubsampling != "4:2:0" || info.Source.Video.ChromaSubsampling != "4:4:4" || info.Basis != "committed_delivery_plan" {
				t.Fatalf("encoded graph/source/report disagree: pixel=%q info=%+v", pixel, info)
			}
			plan.VideoAction = "copy"
			if copied := PlaybackInfoForPlan(plan, nil); copied.Delivered.Video.ChromaSubsampling != "4:4:4" {
				t.Fatal("copy replaced source chroma with an encoder recipe")
			}
		})
	}
}

func TestPlaybackComparisonEncodedChromaOmitsUncommittedRecipe(t *testing.T) {
	for _, target := range []*decoder.ConversionTarget{
		nil,
		{VideoCodec: "hevc", BitDepth: 12},
		{VideoCodec: "h264", BitDepth: 10},
		{VideoCodec: "hevc", VideoProfile: "source", BitDepth: 10},
		{VideoCodec: "hevc", BitDepth: 10, DynamicRange: "dolby_vision", CopyDolbyVision: true, DolbyVisionProfile: 8, DolbyVisionCompatibility: 1},
	} {
		plan := DeliveryPlan{VideoAction: "convert", OutputVideoCodec: "hevc", ConversionTarget: target, Trace: &DecisionTrace{Video: &TraceVideo{Codec: "hevc", ChromaSubsampling: "4:4:4"}}}
		if info := PlaybackInfoForPlan(plan, nil); info.Delivered.Video.ChromaSubsampling != "" || info.Source.Video.ChromaSubsampling != "4:4:4" {
			t.Fatal("unknown/unsupported recipe inherited source chroma", info)
		}
	}
}
