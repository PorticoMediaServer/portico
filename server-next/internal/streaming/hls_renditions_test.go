package streaming

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
)

func TestPlannerBuildsOneRenditionPerTrack(t *testing.T) {
	source := videoSource(
		"mkv",
		"h264",
		mediafacts.StreamDetail{Width: 320, Height: 180, BitDepth: 8, DynamicRange: "sdr"},
		SourceAudio{Codec: "ac3", Channels: 6, Language: "eng", Default: true},
		SourceAudio{Codec: "aac", Channels: 2, Language: "fra"},
		SourceAudio{Codec: "aac", Channels: 2, Language: "eng", Commentary: true},
	)
	p := decide(t, source, chromeProfile(), nil)
	if p.VideoAction != "copy" || len(p.Renditions) != 3 || p.Renditions[0].Action != "convert" || p.Renditions[0].Channels != 2 || !p.Renditions[0].Default || p.Renditions[1].Action != "copy" {
		t.Fatalf("%+v", p)
	}
	raw := string(hlsMasterManifest(&p))
	if strings.Count(raw, "TYPE=AUDIO") != 3 || strings.Contains(raw, "describes-video") || !strings.Contains(raw, "AUTOSELECT=NO") {
		t.Fatal(raw)
	}
}

func TestFragmentedSignallingFollowsActualDolbyVisionLayer(t *testing.T) {
	facts := matrixToolchain()
	qualifiedCopy := func(in *DeliveryInput) { in.FMP4Output = true; in.Server = facts }
	detail := hdr10Detail
	detail.DynamicRange = mediafacts.RangeDolbyVision
	detail.DolbyVisionProfile = 8
	detail.DolbyVisionLevel = 6
	detail.DolbyVisionCompatibility = 1
	detail.CodecTag = "dvh1"
	source := videoSource("mkv", "hevc", detail, SourceAudio{Codec: "eac3", Channels: 6})
	p := decide(t, source, appleTVProfile(), qualifiedCopy)
	m := string(hlsMasterManifest(&p))
	if !strings.Contains(m, `CODECS="dvh1.08.06,ec-3"`) || !strings.Contains(m, `SUPPLEMENTAL-CODECS="dvh1.08.06/db1p"`) || !strings.Contains(m, "VIDEO-RANGE=PQ") {
		t.Fatal(m)
	}
	client := appleTVProfile()
	client.Video[1].DolbyVisionProfiles = nil
	client.Video[1].DynamicRanges = []string{"sdr", "hdr10", "hlg"}
	client.Display.DynamicRanges = []string{"sdr", "hdr10", "hlg"}
	// A device without Dolby Vision is sent the title's own HDR10 base layer,
	// copied, which needs a converter that can strip the Dolby Vision data.
	p = decide(t, source, client, qualifiedCopy)
	if p.VideoAction != "copy" {
		t.Fatalf("expected qualified base copy, got %+v", p)
	}
	if p.Strategy != DeliveryCopyRemux && p.Strategy != DeliveryAudioConversion {
		t.Fatal("the base layer was not copied", p.Strategy)
	}
	m = string(hlsMasterManifest(&p))
	if strings.Contains(m, "dvh1") || strings.Contains(m, "SUPPLEMENTAL") || !strings.Contains(m, "hvc1.2.4.L153.B0") || !strings.Contains(m, "VIDEO-RANGE=PQ") {
		t.Fatal(m)
	}
	source.Video.DolbyVisionCompatibility = 4
	p = decide(t, source, client, qualifiedCopy)
	if p.VideoAction != "copy" {
		t.Fatalf("expected qualified HLG base copy, got %+v", p)
	}
	if !strings.Contains(string(hlsMasterManifest(&p)), "VIDEO-RANGE=HLG") {
		t.Fatal(string(hlsMasterManifest(&p)))
	}
	p.OutputVideoCodec = "av1"
	p.Trace.Video = &TraceVideo{Level: 5, BitDepth: 8}
	if videoCodecString(&p) != "av01.0.05M.08" {
		t.Fatal(videoCodecString(&p))
	}
}

