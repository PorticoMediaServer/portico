package playback

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
)

func TestPlaybackComparisonKeepsOriginalAndRepackagedDistinct(t *testing.T) {
	for _, container := range []string{"mp4", "mkv"} {
		t.Run(container, func(t *testing.T) {
			source := videoSource(container, "h264", assets.StreamDetail{Width: 1920, Height: 1080, BitDepth: 8, DynamicRange: assets.RangeSDR, StreamBreadth: assets.StreamBreadth{InterlaceAnalysis: assets.InterlaceFieldOrderProgressive}}, SourceAudio{Codec: "aac", Channels: 2, Default: true, StreamDetail: assets.StreamDetail{SampleRate: 48000}})
			plan := decide(t, source, appleTVProfile(), nil)
			plan.TargetReach = "remote"
			info := PlaybackInfoForPlan(plan, nil)
			want := "original"
			if container == "mkv" {
				want = "repackaged"
			}
			if info == nil || info.Route != want || info.Routes.Picture != want || info.Routes.Sound != want || info.Network != "away" || len(info.Losses) != 0 || info.Basis != "committed_delivery_plan" {
				t.Fatalf("comparison %+v plan=%+v", info, plan)
			}
			if info.Source.Video.Interlaced == nil || *info.Source.Video.Interlaced || info.Delivered.Video.Interlaced == nil || *info.Delivered.Video.Interlaced {
				t.Fatal("actual progressive fact lost", info)
			}
			if info.Source.Audio.Layout != "" || info.Delivered.Audio.Layout != "" {
				t.Fatal("count became layout", info)
			}
			raw, err := json.Marshal(info)
			if err != nil {
				t.Fatal(err)
			}
			for _, private := range []string{"sourceId", "grant", "hardware", "deviceId", "sessionId", "url", "observed", "screen", "pcm"} {
				if strings.Contains(string(raw), `"`+private+`"`) {
					t.Fatalf("private/inferred field %s in %s", private, raw)
				}
			}
			*info.Delivered.Video.Interlaced = true
			if *plan.Trace.Video.Interlaced || *info.Source.Video.Interlaced {
				t.Fatal("comparison mutated source snapshot")
			}
		})
	}
	if PlaybackInfoForPlan(DeliveryPlan{}, nil) != nil || PlaybackInfoForPlan(DeliveryPlan{Trace: &DecisionTrace{}}, nil) != nil {
		t.Fatal("invented legacy source report")
	}
}

func TestPlaybackComparisonProfile7RewriteIsConvertedWithoutRelabelingRawRoute(t *testing.T) {
	for _, layer := range []string{"MEL", "FEL"} {
		t.Run(layer, func(t *testing.T) {
			plan := decide(t, observedProfile7Source(layer), appleTVProfile(), func(in *DeliveryInput) { in.Server = packetQualifiedUnitFacts(); in.FMP4Output = true })
			before, _ := json.Marshal(plan.Trace)
			info := PlaybackInfoForPlan(plan, nil)
			if plan.Strategy != DeliveryCopyRemux || plan.VideoAction != "copy" || info.Route != "converted" || info.Routes.Picture != "converted" || info.Source.Video.DolbyVisionProfile != 7 || info.Source.Video.DolbyVisionEnhancementType != layer || info.Source.Video.DolbyVisionRPU == nil || !*info.Source.Video.DolbyVisionRPU || info.Delivered.Video.DolbyVisionProfile != 8 || info.Delivered.Video.DolbyVisionCompatibility != 1 || info.Delivered.Video.DolbyVisionEnhancementType != "" {
				t.Fatalf("source/delivery/route mixed: %+v", info)
			}
			conversion := info.Delivered.Video.Conversion
			if conversion == nil || conversion.Kind != decoder.DolbyVisionProfile7To81 || !conversion.EnhancementDiscarded || conversion.FELResidualDiscarded != (layer == "FEL") || conversion.SourceEnhancementType != layer {
				t.Fatal("packet conversion loss omitted", conversion)
			}
			if len(info.Losses) != 0 {
				t.Fatal("DV preserved rewrite claimed DV lost", info.Losses)
			}
			after, _ := json.Marshal(plan.Trace)
			if string(before) != string(after) {
				t.Fatal("report changed committed source")
			}
		})
	}
}

