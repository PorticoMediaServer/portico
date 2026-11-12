package streaming

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
)

// From desk B's review of #3050. A member away from home has a height ceiling put on the
// target after it is made (narrowMemberTarget); the ceiling is a box as well, so a 3840x1600
// film under a 720p ceiling fits 1280x720, not 1728x720.
func TestAMemberCeilingIsABoxToo(t *testing.T) {
	browser := planTestDevice(t, "chromium")
	film := FileFacts{
		ID: "m1", Container: "mkv", VideoCodec: "hevc", AudioCodec: "aac", Duration: 600, Size: 3 << 30, Width: 3840, Height: 1600,
		DetailVersion: mediafacts.StreamDetailVersion, StreamsCurrent: true,
		Streams: []mediafacts.Stream{
			{Index: 0, Type: "video", Codec: "hevc", Detail: mediafacts.StreamDetail{
				Profile: "Main 10", Level: 153, Width: 3840, Height: 1600, FrameRate: 24, BitDepth: 10, DynamicRange: "sdr", BitRate: 40_000_000}},
			{Index: 1, Type: "audio", Codec: "aac", Channels: 2, Detail: mediafacts.StreamDetail{SampleRate: 48000, BitRate: 192000}},
		},
	}
	for _, c := range []struct {
		name   string
		choice Choice
	}{
		{"Original under a 720p ceiling", Choice{}},
		{"1080p under a 720p ceiling", Choice{Limit: true, QualityID: "1080p-low", MaxVideoHeight: 1080, MaxVideoBitrateBPS: 8_000_000}},
	} {
		plan, err := Plan(PlanRequest{File: film, Device: browser, Choice: c.choice, Settings: DefaultDeliveryConfiguration(), TranscodingEnabled: true,
			MemberCeiling: ReplanCeilings{Remote: true, MaxVideoHeight: 720, MaxVideoBitrateBPS: 4_000_000}})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if plan.VideoAction != "convert" {
			t.Fatalf("%s: video %q, want converted", c.name, plan.VideoAction)
		}
		if w, h := convertedDimensions(plan); w > 1280 || h > 720 {
			t.Errorf("%s: made %dx%d, which does not fit within 1280x720", c.name, w, h)
		}
	}
}
