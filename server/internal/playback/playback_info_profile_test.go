package playback

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/decoder"
)

func TestPlaybackComparisonEncodedProfileMatchesGraph(t *testing.T) {
	for _, tc := range []struct {
		codec, profile string
		depth          int
		want           string
	}{
		{"hevc", "", 8, "main"}, {"hevc", "", 10, "main10"},
		{"h264", "", 8, "high"}, {"h264", "baseline", 8, "baseline"},
		{"h264", "main", 8, "main"}, {"h264", "high", 8, "high"},
	} {
		t.Run(tc.codec+tc.want, func(t *testing.T) {
			target := decoder.ConversionTarget{VideoCodec: tc.codec, VideoProfile: tc.profile, BitDepth: tc.depth, DynamicRange: "sdr"}
			graph, err := decoder.BuildConversion(decoder.ConversionRequest{Target: &target, ConvertVideo: true, Toolchain: plannerToolchain(), SourceVideoCodec: "h264", SourceBitDepth: 8})
			if err != nil {
				t.Fatal(err)
			}
			profile := ""
			for i, arg := range graph.Video {
				if arg == "-profile:v" && i+1 < len(graph.Video) {
					profile = graph.Video[i+1]
				}
			}
			plan := DeliveryPlan{VideoAction: "convert", OutputVideoCodec: tc.codec, ConversionTarget: &target, Trace: &DecisionTrace{Video: &TraceVideo{Codec: "h264", Profile: "source profile"}}}
			info := PlaybackInfoForPlan(plan, nil)
			if profile != tc.want || info.Delivered.Video.Profile != profile || info.Source.Video.Profile != "source profile" || info.Basis != "committed_delivery_plan" {
				t.Fatalf("profile report disagrees with graph/source: graph=%q info=%+v", profile, info)
			}
			plan.VideoAction = "copy"
			if copied := PlaybackInfoForPlan(plan, nil); copied.Delivered.Video.Profile != "source profile" {
				t.Fatal("copy profile replaced with an encoder recipe")
			}
		})
	}
}

func TestPlaybackComparisonEncodedProfileStaysUnknownWithoutExplicitRecipe(t *testing.T) {
	for _, target := range []*decoder.ConversionTarget{nil, {VideoCodec: "av1", BitDepth: 10}, {VideoCodec: "hevc", BitDepth: 12}, {VideoCodec: "hevc", VideoProfile: "source profile", BitDepth: 10}} {
		plan := DeliveryPlan{VideoAction: "convert", OutputVideoCodec: "hevc", ConversionTarget: target, Trace: &DecisionTrace{Video: &TraceVideo{Codec: "hevc", Profile: "source profile"}}}
		if info := PlaybackInfoForPlan(plan, nil); info.Delivered.Video.Profile != "" {
			t.Fatal("codec/source/unsupported depth invented a delivered profile", target, info.Delivered.Video)
		}
	}
}
