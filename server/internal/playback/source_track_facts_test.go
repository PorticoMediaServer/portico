package playback

import (
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"testing"
)

// An engine that cannot select an original-file stream receives a remux of the
// chosen track. An arbitrary default track must not replace the selection or
// lend its object metadata to it.
func TestRemuxForUnselectableOriginalReportsChosenTrackObjects(t *testing.T) {
	for _, firstObjects := range []string{"", "atmos"} {
		t.Run("first-"+firstObjects, func(t *testing.T) {
			selectedObjects := "atmos"
			firstChannels, selectedChannels := 2, 8
			if firstObjects != "" {
				selectedObjects = ""
				firstChannels, selectedChannels = 8, 2
			}
			source := videoSource("mp4", "h264", assets.StreamDetail{Width: 1920, Height: 1080, BitDepth: 8, DynamicRange: assets.RangeSDR},
				SourceAudio{Codec: "eac3", Channels: firstChannels, Language: "eng", Default: true, StreamDetail: assets.StreamDetail{ObjectAudio: firstObjects, SampleRate: 48000, StreamBreadth: assets.StreamBreadth{AudioVariant: assets.AudioVariant("eac3", "", firstObjects)}}},
				SourceAudio{Codec: "eac3", Channels: selectedChannels, Language: "jpn", StreamDetail: assets.StreamDetail{ObjectAudio: selectedObjects, SampleRate: 44100, StreamBreadth: assets.StreamBreadth{AudioVariant: assets.AudioVariant("eac3", "", selectedObjects)}}})
			client := appleTVProfile()
			client.EmbeddedAudioSwitching = false
			plan := decide(t, source, client, func(in *DeliveryInput) {
				in.HasAudioChoice, in.AudioStream = true, 2
				in.Config.ConversionMode = "never"
				in.TranscodingEnabled = false
				in.Policy.Transcode = "never"
			})
			if plan.Strategy != DeliveryCopyRemux || plan.AudioStream != 2 || plan.Trace == nil || plan.Trace.Audio == nil {
				t.Fatal("chosen track was not remuxed", plan)
			}
			facts := plan.Trace.Audio
			if facts.StreamIndex != 2 || facts.ObjectAudio != selectedObjects || facts.Codec != "eac3" || facts.Channels != selectedChannels || facts.Language != "jpn" || facts.Tracks != 2 || facts.SampleRate != 44100 || facts.Variant != assets.AudioVariant("eac3", "", selectedObjects) {
				t.Fatalf("another track contaminated selected-source facts: %+v", facts)
			}
		})
	}
}

func TestUnselectableOriginalNeverFallsBackToUncheckedAudio(t *testing.T) {
	for _, first := range []SourceAudio{
		{Codec: "dts", Channels: 6},
		{Codec: "aac", Channels: 8},
		{Codec: "aac", Channels: 2, StreamDetail: assets.StreamDetail{SampleRate: 192000}},
		{Codec: "aac", Channels: 2, StreamDetail: assets.StreamDetail{BitRate: 512000}},
	} {
		client := chromeProfile()
		client.Audio[0].MaxSampleRate = 48000
		first.Default = true
		source := videoSource("mp4", "h264", assets.StreamDetail{Width: 1920, Height: 1080, BitDepth: 8, DynamicRange: assets.RangeSDR}, SourceAudio{Codec: "aac", Channels: 2, StreamDetail: assets.StreamDetail{SampleRate: 48000, BitRate: 128000}}, first)
		in := DeliveryInput{Source: source, Client: client, NoHLS: true, HasAudioChoice: true, AudioStream: 1, Policy: openPolicy(), Target: QualityTarget{AllowHDR: true, MaxAudioBitrateBPS: 192000}, Config: DefaultDeliveryConfiguration()}
		plan, err := planDelivery(in)
		if err == nil || plan.Trace == nil {
			t.Fatal("unselectable original admitted", first, plan, err)
		}
		original := routeEvaluation(plan.Trace, DeliveryOriginal)
		if original == nil || original.Admissible || !hasRejection(original.Rejections, RejectAudioTrack) {
			t.Fatal("missing truthful track-selection refusal", original)
		}
		if plan.AudioStream != 1 {
			t.Fatal("refusal silently substituted another track", plan.AudioStream)
		}
	}
}

func TestOriginalAudioUsesEngineDefaultWithoutEmbeddedSwitching(t *testing.T) {
	source := videoSource("mp4", "h264", assets.StreamDetail{Width: 1920, Height: 1080, BitDepth: 8, DynamicRange: assets.RangeSDR},
		SourceAudio{Codec: "aac", Channels: 2, Language: "eng"},
		SourceAudio{Codec: "aac", Channels: 2, Language: "fra", Default: true})
	client := chromeProfile()
	plan := decide(t, source, client, func(in *DeliveryInput) { in.HasAudioChoice, in.AudioStream = true, 1 })
	if plan.Strategy != DeliveryCopyRemux || plan.AudioStream != 1 {
		t.Fatal("ordinal zero was mistaken for an enforced engine selection", plan)
	}
	plan = decide(t, source, client, func(in *DeliveryInput) { in.HasAudioChoice, in.AudioStream = true, 2 })
	if plan.Strategy != DeliveryOriginal || plan.AudioStream != 2 || !routeEvaluation(plan.Trace, DeliveryOriginal).Admissible {
		t.Fatal("flagged default at ordinal 2 was needlessly remuxed", plan)
	}
	client.EmbeddedAudioSwitching = true
	plan = decide(t, source, client, func(in *DeliveryInput) { in.HasAudioChoice, in.AudioStream = true, 1 })
	if plan.Strategy != DeliveryOriginal || plan.AudioStream != 1 {
		t.Fatal("a selection-capable engine was needlessly remuxed", plan)
	}
	client.EmbeddedAudioSwitching = false
	source.Audio = source.Audio[:1]
	plan = decide(t, source, client, nil)
	if plan.Strategy != DeliveryOriginal {
		t.Fatal("a single audio track was needlessly remuxed", plan)
	}
}

func TestUnselectableOriginalFallsBackToCheckedEngineDefault(t *testing.T) {
	for _, flagged := range []bool{false, true} {
		source := videoSource("mp4", "h264", avcDetail,
			SourceAudio{Codec: "aac", Channels: 2, Language: "eng"},
			SourceAudio{Codec: "aac", Channels: 2, Language: "fra", Default: flagged})
		selected, want := 2, 1
		if flagged {
			selected, want = 1, 2
		}
		plan := decide(t, source, chromeProfile(), func(in *DeliveryInput) {
			in.NoHLS, in.HasAudioChoice, in.AudioStream = true, true, selected
			in.Config.ConversionMode = "never"
			in.TranscodingEnabled = false
			in.Policy.Transcode = "never"
		})
		original := routeEvaluation(plan.Trace, DeliveryOriginal)
		if plan.Strategy != DeliveryOriginal || plan.AudioStream != want || original == nil || !original.Admissible || !hasRejection(original.Rejections, RejectAudioTrack) {
			t.Fatal("checked engine-default fallback was not admitted", plan)
		}
		if !containsString(plan.Trace.Notes, NoteFirstAudioTrackPlayed) || plan.Trace.Audio == nil || plan.Trace.Audio.StreamIndex != want || plan.Trace.Audio.ChosenBy != "only_reachable_track" {
			t.Fatal("fallback lost its actual-track trace", plan.Trace)
		}
	}
}