func TestPlaybackComparisonTargetFactsNeverBecomeRendererObservations(t *testing.T) {
	progressive := false
	plan := DeliveryPlan{Strategy: DeliveryVideoConversion, VideoAction: "convert", AudioAction: "convert", SourceVideoCodec: "hevc", SourceAudioCodec: "truehd", OutputVideoCodec: "h264", OutputAudioCodec: "flac", OutputContainer: "fmp4_hls", OutputBitDepth: 8, OutputRange: assets.RangeSDR, TargetHeight: 720, AudioChannels: 8, AudioSampleRate: 96000, Deinterlace: true, ReasonCodes: []string{ReasonHeightExceedsPolicy}, VideoBitrateBPS: 3000000, AudioBitrateBPS: 0,
		Trace:            &DecisionTrace{Container: "matroska", Video: &TraceVideo{Codec: "hevc", BitDepth: 10, Width: 1920, Height: 1080, FrameRate: 24, FrameRateNumerator: 24, FrameRateDenominator: 1, FrameRateMode: "vfr", Interlaced: &progressive, DynamicRange: assets.RangeDolbyVision, DolbyVisionProfile: 7, HDR10Plus: true, MasterDisplay: "source mastering", MaxCLL: "1000,400"}, Audio: &TraceAudio{Codec: "truehd", Profile: "source profile", Variant: "truehd_atmos", ObjectAudio: "atmos", Channels: 8, Layout: "7.1", SampleRate: 96000, BitDepth: 24}},
		ConversionTarget: &decoder.ConversionTarget{VideoCodec: "h264", VideoProfile: "high", BitDepth: 8, DynamicRange: assets.RangeSDR, AudioCodec: "flac", AudioChannels: 8, AudioSampleRate: 96000, AudioBitDepth: 24, AudioChannelLayout: "7.1"},
	}
	subtitle := &PlaybackSubtitleFacts{Format: "pgs", Language: "eng", Origin: "embedded", Rendering: "burnedIn"}
	info := PlaybackInfoForPlan(plan, subtitle)
	if info.Route != "converted" || info.Reason.Kind != "subtitles" || info.Routes.Subtitles != "converted" || info.Delivered.Video.Width != 1280 || info.Delivered.Video.Height != 720 || info.Delivered.Video.Profile != "high" || info.Delivered.Video.MasterDisplay != "" || info.Delivered.Video.DolbyVisionProfile != 0 || info.Delivered.Video.HDR10Plus != nil || info.Delivered.Video.FrameRateMode != "" || info.Delivered.Video.Interlaced == nil || *info.Delivered.Video.Interlaced {
		t.Fatalf("stale source facts on target: %+v", info)
	}
	if info.Source.Audio.ObjectAudio != "atmos" || info.Delivered.Audio.ObjectAudio != "" || info.Delivered.Audio.Codec != "flac" || info.Delivered.Audio.BitDepth != 24 || info.Delivered.Audio.SampleRate != 96000 || info.Delivered.Audio.Layout != "7.1" {
		t.Fatal("FLAC target lost full rate/depth or invented object retention", info)
	}
	want := []PlaybackLoss{{Feature: "hdr10plus", Replacement: PlaybackReplacement{DynamicRange: "sdr"}}, {Feature: "dolbyVision", Replacement: PlaybackReplacement{DynamicRange: "sdr"}}, {Feature: "hdr", Replacement: PlaybackReplacement{DynamicRange: "sdr"}}, {Feature: "atmos", Replacement: PlaybackReplacement{AudioCodec: "flac", Channels: 8, Layout: "7.1"}}}
	if !reflect.DeepEqual(info.Losses, want) || info.EstimatedBitrateKbps != 0 {
		t.Fatal("structured losses/unknown lossless total", info)
	}
	if info.Source.Subtitles.Rendering != "" || info.Source.Subtitles.Format != "pgs" || info.Delivered.Subtitles.Rendering != "burnedIn" || info.Delivered.Subtitles.Format != "" || subtitle.Rendering != "burnedIn" {
		t.Fatal("subtitle source and rendering mixed", info, subtitle)
	}
}

