package playback

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
)

func TestAudioCodecStringsUseKnownAACProfilesAndOmitUnknownInit(t *testing.T) {
	for _, tc := range []struct{ codec, action, profile, want string }{
		{"aac", "copy", "LC", "mp4a.40.2"},
		{"aac", "convert", "HE-AAC", "mp4a.40.2"},
		{"aac", "convert", "", "mp4a.40.2"},
		{"aac", "copy", "HE-AAC", "mp4a.40.5"},
		{"aac", "copy", "HE-AACv2", "mp4a.40.29"},
		{"aac", "copy", "", ""},
		{"aac", "copy", "unknown", ""},
		{"aac", "copy", "lc", "mp4a.40.2"},
		{"aac", "copy", "he-aac", "mp4a.40.5"},
		{"aac", "copy", "he-aacV2", "mp4a.40.29"},
		{"dts", "copy", "DTS", ""},
		{"dts", "copy", "DTS-HD MA", ""},
		{"dts", "copy", "DTS-HD HRA", ""},
		{"eac3", "copy", "", "ec-3"},
	} {
		p := &DeliveryPlan{VideoAction: "none", AudioAction: tc.action, OutputAudioCodec: tc.codec, OutputContainer: "fmp4_hls", Trace: &DecisionTrace{Audio: &TraceAudio{StreamIndex: 1, Codec: tc.codec, Profile: tc.profile}}}
		if got := audioCodecString(tc.codec, tc.action, tc.profile); got != tc.want {
			t.Fatalf("%+v: CODECS = %q, want %q", tc, got, tc.want)
		}
		manifest := string(hlsMasterManifest(p))
		if tc.want == "" && strings.Contains(manifest, "CODECS=") || tc.want != "" && !strings.Contains(manifest, `CODECS="`+tc.want+`"`) {
			t.Fatal("manifest invented or lost a codec assertion", tc, manifest)
		}
	}
}

func TestUnselectedCopiedAACRenditionCannotBorrowSelectedProfile(t *testing.T) {
	p := &DeliveryPlan{VideoAction: "none", OutputContainer: "fmp4_hls", Trace: &DecisionTrace{Audio: &TraceAudio{StreamIndex: 1, Codec: "aac", Profile: "LC"}}, Renditions: []AudioRenditionPlan{
		{Ordinal: 0, StreamIndex: 1, Action: "copy", Codec: "aac", Profile: "LC", Channels: 2, Default: true},
		{Ordinal: 1, StreamIndex: 2, Action: "copy", Codec: "aac", Channels: 2},
	}}
	if manifest := string(hlsMasterManifest(p)); strings.Contains(manifest, "CODECS=") || strings.Count(manifest, "TYPE=AUDIO") != 2 {
		t.Fatal("unknown alternate AAC borrowed an LC profile or lost its rendition", manifest)
	}
	p.Renditions[1].Action = "convert"
	if manifest := string(hlsMasterManifest(p)); !strings.Contains(manifest, `CODECS="mp4a.40.2"`) {
		t.Fatal("known copied/encoded LC lost its codec assertion", manifest)
	}
}

func TestCopiedAACRenditionsKeepTheirOwnProfiles(t *testing.T) {
	source := videoSource("mkv", "h264", avcDetail,
		SourceAudio{Codec: "aac", Channels: 2, StreamDetail: assets.StreamDetail{Profile: "LC"}},
		SourceAudio{Codec: "aac", Channels: 2, StreamDetail: assets.StreamDetail{Profile: "HE-AAC"}},
		SourceAudio{Codec: "aac", Channels: 2, StreamDetail: assets.StreamDetail{Profile: "HE-AACv2"}})
	plan := decide(t, source, appleTVProfile(), func(in *DeliveryInput) {
		in.HasAudioChoice, in.AudioStream = true, 1
	})
	if len(plan.Renditions) != 3 {
		t.Fatal("missing alternate AAC renditions", plan)
	}
	for i, r := range plan.Renditions {
		if r.Action != "copy" || r.Profile != source.Audio[i].Profile {
			t.Fatal("rendition lost its source profile", r)
		}
	}
	manifest := string(hlsMasterManifest(&plan))
	if !strings.Contains(manifest, `CODECS="avc1.640029,mp4a.40.2,mp4a.40.5,mp4a.40.29"`) {
		t.Fatal("alternate AAC profiles did not reach CODECS", manifest)
	}
}
