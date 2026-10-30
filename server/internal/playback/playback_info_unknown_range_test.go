package playback

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestPlaybackComparisonLegacySummaryDoesNotSupplyObservedRange(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, _, asset := catalogFixture(t, db, "lib", "/test", compactcatalog.Movie, "film", "Film", compactcatalog.Asset{Path: "/test/film.mp4", Size: 100, ModifiedNS: 1, Container: "mp4", VideoCodec: "h264", Duration: 10})
	source, err := loadDeliverySource(context.Background(), db, asset)
	if err != nil {
		t.Fatal(err)
	}
	if source.Video == nil || source.Video.Codec != "h264" || source.DetailKnown || source.Video.DynamicRange != "" {
		t.Fatalf("summary invented observed picture range: %+v", source.Video)
	}
	plan := decide(t, &source, appleTVProfile(), nil)
	// The established planning fallback is unchanged. Its assumption must not
	// be relabelled as measured source or copy-delivery metadata.
	prior := source
	priorVideo := *source.Video
	priorVideo.DynamicRange = assets.RangeSDR
	prior.Video = &priorVideo
	priorPlan := decide(t, &prior, appleTVProfile(), nil)
	if plan.Strategy != priorPlan.Strategy || plan.VideoAction != priorPlan.VideoAction || plan.AudioAction != priorPlan.AudioAction {
		t.Fatal("unknown-range reporting changed the existing fallback route", plan.Strategy, priorPlan.Strategy)
	}
	if plan.VideoAction == "convert" {
		t.Fatal("fixture did not exercise the unchanged copy/original route")
	}
	info := PlaybackInfoForPlan(plan, nil)
	if info == nil || info.Source.Video.DynamicRange != "" || info.Delivered.Video.DynamicRange != "" {
		t.Fatal("planning assumption escaped as observed range", info)
	}
	if source.Video.DynamicRange != "" {
		t.Fatal("planning mutated source facts")
	}
}

func TestPlaybackComparisonCopiedRangeRequiresSourceFact(t *testing.T) {
	for _, sourceRange := range []string{"", assets.RangeSDR, assets.RangeHDR10} {
		for _, target := range []*decoder.ConversionTarget{nil, {VideoCodec: "hevc", BitDepth: 10, DynamicRange: assets.RangeSDR}} {
			plan := DeliveryPlan{Strategy: DeliveryCopyRemux, VideoAction: "copy", ConversionTarget: target, Trace: &DecisionTrace{Video: &TraceVideo{Codec: "hevc", DynamicRange: sourceRange, OutputRange: assets.RangeSDR}}}
			info := PlaybackInfoForPlan(plan, nil)
			want := assets.RangeSDR
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
