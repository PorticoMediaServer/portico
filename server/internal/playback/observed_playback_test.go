package playback

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestObservedPlaybackKeepsSparseActualFactsAndRejectsUnboundedValues(t *testing.T) {
	actual := ObservedPlayback{
		Video: &ObservedVideo{Width: 1920, Height: 1080, FrameRateHz: 23.976},
		Audio: &ObservedAudioOutput{Channels: 2, SampleRate: 44100},
	}
	if !actual.Valid() {
		t.Fatal("valid measured fields refused")
	}
	raw, err := json.Marshal(actual)
	if err != nil {
		t.Fatal(err)
	}
	for _, unsupported := range []string{"screen", "handling", "bitDepth", "objectAudio", "dolby", "hardware", "url", "route"} {
		if strings.Contains(string(raw), unsupported) {
			t.Fatalf("invented %s: %s", unsupported, raw)
		}
	}
	for _, test := range []struct {
		name   string
		mutate func(*ObservedPlayback)
	}{
		{"NaN-video", func(o *ObservedPlayback) { o.Video.FrameRateHz = math.NaN() }},
		{"infinite-screen", func(o *ObservedPlayback) { o.Screen = &ObservedScreen{RefreshRateHz: math.Inf(1)} }},
		{"capability-not-observation", func(o *ObservedPlayback) { o.Screen = &ObservedScreen{DynamicRange: "supported_hdr10"} }},
		{"quality-label-not-pixels", func(o *ObservedPlayback) { o.Video.Width = -1 }},
		{"unbounded-pixels", func(o *ObservedPlayback) { o.Video.Height = 100000 }},
		{"arbitrary-handling", func(o *ObservedPlayback) { o.Audio.Handling = "probably_passthrough" }},
		{"private-codec-string", func(o *ObservedPlayback) { o.Audio.Codec = "https://private/source?token=secret" }},
		{"unbounded-channels", func(o *ObservedPlayback) { o.Audio.Channels = 65 }},
		{"unbounded-rate", func(o *ObservedPlayback) { o.Audio.SampleRate = 1536001 }},
		{"invented-depth", func(o *ObservedPlayback) { o.Audio.BitDepth = 64 }},
		{"invented-object", func(o *ObservedPlayback) { o.Audio.ObjectAudio = "eac3_implies_atmos" }},
		{"invalid-subtitle-render", func(o *ObservedPlayback) { o.Subtitles = &ObservedSubtitles{Rendering: "probably_burned"} }},
		{"unbounded-bitrate", func(o *ObservedPlayback) { o.DeliveredBitrateKbps = 1000001 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			copy := actual
			video, audio := *actual.Video, *actual.Audio
			copy.Video, copy.Audio = &video, &audio
			test.mutate(&copy)
			if copy.Valid() {
				t.Fatal("unsupported observation accepted")
			}
		})
	}
	var unreported *ObservedPlayback
	if !unreported.Valid() {
		t.Fatal("omitted observations invalidated ordinary timeline")
	}
}
