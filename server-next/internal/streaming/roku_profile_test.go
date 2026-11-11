package streaming

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
)

// rokuDocument is the recorded 4K Roku that reports Dolby Vision and AV1
// (profiles/roku_4k.json), with a receiver on its HDMI output.
func rokuDocument(t *testing.T) ClientProfile {
	t.Helper()
	doc := recordedDocuments(t)["roku_4k"]
	doc.AudioOutput.MaxChannels = 8
	return doc.Profile(EngineNative)
}

// The Roku document's declared ceiling is what it plans: Dolby Vision 5 and 9
// play as Dolby Vision, HDR10+ on AV1 keeps its dynamic metadata, DTS reaches
// the sink as it is, and Matroska is direct. A profile 7 disc must not be
// offered as Dolby Vision the television cannot apply.
func TestRokuDeclaredDocumentPlansItsCeiling(t *testing.T) {
	client := rokuDocument(t)
	plan := func(source *DeliverySource) DeliveryPlan {
		t.Helper()
		policy := openPolicy()
		p, err := planDelivery(
			DeliveryInput{
				Source:             source,
				Client:             client,
				Server:             matrixToolchain(),
				Policy:             policy,
				Target:             autoTarget(policy, source.Video.Height),
				Config:             DefaultDeliveryConfiguration(),
				TranscodingEnabled: true,
				FMP4Output:         true,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	p5 := hdr10Detail
	p5.DynamicRange, p5.DolbyVisionProfile, p5.CodecTag = mediafacts.RangeDolbyVision, 5, "dvh1"
	atmos := SourceAudio{Codec: "eac3", Channels: 8, StreamDetail: mediafacts.StreamDetail{ObjectAudio: "atmos"}}
	plan5 := plan(videoSource("mp4", "hevc", p5, atmos))
	info := PlaybackInfoForPlan(plan5, nil)
	if plan5.Strategy != DeliveryOriginal || info == nil || info.Delivered.Video.DolbyVisionProfile != 5 || plan5.OutputAudioCodec != "eac3" || !plan5.AudioPassthrough {
		t.Fatalf("Dolby Vision 5 with Atmos was not left alone: %+v", plan5)
	}

	// The models in this document report profiles 5 and 9 only: an 8.1 film
	// plays its HDR10 base picture and the panel says Dolby Vision was lost.
	p81 := hdr10Detail
	p81.DynamicRange, p81.DolbyVisionProfile, p81.DolbyVisionCompatibility, p81.CodecTag = mediafacts.RangeDolbyVision, 8, 1, "dvh1"
	plan81 := plan(videoSource("mp4", "hevc", p81, atmos))
	if info := PlaybackInfoForPlan(plan81, nil); info == nil || info.Delivered.Video.DolbyVisionProfile != 0 || info.Delivered.Video.DynamicRange != mediafacts.RangeHDR10 || len(info.Losses) == 0 ||
		info.Losses[0].Feature != "dolbyVision" {
		t.Fatalf("Dolby Vision 8.1 was not honestly reduced to HDR10 for this model: %+v", info)
	}

	// Profile 9 rides AVC (`dvav.09`), which this document declares.
	p9 := avcDetail
	p9.BitDepth = 10
	p9.DynamicRange, p9.DolbyVisionProfile, p9.CodecTag = mediafacts.RangeDolbyVision, 9, "dvav"
	if plan9 := plan(videoSource("mkv", "h264", p9, atmos)); plan9.Strategy != DeliveryOriginal {
		t.Fatalf("Dolby Vision 9 on AVC did not play as it is: %+v", plan9)
	} else if info := PlaybackInfoForPlan(plan9, nil); info == nil || info.Delivered.Video.DolbyVisionProfile != 9 {
		t.Fatalf("Dolby Vision 9 was stripped: %+v", info)
	}

	hdr10plus := hdr10Detail
	hdr10plus.DynamicRange, hdr10plus.HDR10Plus = mediafacts.RangeHDR10, true
	av1Detail := hdr10plus
	av1Detail.CodecTag = "av01"
	planPlus := plan(videoSource("mkv", "av1", av1Detail, atmos))
	plusInfo := PlaybackInfoForPlan(planPlus, nil)
	if planPlus.Strategy != DeliveryOriginal || plusInfo == nil || plusInfo.Delivered.Video.HDR10Plus == nil || !*plusInfo.Delivered.Video.HDR10Plus {
		t.Fatalf("HDR10+ on AV1 lost its dynamic metadata: %+v", planPlus)
	}

	dts := SourceAudio{Codec: "dts", Channels: 6, StreamDetail: mediafacts.StreamDetail{StreamBreadth: mediafacts.StreamBreadth{AudioVariant: "dts_core"}}}
	planDTS := plan(videoSource("mkv", "h264", avcDetail, dts))
	if planDTS.Strategy != DeliveryOriginal || planDTS.OutputAudioCodec != "dts" || !planDTS.AudioPassthrough {
		t.Fatalf("a DTS core was not passed to the sink: %+v", planDTS)
	}

	// Profile 7 is not one of the profiles this model declares; its HDR10 base
	// layer plays, and the notice says Dolby Vision was lost.
	p7 := hdr10Detail
	p7.DynamicRange, p7.DolbyVisionProfile, p7.DolbyVisionCompatibility, p7.CodecTag = mediafacts.RangeDolbyVision, 7, 6, "dvhe"
	p7.DolbyVisionEnhancementType = "fel"
	plan7 := plan(videoSource("mkv", "hevc", p7, atmos))
	sevenInfo := PlaybackInfoForPlan(plan7, nil)
	if plan7.Strategy != DeliveryCopyRemux || sevenInfo == nil || sevenInfo.Delivered.Video.DolbyVisionProfile != 0 || sevenInfo.Delivered.Video.DynamicRange == mediafacts.RangeDolbyVision {
		t.Fatalf("profile 7 was signalled as Dolby Vision: %+v %+v", plan7, sevenInfo)
	}
}