func TestPlaybackComparisonOmitsPartialOrUnappliedRoutesAndRates(t *testing.T) {
	plan := DeliveryPlan{VideoAction: "none", AudioAction: "none", Trace: &DecisionTrace{Video: &TraceVideo{Codec: "hevc"}}}
	if PlaybackInfoForPlan(plan, nil) != nil {
		t.Fatal("unknown/drop became Original")
	}
	plan = DeliveryPlan{Strategy: DeliveryVideoConversion, VideoAction: "convert", AudioAction: "convert", VideoBitrateBPS: 3000000, AudioBitrateBPS: 320000, OutputVideoCodec: "h264", OutputAudioCodec: "aac", Trace: &DecisionTrace{Video: &TraceVideo{Codec: "hevc"}, Audio: &TraceAudio{Codec: "truehd"}}}
	if info := PlaybackInfoForPlan(plan, nil); info.EstimatedBitrateKbps != 3320 {
		t.Fatal("known aggregate estimate", info)
	}
	plan.VideoBitrateBPS = 0
	if info := PlaybackInfoForPlan(plan, nil); info.EstimatedBitrateKbps != 0 {
		t.Fatal("audio-only rate misreported as aggregate", info)
	}
}

// VIDEO.md VS-10 asks for the sound's format and whether it is passed through
// or decoded. The committed recipe knows which: a passthrough route hands the
// bitstream to the sink, everything else is decoded to PCM first.
func TestPlaybackInfoReportsTheSoundDelivery(t *testing.T) {
	plan := DeliveryPlan{Strategy: DeliveryOriginal, VideoAction: "original", AudioAction: "original", OutputAudioCodec: "eac3", AudioPassthrough: true,
		Trace: &DecisionTrace{Audio: &TraceAudio{Codec: "eac3", ObjectAudio: "atmos", Channels: 8, Layout: "7.1"}}}
	info := PlaybackInfoForPlan(plan, nil)
	if info == nil || info.Source.Audio == nil || info.Source.Audio.Delivery != "" || info.Delivered.Audio == nil || info.Delivered.Audio.Delivery != "passthrough" {
		t.Fatalf("a passed-through track was not reported as one: %+v", info)
	}
	plan.AudioPassthrough = false
	plan.AudioAction = "convert"
	plan.OutputAudioCodec = "aac"
	info = PlaybackInfoForPlan(plan, nil)
	if info == nil || info.Delivered.Audio == nil || info.Delivered.Audio.Delivery != "decoded" || info.Delivered.Audio.Channels != 0 {
		t.Fatalf("a decoded track was not reported as one: %+v", info)
	}
}

// A device that decodes E-AC-3 or TrueHD but renders no objects plays the
// channel bed. The plan records that, and the panel says Atmos was lost rather
// than claiming it on the delivered track (VIDEO.md VS-3c and VC-8).
func TestPlaybackInfoDoesNotClaimObjectsTheEngineCannotRender(t *testing.T) {
	plan := DeliveryPlan{Strategy: DeliveryOriginal, VideoAction: "original", AudioAction: "original", OutputAudioCodec: "eac3",
		Trace: &DecisionTrace{Audio: &TraceAudio{Codec: "eac3", Variant: "eac3_joc", ObjectAudio: "atmos", Channels: 8, Layout: "7.1"}, Notes: []string{NoteObjectAudioDecoded}}}
	info := PlaybackInfoForPlan(plan, nil)
	if info == nil || info.Delivered.Audio == nil || info.Delivered.Audio.ObjectAudio != "" || info.Source.Audio.ObjectAudio != "atmos" {
		t.Fatalf("the delivered track still claimed its objects: %+v", info)
	}
	if len(info.Losses) != 1 || info.Losses[0].Feature != "atmos" || info.Losses[0].Replacement.AudioCodec != "eac3" {
		t.Fatalf("the object loss was not reported: %+v", info.Losses)
	}
}

// A device that renders the objects keeps them, and nothing is reported lost.
func TestPlaybackInfoKeepsObjectsTheEngineRenders(t *testing.T) {
	plan := DeliveryPlan{Strategy: DeliveryOriginal, VideoAction: "original", AudioAction: "original", OutputAudioCodec: "eac3",
		Trace: &DecisionTrace{Audio: &TraceAudio{Codec: "eac3", Variant: "eac3_joc", ObjectAudio: "atmos", Channels: 8, Layout: "7.1"}, Notes: []string{}}}
	info := PlaybackInfoForPlan(plan, nil)
	if info == nil || info.Delivered.Audio == nil || info.Delivered.Audio.ObjectAudio != "atmos" || len(info.Losses) != 0 {
		t.Fatalf("rendered objects were dropped: %+v", info)
	}
}

