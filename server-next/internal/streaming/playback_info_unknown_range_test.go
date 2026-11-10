package streaming

import (
	"encoding/json"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
)

func TestPlaybackComparisonCopiedRangeRequiresSourceFact(t *testing.T) {
	for _, sourceRange := range []string{"", mediafacts.RangeSDR, mediafacts.RangeHDR10} {
		for _, target := range []*decoder.ConversionTarget{nil, {VideoCodec: "hevc", BitDepth: 10, DynamicRange: mediafacts.RangeSDR}} {
			plan := DeliveryPlan{
				Strategy:         DeliveryCopyRemux,
				VideoAction:      "copy",
				ConversionTarget: target,
				Trace:            &DecisionTrace{Video: &TraceVideo{Codec: "hevc", DynamicRange: sourceRange, OutputRange: mediafacts.RangeSDR}},
			}
			info := PlaybackInfoForPlan(plan, nil)
			want := mediafacts.RangeSDR
			if sourceRange == "" {
				want = ""
			}
			if info.Source.Video.DynamicRange != sourceRange || info.Delivered.Video.DynamicRange != want {
				t.Fatal("copy range disagrees with the committed source fact", info)
			}
			if sourceRange == "" {
				raw, err := json.Marshal(info)
				if err != nil {
					t.Fatal(err)
				}
				var wire struct {
					Source, Delivered struct{ Video map[string]any }
				}
				if err = json.Unmarshal(raw, &wire); err != nil {
					t.Fatal(err)
				}
				for _, facts := range []map[string]any{wire.Source.Video, wire.Delivered.Video} {
					if _, exists := facts["dynamicRange"]; exists {
						t.Fatal("unknown range was not omitted", string(raw))
					}
				}
			}
		}
	}
}
