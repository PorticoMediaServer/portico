package decoder

import (
	"strings"
	"testing"
)

func TestAnalysisClosedRecipes(t *testing.T) {
	for _, kind := range []string{"pcm_shared", "image", "trickplay", "scenes"} {
		args, err := AnalysisArgs("http://127.0.0.1:1234/input/test", AnalysisSpec{Kind: kind, StartUS: 1234567, IntervalUS: 1000000, MaxFrames: 12, MaxDurationUS: 60000000})
		if err != nil {
			t.Fatal(err)
		}
		joined := " " + strings.Join(args, " ") + " "
		if strings.Contains(joined, " -t ") || strings.Contains(joined, " -to ") || kind != "image" && strings.Contains(joined, " -frames") {
			t.Fatalf("truncated full analysis: %s", joined)
		}
		preview := kind == "image" || kind == "trickplay" || kind == "scenes"
		if strings.Contains(joined, " -xerror ") == preview || preview && !strings.Contains(joined, "-max_error_rate 0.05") || !strings.Contains(joined, "-protocol_whitelist http,tcp") || !strings.HasSuffix(joined, "pipe:1 ") {
			t.Fatal(joined)
		}
		if kind == "image" && !strings.Contains(joined, "-ss 1.234567") {
			t.Fatal("timestamp precision", joined)
		}
		if strings.Contains(joined, "setpts=PTS-STARTPTS") {
			t.Fatal("discarded stream offset", joined)
		}
	}
	for _, spec := range []AnalysisSpec{{Kind: "arbitrary-filter", MaxDurationUS: 1}, {Kind: "pcm_shared"}, {Kind: "image", StartUS: -1, MaxDurationUS: 1}, {Kind: "image", StartUS: 2, MaxDurationUS: 2}, {Kind: "scenes", IntervalUS: 1, MaxFrames: 1, MaxDurationUS: 2}, {Kind: "trickplay", IntervalUS: 1000000, MaxFrames: 65537, MaxDurationUS: 60000000}} {
		if _, err := AnalysisArgs("ignored", spec); err == nil {
			t.Fatalf("accepted unsafe recipe %+v", spec)
		}
	}
}

func TestAnalysisPreviewColorAndDisplayGeometry(t *testing.T) {
	original := CurrentToolchain()
	defer RestoreToolchain(original)
	for _, kind := range []string{"image", "trickplay", "scenes"} {
		args, err := AnalysisArgs("input", AnalysisSpec{Kind: kind, MaxDurationUS: 60000000, IntervalUS: 1000000, MaxFrames: 12, SourceVideo: PreviewVideoFacts{DynamicRange: "hdr10"}})
		graph := strings.Join(args, " ")
		if err != nil || !strings.Contains(graph, "reset_sar=1") || !strings.Contains(graph, "zscale=t=linear") || !strings.Contains(graph, "tonemap=") {
			t.Fatal(graph, err)
		}
	}
	spec := AnalysisSpec{Kind: "image", MaxDurationUS: 60000000, SourceVideo: PreviewVideoFacts{DynamicRange: "dolby_vision", DolbyVisionProfile: 5}}
	RestoreToolchain(nil)
	if _, err := AnalysisArgs("input", spec); err == nil {
		t.Fatal("unqualified Dolby Vision color conversion")
	}
	RestoreToolchain(&ToolchainFacts{VulkanAvailable: true, Filters: map[string]bool{"libplacebo": true}})
	args, err := AnalysisArgs("input", spec)
	if err != nil || !strings.Contains(strings.Join(args, " "), "libplacebo=") || !analysisNeedsHardware(spec) {
		t.Fatal(args, err)
	}
}

func TestSegmentRegionRecipes(t *testing.T) {
	audio, err := AnalysisArgs("http://127.0.0.1:1234/input/test", AnalysisSpec{Kind: "segment_audio", StartUS: 1_500_000_000, RegionUS: 480_000_000, MaxDurationUS: 2_000_000_000})
	joined := " " + strings.Join(audio, " ") + " "
	if err != nil || !strings.Contains(joined, " -ss 1500.000000 -protocol_whitelist") || !strings.Contains(joined, " -t 480.000000 ") || !strings.Contains(joined, "aresample=8000:async=1:first_pts=0 -ac 1 -ar 8000") || !strings.Contains(joined, "-max_error_rate 0.05") || strings.Contains(joined, " -xerror ") {
		t.Fatal(joined, err)
	}
	opening, _ := AnalysisArgs("input", AnalysisSpec{Kind: "segment_audio", RegionUS: 600_000_000, MaxDurationUS: 2_000_000_000})
	if strings.Contains(strings.Join(opening, " "), "-ss") {
		t.Fatal("an opening region seeks", opening)
	}
	frames, err := AnalysisArgs("input", AnalysisSpec{Kind: "segment_frames", StartUS: 60_000_000, RegionUS: 900_000_000, IntervalUS: 1000000, MaxFrames: 901, MaxDurationUS: 2_000_000_000, SourceVideo: PreviewVideoFacts{DynamicRange: "hdr10"}})
	graph := strings.Join(frames, " ")
	if err != nil || !strings.Contains(graph, "fps=1:start_time=0,scale=256:144") || !strings.Contains(graph, "tonemap=") || !strings.Contains(graph, "format=gray -frames:v 901 -pix_fmt gray -f rawvideo pipe:1") {
		t.Fatal(graph, err)
	}
	for _, spec := range []AnalysisSpec{{Kind: "segment_audio", MaxDurationUS: 10}, {Kind: "segment_audio", RegionUS: 11, MaxDurationUS: 10}, {Kind: "segment_frames", RegionUS: 5, MaxDurationUS: 10, IntervalUS: 500000, MaxFrames: 5}, {Kind: "segment_frames", RegionUS: 5, MaxDurationUS: 10, IntervalUS: 1000000, MaxFrames: 5, Width: 320, Height: 180}} {
		if _, err := AnalysisArgs("ignored", spec); err == nil {
			t.Fatalf("accepted unsafe recipe %+v", spec)
		}
	}
}
