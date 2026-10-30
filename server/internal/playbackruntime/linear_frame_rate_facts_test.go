package playbackruntime

import (
	"fmt"
	"math"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/playback"
)

func TestLinearVideoSourceKeepsExactProbeFrameRate(t *testing.T) {
	for _, tc := range []struct {
		name, average, real    string
		rate                   float64
		numerator, denominator int
	}{
		{"fractional", "24000/1001", "24/1", 23.976, 24000, 1001},
		{"measured-fallback", "0/0", "30000/1001", 29.97, 30000, 1001},
		{"missing", "", "", 0, 0, 0},
		{"unknown", "0/0", "0/0", 0, 0, 0},
		{"nan", "NaN/1", "", 0, 0, 0},
		{"infinity", "Inf/1", "", 0, 0, 0},
		{"overflow", "1e309/1", "", 0, 0, 0},
		{"zero-denominator", "24/0", "", 0, 0, 0},
		{"negative", "-24/1", "", 0, 0, 0},
		{"outside-probe-bound", "1001/1", "", 0, 0, 0},
		{"malformed-with-measured-fallback", "NaN/1", "25/1", 25, 25, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := linearVideoSource(&linearProbeStream{FrameRate: tc.average, RealFrameRate: tc.real})
			if source.FrameRate != tc.rate || source.FrameRateNumerator != tc.numerator || source.FrameRateDenominator != tc.denominator || math.IsNaN(source.FrameRate) || math.IsInf(source.FrameRate, 0) {
				t.Fatalf("rate=%v rational=%d/%d, want %v %d/%d", source.FrameRate, source.FrameRateNumerator, source.FrameRateDenominator, tc.rate, tc.numerator, tc.denominator)
			}
			if source.FrameRateMode != "" {
				t.Fatal("probe rate invented constant/variable cadence")
			}
		})
	}
}

func TestLinearPlanUsesMeasuredRealRateFallback(t *testing.T) {
	client := playback.BaselineClientProfile()
	client.Video[0].MaxFrameRate = 30
	work := playback.LinearWork{ClientProfile: client, TranscodingEnabled: false}
	work.Selection.Quality.Mode = "original"
	for _, rate := range []int{25, 60} {
		raw := []byte(fmt.Sprintf(`{"streams":[{"codec_type":"video","codec_name":"h264","pix_fmt":"yuv420p","width":1920,"height":1080,"avg_frame_rate":"0/0","r_frame_rate":"%d/1","field_order":"progressive"}]}`, rate))
		plan, err := linearPlan(raw, work, playback.LinearTSOnly)
		if rate == 60 && err == nil {
			t.Fatal("measured 60fps copied to a 30fps-only client", plan)
		}
		if rate == 25 && (err != nil || plan.ConvertVideo) {
			t.Fatal("compatible measured fallback stopped copying", plan, err)
		}
	}
}
