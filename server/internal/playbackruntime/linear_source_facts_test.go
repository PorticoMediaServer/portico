package playbackruntime

import (
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
)

func TestLinearVideoSourceKeepsMeasuredDepthAndScanFacts(t *testing.T) {
	for _, tc := range []struct {
		name, raw, pixel, order string
		depth                   int
		scan                    *bool
	}{
		{"twelve-bit", "", "yuv420p12le", "progressive", 12, boolPointer(false)},
		{"sixteen-bit", "", "yuv444p16le", "tt", 16, boolPointer(true)},
		{"semi-planar", "", "p012le", "bb", 12, boolPointer(true)},
		{"raw-depth", "12", "yuv420p", "tb", 12, boolPointer(true)},
		{"ten-bit", "", "yuv420p10le", "bt", 10, boolPointer(true)},
		{"eight-bit", "", "yuv420p", "progressive", 8, boolPointer(false)},
		{"missing", "", "", "", 0, nil},
		{"unknown-field-order", "", "yuv420p", "unknown", 8, nil},
		{"unrecognized-field-order", "", "yuv420p", "unspecified", 8, nil},
		{"invalid-raw-without-format", "17", "", "", 0, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := linearVideoSource(&linearProbeStream{Codec: "hevc", AudioDepth: tc.raw, PixelFormat: tc.pixel, FieldOrder: tc.order})
			if v.BitDepth != tc.depth {
				t.Fatalf("depth=%d, want actual probe depth %d", v.BitDepth, tc.depth)
			}
			scan := v.ObservedInterlaced()
			if (scan == nil) != (tc.scan == nil) || scan != nil && *scan != *tc.scan {
				t.Fatalf("scan=%v, want %v (analysis %q)", scan, tc.scan, v.InterlaceAnalysis)
			}
			if v.Interlaced != (tc.scan != nil && *tc.scan) {
				t.Fatal("legacy deinterlace input disagrees with normalized fact")
			}
		})
	}
	if linearVideoSource(nil) != nil {
		t.Fatal("invented a video stream for an audio-only probe")
	}
}

func TestLinearPlanRejectsUnsupportedHighDepthInsteadOfCopyingAsEightBit(t *testing.T) {
	client := playback.BaselineClientProfile()
	client.Video = []playback.ClientVideoCodec{{Codec: "hevc", BitDepths: []int{8}, DynamicRanges: []string{assets.RangeSDR}, Evidence: playback.EvidenceBuiltin}}
	for i := range client.Transports {
		if client.Transports[i].Transport == playback.TransportHLSTS {
			client.Transports[i].Video = []string{"hevc"}
		}
	}
	w := playback.LinearWork{ClientProfile: client, TranscodingEnabled: false}
	w.Selection.Quality.Mode = "original"
	for _, pixel := range []string{"yuv420p12le", "p012le", "yuv444p16le"} {
		raw := []byte(fmt.Sprintf(`{"streams":[{"codec_type":"video","codec_name":"hevc","pix_fmt":%q,"width":1920,"height":1080,"field_order":"progressive"}]}`, pixel))
		if plan, err := linearPlan(raw, w, playback.LinearTSOnly); err == nil {
			t.Fatalf("unsupported %s admitted as an original copy: %+v", pixel, plan)
		}
	}
	raw := []byte(`{"streams":[{"codec_type":"video","codec_name":"hevc","pix_fmt":"yuv420p","width":1920,"height":1080,"field_order":"progressive"}]}`)
	if plan, err := linearPlan(raw, w, playback.LinearTSOnly); err != nil || plan.ConvertVideo {
		t.Fatalf("measured compatible eight-bit copy changed: %+v %v", plan, err)
	}
}

func boolPointer(value bool) *bool { return &value }