func TestRenditionsKeepChosenTrackBeyondCapAndDisambiguateTitles(t *testing.T) {
	tracks := make([]SourceAudio, 20)
	for i := range tracks {
		tracks[i] = SourceAudio{Codec: "aac", Channels: 2, Language: "eng", Title: "Same title"}
	}
	source := videoSource("mkv", "h264", avcDetail, tracks...)
	p := decide(t, source, chromeProfile(), func(in *DeliveryInput) { in.HasAudioChoice = true; in.AudioStream = 20 })
	if len(p.Renditions) != 16 || p.Renditions[15].StreamIndex != 20 || !p.Renditions[15].Default {
		t.Fatal(p.Renditions)
	}
	labels := map[string]bool{}
	for _, r := range p.Renditions {
		if labels[r.Label] {
			t.Fatal("duplicate label")
		}
		labels[r.Label] = true
	}
}

func TestDolbyVisionManifestDoesNotInventMissingSourceLevel(t *testing.T) {
	detail := hdr10Detail
	detail.DynamicRange = mediafacts.RangeDolbyVision
	detail.DolbyVisionProfile = 8
	detail.DolbyVisionCompatibility = 1
	detail.CodecTag = "dvh1"
	source := videoSource("mkv", "hevc", detail, SourceAudio{Codec: "eac3", Channels: 6})
	p := decide(t, source, appleTVProfile(), func(in *DeliveryInput) { in.FMP4Output = true; in.Server = matrixToolchain() })
	if p.Trace.Video.DolbyVisionLevel != 0 {
		t.Fatal("fixture unexpectedly has a DV level")
	}
	manifest := string(hlsMasterManifest(&p))
	if strings.Contains(manifest, "CODECS=") {
		t.Fatal("unknown DV level became a codec claim", manifest)
	}
	if !strings.Contains(manifest, "VIDEO-RANGE=PQ") {
		t.Fatal("actual compatible base range lost", manifest)
	}
}

// Apple's HLS authoring specification, exactly (VIDEO.md VS-1d): profile 5 is
// a dvh1 sample entry on its own, 8.4 keeps its HLG base and names the db4h
// brand in SUPPLEMENTAL-CODECS, and neither is signalled as anything else.
func TestAppleHLSReportsTheDeliveredDolbyVisionProfile(t *testing.T) {
	facts := matrixToolchain()
	qualified := func(in *DeliveryInput) { in.FMP4Output = true; in.Server = facts }
	eac3 := SourceAudio{Codec: "eac3", Channels: 6}

	p5 := hdr10Detail
	p5.DynamicRange, p5.DolbyVisionProfile, p5.DolbyVisionLevel, p5.CodecTag = mediafacts.RangeDolbyVision, 5, 6, "dvh1"
	manifest := string(hlsMasterManifest(func() *DeliveryPlan {
		p := decide(t, videoSource("mkv", "hevc", p5, eac3), appleTVProfile(), qualified)
		return &p
	}()))
	if !strings.Contains(manifest, `CODECS="dvh1.05.06,ec-3"`) || strings.Contains(manifest, "SUPPLEMENTAL-CODECS") || !strings.Contains(manifest, "VIDEO-RANGE=PQ") {
		t.Fatal(manifest)
	}

	p84 := hdr10Detail
	p84.DynamicRange, p84.DolbyVisionProfile, p84.DolbyVisionCompatibility, p84.DolbyVisionLevel, p84.CodecTag = mediafacts.RangeDolbyVision, 8, 4, 6, "dvh1"
	client := appleTVProfile()
	for i := range client.Video {
		if client.Video[i].Codec == "hevc" {
			client.Video[i].DolbyVisionProfile8Compatibility = []int{1, 4}
		}
	}
	manifest = string(hlsMasterManifest(func() *DeliveryPlan {
		p := decide(t, videoSource("mkv", "hevc", p84, eac3), client, qualified)
		return &p
	}()))
	if !strings.Contains(manifest, `CODECS="dvh1.08.06,ec-3"`) || !strings.Contains(manifest, `SUPPLEMENTAL-CODECS="dvh1.08.06/db4h"`) || !strings.Contains(manifest, "VIDEO-RANGE=HLG") {
		t.Fatal(manifest)
	}
}