// The decision records the object loss only for an engine that declares the
// codec without the object kind; one that declares Atmos keeps it.
func TestCopiedObjectsAreLostOnlyWhereTheEngineCannotRenderThem(t *testing.T) {
	document := func(objects string) ClientProfile {
		t.Helper()
		doc, err := ParseCapabilityDocument([]byte(`{"version":2,"client":{"family":"android"},"display":{"dynamicRanges":["sdr"]},"engines":{"native":{"name":"exoplayer","evidence":"declared","video":[{"codec":"h264","bitDepths":[8],"maxWidth":1920,"maxHeight":1080}],"audio":[{"codec":"eac3","maxChannels":8` + objects + `}],"transports":[{"transport":"direct","containers":["mkv"],"video":["h264"],"audio":["eac3"]}]}}}`))
		if err != nil {
			t.Fatal(err)
		}
		return doc.Profile(EngineNative)
	}
	joc := SourceAudio{Codec: "eac3", Channels: 8, StreamDetail: assets.StreamDetail{StreamBreadth: assets.StreamBreadth{AudioVariant: "eac3_joc"}, ObjectAudio: "atmos"}}
	source := videoSource("mkv", "h264", avcDetail, joc)
	bedOnly := decide(t, source, document(``), nil)
	if bedOnly.AudioAction == "convert" || !containsString(bedOnly.Trace.Notes, NoteObjectAudioDecoded) {
		t.Fatalf("a bed-only renderer kept the object claim: %+v", bedOnly.Trace.Notes)
	}
	info := PlaybackInfoForPlan(bedOnly, nil)
	if info == nil || info.Delivered.Audio.ObjectAudio != "" || len(info.Losses) != 1 || info.Losses[0].Feature != "atmos" {
		t.Fatalf("the panel claimed Atmos on a bed-only renderer: %+v", info)
	}
	withObjects := decide(t, source, document(`,"objectAudio":["atmos"]`), nil)
	if containsString(withObjects.Trace.Notes, NoteObjectAudioDecoded) {
		t.Fatalf("an Atmos renderer was told it lost Atmos: %+v", withObjects.Trace.Notes)
	}
	if info := PlaybackInfoForPlan(withObjects, nil); info == nil || info.Delivered.Audio.ObjectAudio != "atmos" {
		t.Fatalf("an Atmos renderer lost its objects: %+v", info)
	}
	// A bed-only decoder that can also pass Atmos to a receiver: the route is
	// unknown until observed, so the plan must not report a confirmed loss.
	bothDoc, err := ParseCapabilityDocument([]byte(`{"version":2,"client":{"family":"android"},"display":{"dynamicRanges":["sdr"]},"audioOutput":{"route":"hdmi","maxChannels":8},"engines":{"native":{"name":"exoplayer","evidence":"declared","video":[{"codec":"h264","bitDepths":[8],"maxWidth":1920,"maxHeight":1080}],"audio":[{"codec":"eac3","maxChannels":8}],"audioPassthrough":[{"codec":"eac3","maxChannels":8,"objectAudio":["atmos"]}],"transports":[{"transport":"direct","containers":["mkv"],"video":["h264"],"audio":["eac3"]}]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	both := decide(t, source, bothDoc.Profile(EngineNative), nil)
	if containsString(both.Trace.Notes, NoteObjectAudioDecoded) || both.AudioPassthrough {
		t.Fatalf("decode-or-passthrough was reported as decided: passthrough=%v notes=%+v", both.AudioPassthrough, both.Trace.Notes)
	}
	if info := PlaybackInfoForPlan(both, nil); info == nil || len(info.Losses) != 0 {
		t.Fatalf("decode-or-passthrough claimed a loss: %+v", info)
	}
}

// A device that shows HDR10 but not HDR10+ plays the HDR10 base picture. The
// panel keeps the base picture and reports the dynamic metadata as lost
// (VIDEO.md VS-2b), while a device that declares HDR10+ keeps it.
func TestHDR10PlusMetadataLossIsReportedOnlyWhereItIsLost(t *testing.T) {
	device := func(ranges string) ClientProfile {
		t.Helper()
		doc, err := ParseCapabilityDocument([]byte(`{"version":2,"client":{"family":"androidtv"},"display":{"dynamicRanges":` + ranges + `},"engines":{"native":{"name":"exoplayer","evidence":"declared","video":[{"codec":"hevc","bitDepths":[8,10],"maxWidth":1920,"maxHeight":1080,"dynamicRanges":` + ranges + `}],"audio":[{"codec":"eac3","maxChannels":6}],"transports":[{"transport":"direct","containers":["mkv"],"video":["hevc"],"audio":["eac3"]}]}}}`))
		if err != nil {
			t.Fatal(err)
		}
		return doc.Profile(EngineNative)
	}
	plus := hdr10Detail
	plus.HDR10Plus, plus.Width, plus.Height = true, 1920, 1080
	source := videoSource("mkv", "hevc", plus, SourceAudio{Index: 1, Codec: "eac3", Channels: 6})

	plan := decide(t, source, device(`["sdr","hdr10"]`), nil)
	if plan.VideoAction == "convert" || !containsString(plan.Trace.Notes, NoteHDR10PlusLost) {
		t.Fatalf("an HDR10-only device was not told the metadata was lost: %+v", plan.Trace.Notes)
	}
	info := PlaybackInfoForPlan(plan, nil)
	if info == nil || info.Delivered.Video == nil || info.Delivered.Video.HDR10Plus != nil || info.Delivered.Video.DynamicRange != assets.RangeHDR10 {
		t.Fatalf("the delivered picture still claimed HDR10+: %+v", info)
	}
	var lost bool
	for _, loss := range info.Losses {
		lost = lost || loss.Feature == "hdr10plus" && loss.Replacement.DynamicRange == assets.RangeHDR10
	}
	if !lost {
		t.Fatalf("the HDR10+ loss was not reported: %+v", info.Losses)
	}

	kept := decide(t, source, device(`["sdr","hdr10","hdr10plus"]`), nil)
	if containsString(kept.Trace.Notes, NoteHDR10PlusLost) {
		t.Fatalf("an HDR10+ device was told it lost the metadata: %+v", kept.Trace.Notes)
	}
	if info := PlaybackInfoForPlan(kept, nil); info == nil || info.Delivered.Video.HDR10Plus == nil || !*info.Delivered.Video.HDR10Plus {
		t.Fatalf("an HDR10+ device lost its metadata: %+v", info)
	}
}

func TestHDR10PlusConversionReportsMetadataLossForHDRAndSDR(t *testing.T) {
	plus := hdr10Detail
	plus.HDR10Plus = true
	source := videoSource("mkv", "hevc", plus, SourceAudio{Codec: "aac", Channels: 2})
	for _, allowHDR := range []bool{true, false} {
		plan := decide(t, source, appleTVProfile(), func(in *DeliveryInput) {
			in.FMP4Output = true
			in.Server = matrixToolchain()
			in.Target = QualityTarget{RungID: "720p", Kind: QualityFixed, MaxVideoHeight: 720, MaxVideoBitrateBPS: 2_000_000, AllowHDR: allowHDR}
		})
		if plan.VideoAction != "convert" || !containsString(plan.Trace.Notes, NoteHDR10PlusLost) {
			t.Fatal("conversion did not record HDR10+ loss", plan)
		}
		info := PlaybackInfoForPlan(plan, nil)
		if info == nil || info.Source.Video.HDR10Plus == nil || !*info.Source.Video.HDR10Plus || info.Delivered.Video.HDR10Plus != nil {
			t.Fatal("source and delivered metadata facts disagree", info)
		}
		wantRange := assets.RangeSDR
		if allowHDR {
			wantRange = assets.RangeHDR10
		}
		if info.Delivered.Video.DynamicRange != wantRange {
			t.Fatal("conversion did not exercise the requested delivered range", info.Delivered.Video)
		}
		losses := 0
		for _, loss := range info.Losses {
			if loss.Feature == "hdr10plus" {
				losses++
				if loss.Replacement.DynamicRange != info.Delivered.Video.DynamicRange {
					t.Fatal("loss names the wrong delivered range", loss, info.Delivered.Video)
				}
			}
		}
		if losses != 1 {
			t.Fatal("conversion omitted or duplicated HDR10+ loss", info.Losses)
		}
	}
}
