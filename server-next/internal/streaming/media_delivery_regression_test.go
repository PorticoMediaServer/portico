package streaming

import (
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
)

func TestLongHLSTimelinesCoverWholeTitleWithinExplicitBound(t *testing.T) {
	for _, duration := range []float64{12*3600 - 1, 12 * 3600, 13*3600 + 1, 30 * 3600, 7 * 24 * 3600} {
		manifest := string(hlsTimelineManifest(duration))
		total := 0.0
		for _, line := range strings.Split(manifest, "\n") {
			if strings.HasPrefix(line, "#EXTINF:") {
				value, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimPrefix(line, "#EXTINF:"), ","), 64)
				if err != nil {
					t.Fatal(err)
				}
				total += value
			}
		}
		if math.Abs(total-duration) > 1e-6 {
			t.Fatalf("duration %.0f truncated to %.0f", duration, total)
		}
		last := hlsSegmentCount(duration) - 1
		if _, ok := hlsSegmentIndex(hlsSegmentFile(last)); !ok || !strings.Contains(manifest, hlsSegmentFile(last)) {
			t.Fatal("last index unreachable")
		}
		if len(manifest) > 6<<20 {
			t.Fatal("manifest exceeded safety ceiling")
		}
	}
	if err := writeTimelineManifest(t.TempDir(), 7*24*3600+1); err == nil {
		t.Fatal("over-limit title silently published")
	}
}

func TestDeliveryPlanPinsClientMaximumWidth(t *testing.T) {
	client := chromeProfile()
	client.Video[0].MaxWidth = 1920
	client.Video[0].MaxHeight = 1080
	p := decide(t, videoSource("mp4", "h264", mediafacts.StreamDetail{Width: 2560, Height: 1080, BitDepth: 8, DynamicRange: "sdr"}, SourceAudio{Codec: "aac", Channels: 2}), client, nil)
	if p.VideoAction != "convert" || p.TargetWidth != 1920 {
		t.Fatalf("missing width limit: %+v", p)
	}
}
