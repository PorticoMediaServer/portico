package catalog

import "testing"

func TestPlaybackFramesPolicyIsExplicitOutsideComplete(t *testing.T) {
	for _, tc := range []struct {
		tier string
		want bool
	}{{"basic", false}, {"file_list_only", false}, {"complete", true}} {
		p, err := ValidateScanPolicy(ScanPolicy{Tier: tc.tier})
		if err != nil || p.Allows(PlaybackFramesOperation) != tc.want {
			t.Fatal(tc, p, err)
		}
	}
	for _, ops := range [][]string{{PlaybackFramesOperation}, {"probe", "packet_peak"}, {"probe", "playback_frames:1"}} {
		if _, err := ValidateScanPolicy(ScanPolicy{Tier: "custom", Operations: ops}); err == nil {
			t.Fatal("ungranted/internal recipe became public permission", ops)
		}
	}
	p, err := ValidateScanPolicy(ScanPolicy{Tier: "custom", Operations: []string{"probe", PlaybackFramesOperation}})
	if err != nil || !p.Allows(PlaybackFramesOperation) {
		t.Fatal("explicit frames+probe refused", p, err)
	}
	values := (ScanOperations{}).JSONSchema()["items"].(map[string]any)["enum"].([]string)
	found := false
	for _, v := range values {
		found = found || v == PlaybackFramesOperation
		if v == "packet_peak" || v == "playback_frames:1" {
			t.Fatal("recipe exposed as permission", v)
		}
	}
	if !found {
		t.Fatal("operation omitted from published enum")
	}
}
