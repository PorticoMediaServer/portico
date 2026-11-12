package streaming

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
	"github.com/PorticoMediaServer/portico/server-next/internal/qualityladder"
)

// A quality is a 16:9 box, not a height alone: a film wider than 16:9 is fitted to the
// box's width. A 3840x1600 film at 1080p was made 2592x1080, a third wider than the 1920 a
// 1080p device or a 1080p data budget is made for.
func TestAQualityIsABoxNotAHeight(t *testing.T) {
	browser := planTestDevice(t, "chromium")
	film := func(width, height int) FileFacts {
		return FileFacts{
			ID: "m1", Container: "mkv", VideoCodec: "hevc", AudioCodec: "aac", Duration: 600, Size: 3 << 30, Width: width, Height: height,
			DetailVersion: mediafacts.StreamDetailVersion, StreamsCurrent: true,
			Streams: []mediafacts.Stream{
				{Index: 0, Type: "video", Codec: "hevc", Detail: mediafacts.StreamDetail{
					Profile: "Main 10", Level: 153, Width: width, Height: height, FrameRate: 24, BitDepth: 10, DynamicRange: "sdr", BitRate: 40_000_000}},
				{Index: 1, Type: "audio", Codec: "aac", Channels: 2, Detail: mediafacts.StreamDetail{SampleRate: 48000, BitRate: 192000}},
			},
		}
	}
	for _, c := range []struct {
		name          string
		width, height int
		quality       string
		wantW, wantH  int
	}{
		{"a scope film at 1080p", 3840, 1600, "1080p-low", 1920, 800},
		// 533 lines made even: 532, and the width that keeps the shape.
		{"a scope film at 720p", 3840, 1600, "720p-high", 1276, 532},
		{"a 16:9 film at 1080p", 3840, 2160, "1080p-low", 1920, 1080},
		{"a 4:3 film at 720p", 1440, 1080, "720p-high", 960, 720},
		{"a scope film at 4K", 3840, 1600, "2160p", 3840, 1600},
	} {
		preset, _ := qualityladder.Find(c.quality)
		choice := Choice{Limit: true, QualityID: c.quality, MaxVideoHeight: preset.Height, MaxVideoBitrateBPS: preset.VideoBitrateBPS,
			MaxAudioBitrateBPS: preset.MaxAudioBitrateBPS}
		plan, err := Plan(PlanRequest{File: film(c.width, c.height), Device: browser, Choice: choice, Settings: DefaultDeliveryConfiguration(), TranscodingEnabled: true})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if c.quality == "2160p" {
			if plan.VideoAction == "convert" {
				if w, h := convertedDimensions(plan); w != c.wantW || h != c.wantH {
					t.Errorf("%s: made %dx%d, want %dx%d", c.name, w, h, c.wantW, c.wantH)
				}
			}
			continue
		}
		if plan.VideoAction != "convert" {
			t.Fatalf("%s: video %q, want converted", c.name, plan.VideoAction)
		}
		if w, h := convertedDimensions(plan); w != c.wantW || h != c.wantH {
			t.Errorf("%s: made %dx%d, want %dx%d", c.name, w, h, c.wantW, c.wantH)
		}
		if plan.TargetHeight != c.wantH {
			t.Errorf("%s: the plan stores height %d, want the height made, %d", c.name, plan.TargetHeight, c.wantH)
		}
	}
}
