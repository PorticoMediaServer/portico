package playback

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
)

// rokuDocument is the document the Roku channel publishes from roDeviceInfo
// (packages/client-core/src/client-profile.ts `rokuDeclaredProfile`, widened by
// the channel): a 4K model that reports Dolby Vision and AV1. VIDEO.md VC-5b.
func rokuDocument(t *testing.T) ClientProfile {
	t.Helper()
	doc, err := ParseCapabilityDocument([]byte(`{
 "version":2,"engineSetting":"automatic",
 "client":{"family":"roku","form":"tv","platform":"roku","os":"roku","model":"4800X","app":"portico-roku"},
 "display":{"width":3840,"height":2160,"dynamicRanges":["sdr","hdr10","hdr10plus","hlg","dolby_vision"],"maxFrameRate":60},
 "audioOutput":{"route":"hdmi","maxChannels":8},
 "engines":{"native":{"name":"roku-video","evidence":"declared",
  "video":[
   {"codec":"h264","bitDepths":[8,10],"maxWidth":3840,"maxHeight":2160,"maxLevel":51,"maxFrameRate":60,"dynamicRanges":["sdr","dolby_vision"],"dolbyVisionProfiles":[9],"codecTags":["avc1","dva1","dvav"]},
   {"codec":"hevc","bitDepths":[8,10],"maxWidth":3840,"maxHeight":2160,"maxLevel":153,"maxFrameRate":60,"dynamicRanges":["sdr","hdr10","hdr10plus","hlg","dolby_vision"],"dolbyVisionProfiles":[5],"codecTags":["dvh1","dvhe","hev1","hvc1"]},
   {"codec":"vp9","bitDepths":[8,10],"maxWidth":3840,"maxHeight":2160,"maxFrameRate":60,"dynamicRanges":["sdr","hdr10","hlg"]},
   {"codec":"av1","bitDepths":[8,10],"maxWidth":3840,"maxHeight":2160,"maxFrameRate":60,"dynamicRanges":["sdr","hdr10","hdr10plus","hlg"]}],
  "audio":[{"codec":"aac","maxChannels":6},{"codec":"mp3","maxChannels":2},{"codec":"flac","maxChannels":2},{"codec":"pcm","maxChannels":2},{"codec":"alac","maxChannels":2},{"codec":"opus","maxChannels":2},{"codec":"vorbis","maxChannels":2}],
  "audioPassthrough":[{"codec":"ac3","maxChannels":6},{"codec":"eac3","maxChannels":8,"objectAudio":["atmos"]},{"codec":"dts","variants":["dts_core"],"maxChannels":8}],
  "transports":[
   {"transport":"direct","containers":["mp4","m4v","mov","mkv","ts","m2ts"],"video":["h264","hevc","vp9","av1"],"audio":["aac","mp3","ac3","eac3","flac","pcm","alac","opus","vorbis","dts"]},
   {"transport":"hls_ts","containers":["mpegts"],"video":["h264","hevc","vp9","av1"],"audio":["aac","mp3","ac3","eac3"]},
   {"transport":"hls_fmp4","containers":["fmp4"],"video":["h264","hevc","vp9","av1"],"audio":["aac","ac3","eac3"]}],
  "subtitleRendering":["text"]}}}`))
	if err != nil {
		t.Fatal(err)
	}
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
		p, err := planDelivery(DeliveryInput{Source: source, Client: client, Server: matrixToolchain(), Policy: policy, Target: autoTarget(policy, source.Video.Height), Config: DefaultDeliveryConfiguration(), TranscodingEnabled: true, FMP4Output: true})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	p5 := hdr10Detail
	p5.DynamicRange, p5.DolbyVisionProfile, p5.CodecTag = assets.RangeDolbyVision, 5, "dvh1"
	atmos := SourceAudio{Codec: "eac3", Channels: 8, StreamDetail: assets.StreamDetail{ObjectAudio: "atmos"}}
	plan5 := plan(videoSource("mp4", "hevc", p5, atmos))
	info := PlaybackInfoForPlan(plan5, nil)
	if plan5.Strategy != DeliveryOriginal || info == nil || info.Delivered.Video.DolbyVisionProfile != 5 || plan5.OutputAudioCodec != "eac3" || !plan5.AudioPassthrough {
		t.Fatalf("Dolby Vision 5 with Atmos was not left alone: %+v", plan5)
	}

	// The models in this document report profiles 5 and 9 only: an 8.1 film
	// plays its HDR10 base picture and the panel says Dolby Vision was lost.
	p81 := hdr10Detail
	p81.DynamicRange, p81.DolbyVisionProfile, p81.DolbyVisionCompatibility, p81.CodecTag = assets.RangeDolbyVision, 8, 1, "dvh1"
	plan81 := plan(videoSource("mp4", "hevc", p81, atmos))
	if info := PlaybackInfoForPlan(plan81, nil); info == nil || info.Delivered.Video.DolbyVisionProfile != 0 || info.Delivered.Video.DynamicRange != assets.RangeHDR10 || len(info.Losses) == 0 || info.Losses[0].Feature != "dolbyVision" {
		t.Fatalf("Dolby Vision 8.1 was not honestly reduced to HDR10 for this model: %+v", info)
	}

	// Profile 9 rides AVC (`dvav.09`), which this document declares.
	p9 := avcDetail
	p9.BitDepth = 10
	p9.DynamicRange, p9.DolbyVisionProfile, p9.CodecTag = assets.RangeDolbyVision, 9, "dvav"
	if plan9 := plan(videoSource("mkv", "h264", p9, atmos)); plan9.Strategy != DeliveryOriginal {
		t.Fatalf("Dolby Vision 9 on AVC did not play as it is: %+v", plan9)
	} else if info := PlaybackInfoForPlan(plan9, nil); info == nil || info.Delivered.Video.DolbyVisionProfile != 9 {
		t.Fatalf("Dolby Vision 9 was stripped: %+v", info)
	}

	hdr10plus := hdr10Detail
	hdr10plus.DynamicRange, hdr10plus.HDR10Plus = assets.RangeHDR10, true
	av1Detail := hdr10plus
	av1Detail.CodecTag = "av01"
	planPlus := plan(videoSource("mkv", "av1", av1Detail, atmos))
	plusInfo := PlaybackInfoForPlan(planPlus, nil)
	if planPlus.Strategy != DeliveryOriginal || plusInfo == nil || plusInfo.Delivered.Video.HDR10Plus == nil || !*plusInfo.Delivered.Video.HDR10Plus {
		t.Fatalf("HDR10+ on AV1 lost its dynamic metadata: %+v", planPlus)
	}

	dts := SourceAudio{Codec: "dts", Channels: 6, StreamDetail: assets.StreamDetail{StreamBreadth: assets.StreamBreadth{AudioVariant: "dts_core"}}}
	planDTS := plan(videoSource("mkv", "h264", avcDetail, dts))
	if planDTS.Strategy != DeliveryOriginal || planDTS.OutputAudioCodec != "dts" || !planDTS.AudioPassthrough {
		t.Fatalf("a DTS core was not passed to the sink: %+v", planDTS)
	}

	// Profile 7 is not one of the profiles this model declares; its HDR10 base
	// layer plays, and the notice says Dolby Vision was lost.
	p7 := hdr10Detail
	p7.DynamicRange, p7.DolbyVisionProfile, p7.DolbyVisionCompatibility, p7.CodecTag = assets.RangeDolbyVision, 7, 6, "dvhe"
	p7.DolbyVisionEnhancementType = "fel"
	plan7 := plan(videoSource("mkv", "hevc", p7, atmos))
	sevenInfo := PlaybackInfoForPlan(plan7, nil)
	if plan7.Strategy != DeliveryCopyRemux || sevenInfo == nil || sevenInfo.Delivered.Video.DolbyVisionProfile != 0 || sevenInfo.Delivered.Video.DynamicRange == assets.RangeDolbyVision {
		t.Fatalf("profile 7 was signalled as Dolby Vision: %+v %+v", plan7, sevenInfo)
	}
}
