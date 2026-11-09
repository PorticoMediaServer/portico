package decoder

import (
	"reflect"
	"strings"
	"testing"
)

func TestBuildConversionProfile7CopyRefusesUnqualifiedBundle(t *testing.T) {
	target := profile7CopyTarget()
	for _, facts := range []*ToolchainFacts{nil, {}, {Identity: "stock", BitstreamFilters: map[string]bool{"dovi_profile": true}}} {
		graph, err := BuildConversion(ConversionRequest{Target: &target, CopyVideo: true, CopyAudio: true, Toolchain: facts})
		if err == nil || err.Error() != "dolby_vision_profile7_to_81_unavailable" || len(graph.Video) != 0 {
			t.Fatalf("unqualified rewrite reached copy arguments: %+v %v", graph, err)
		}
	}
}

func TestBuildConversionProfile7CopyUsesQualifiedFilter(t *testing.T) {
	target := profile7CopyTarget()
	facts := &ToolchainFacts{Identity: "synthetic-unit-qualified-cohort", DolbyVisionProfile7To81: true, BitstreamFilters: map[string]bool{"dovi_profile": true, "dovi_rpu": true}}
	graph, err := BuildConversion(ConversionRequest{Target: &target, CopyVideo: true, CopyAudio: true, Toolchain: facts})
	if err != nil {
		t.Fatal(err)
	}
	expected := []string{"-bsf:v", "dovi_profile=mode=2", "-c:v", "copy"}
	if !reflect.DeepEqual(graph.Video, expected) {
		t.Fatalf("rewrite filter must precede copy args: %v", graph.Video)
	}
	if target.DolbyVisionProfile != 7 || target.DolbyVisionCompatibility != 6 {
		t.Fatal("source facts changed", target)
	}
	target.DolbyVisionConversion = ""
	graph, err = BuildConversion(ConversionRequest{Target: &target, CopyVideo: true, CopyAudio: true})
	if err != nil || !reflect.DeepEqual(graph.Video, []string{"-c:v", "copy"}) {
		t.Fatal("ordinary source-preserving copy changed", graph, err)
	}
	target.DolbyVisionConversion = "unknown"
	if graph, err = BuildConversion(ConversionRequest{Target: &target, CopyVideo: true, Toolchain: facts}); err == nil || strings.Contains(strings.Join(graph.Video, " "), "copy") {
		t.Fatal("unknown rewrite was silently copied", graph, err)
	}
}
