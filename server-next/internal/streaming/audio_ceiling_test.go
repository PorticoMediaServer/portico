package streaming

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
)

func TestAudioCeilingClosesAudioPreservingRoutes(t *testing.T) {
	for _, container := range []string{"mp4", "mkv"} {
		t.Run(container, func(t *testing.T) {
			source := videoSource(
				container,
				"h264",
				mediafacts.StreamDetail{Width: 1920, Height: 1080, BitDepth: 8, DynamicRange: mediafacts.RangeSDR},
				SourceAudio{Codec: "aac", Channels: 2, StreamDetail: mediafacts.StreamDetail{BitRate: 512_000, SampleRate: 48000}},
			)
			plan := planDocument(t, source, documentOf(appleTVProfile(), ClientProfile{}), func(in *DeliveryInput) { in.Target.MaxAudioBitrateBPS = 128_000 })
			if plan.AudioAction != "convert" || plan.AudioBitrateBPS > 128_000 {
				t.Fatalf("audio ceiling bypassed: route=%s audio=%s bitrate=%d", plan.Strategy, plan.AudioAction, plan.AudioBitrateBPS)
			}
		})
	}
}
