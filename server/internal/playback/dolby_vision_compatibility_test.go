package playback

import (
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"testing"
)

func TestDolbyVisionProfile8CompatibilityUsesExactDeclaredVariant(t *testing.T) {
	client := appleTVProfile()
	codec := client.videoCodec("hevc")
	if codec == nil {
		t.Fatal("fixture codec missing")
	}
	v := &SourceVideo{Codec: "hevc", StreamDetail: assets.StreamDetail{DynamicRange: assets.RangeDolbyVision, DolbyVisionProfile: 8, DolbyVisionCompatibility: 1}}
	for _, ids := range [][]int{nil, {1}, {4}, {1, 4}, {2}} {
		codec.DolbyVisionProfile8Compatibility = ids
		for _, compat := range []int{1, 2, 4} {
			v.DolbyVisionCompatibility = compat
			want := len(ids) == 0 || containsInt(ids, compat)
			if got := nativeDolbyVision(v, codec, client); got != want {
				t.Fatalf("ids=%v source=%d got=%v", ids, compat, got)
			}
		}
	}
	v.DolbyVisionProfile = 5
	v.DolbyVisionCompatibility = 0
	codec.DolbyVisionProfile8Compatibility = []int{1}
	if !nativeDolbyVision(v, codec, client) {
		t.Fatal("profile-8 narrowing changed explicit profile 5")
	}
}

func TestDolbyVisionProfile8CompatibilityValidation(t *testing.T) {
	for _, ids := range [][]int{{1}, {4}, {1, 2, 4}, {0}, {3}, {1, 1}} {
		doc := BaselineCapabilityDocument()
		engine := doc.Engines.Native
		engine.Evidence = EvidenceDeclared
		engine.Video = append(engine.Video, ClientVideoCodec{Codec: "hevc", BitDepths: []int{10}, DynamicRanges: []string{"sdr", "dolby_vision"}, DolbyVisionProfiles: []int{8}, DolbyVisionProfile8Compatibility: ids})
		err := doc.Accept()
		invalid := len(ids) == 1 && (ids[0] == 0 || ids[0] == 3) || len(ids) == 2 && ids[0] == ids[1]
		if (err != nil) != invalid {
			t.Fatalf("ids=%v err=%v", ids, err)
		}
	}
}
