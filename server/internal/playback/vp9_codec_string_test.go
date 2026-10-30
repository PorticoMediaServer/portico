package playback

import (
	"strings"
	"testing"
)

func TestVP9CodecStringRequiresMeasuredProfileLevelAndDepth(t *testing.T) {
	for _, tc := range []struct {
		profile      string
		level, depth int
		want         string
	}{
		{"Profile 0", 41, 8, "vp09.00.41.08"},
		{"Profile 1", 31, 8, "vp09.01.31.08"},
		{"Profile 2", 51, 10, "vp09.02.51.10"},
		{"Profile 3", 62, 12, "vp09.03.62.12"},
		{"Profile 2", 0, 10, ""},
		{"Profile 2", -99, 10, ""},
		{"Profile 2", 42, 10, ""},
		{"", 41, 8, ""},
		{"Profile 4", 41, 8, ""},
		{"Profile 0", 41, 10, ""},
		{"Profile 2", 41, 8, ""},
		{"Profile 2", 41, 0, ""},
	} {
		p := &DeliveryPlan{OutputVideoCodec: "vp9", VideoAction: "copy", AudioAction: "none", OutputContainer: "fmp4_hls", Trace: &DecisionTrace{Video: &TraceVideo{Profile: tc.profile, Level: tc.level, BitDepth: tc.depth}}}
		if got := videoCodecString(p); got != tc.want {
			t.Fatalf("%+v: CODECS = %q, want %q", tc, got, tc.want)
		}
		manifest := string(hlsMasterManifest(p))
		if tc.want == "" && strings.Contains(manifest, "CODECS=") || tc.want != "" && !strings.Contains(manifest, `CODECS="`+tc.want+`"`) {
			t.Fatal("manifest did not preserve codec certainty", manifest)
		}
		p.VideoAction = "convert"
		if got := videoCodecString(p); got != "" {
			t.Fatal("source facts described an unmeasured VP9 conversion", got)
		}
	}
	if got := videoCodecString(&DeliveryPlan{OutputVideoCodec: "vp9", VideoAction: "copy"}); got != "" {
		t.Fatal("missing trace invented a VP9 configuration", got)
	}
}
