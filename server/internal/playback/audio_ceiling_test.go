package playback

import (
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"testing"
)

func TestAudioCeilingClosesAudioPreservingRoutes(t *testing.T) {
	for _, container := range []string{"mp4", "mkv"} {
		t.Run(container, func(t *testing.T) {
			source := videoSource(container, "h264", assets.StreamDetail{Width: 1920, Height: 1080, BitDepth: 8, DynamicRange: assets.RangeSDR}, SourceAudio{Codec: "aac", Channels: 2, StreamDetail: assets.StreamDetail{BitRate: 512_000, SampleRate: 48000}})
			plan := planDocument(t, source, documentOf(appleTVProfile(), ClientProfile{}), func(in *DeliveryInput) { in.Target.MaxAudioBitrateBPS = 128_000 })
			if plan.AudioAction != "convert" || plan.AudioBitrateBPS > 128_000 {
				t.Fatalf("audio ceiling bypassed: route=%s audio=%s bitrate=%d", plan.Strategy, plan.AudioAction, plan.AudioBitrateBPS)
			}
		})
	}
}
