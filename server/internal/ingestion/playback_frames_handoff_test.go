package ingestion

import (
	"context"
	"io"
	"os"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
)

type frameHandoffInput struct{}

func (*frameHandoffInput) Evidence() string               { return "retained-handoff" }
func (*frameHandoffInput) Size() int64                    { return 5 }
func (*frameHandoffInput) Validate(context.Context) error { return nil }
func (*frameHandoffInput) Close() error                   { return nil }
func (*frameHandoffInput) ReadExtent(context.Context, int64, int64) ([]byte, error) {
	return nil, io.EOF
}

func TestPlaybackFramesPolicyUpgradeDoesNotRepeatBasicProbe(t *testing.T) {
	f := newLoopFixture(t, "movie", "Film (2026).mp4")
	f.finish(f.queue())
	var asset string
	var id, size, modified int64
	if err := f.db.QueryRow(`SELECT a.token,a.id,a.size,a.modified_ns FROM inventory_objects o JOIN catalog_assets a ON a.token=o.asset_id WHERE o.source_id=?`, f.source).Scan(&asset, &id, &size, &modified); err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = compactcatalog.SetAssetTx(context.Background(), tx, id, map[string]any{"container": "mp4", "video_codec": "h264", "duration": float64(1), "width": int64(160), "height": int64(90)}); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = assets.PersistStreams(tx, asset, size, modified, []assets.Stream{{Index: 0, Type: "video", Codec: "h264"}}); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = catalog.ApplyScanPolicyTx(context.Background(), tx, f.library, catalog.ScanPolicy{Tier: "custom", Operations: []string{"probe", catalog.PlaybackFramesOperation}}); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	drainDerivedCatalogue(t, f.db)
	f.markObjectsSettled([]string{"probe", assets.PlaybackAnalysisRevision})
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	probes, opens := 0, 0
	service, err := mediaanalysis.New(mediaanalysis.Options{DB: f.db, Directory: t.TempDir(), FFprobe: exe,
		Open: func(context.Context, string, string) (mediaanalysis.Input, error) {
			opens++
			return &frameHandoffInput{}, nil
		},
		Decoder: func(mediaanalysis.Input) mediaanalysis.Decode {
			return func(context.Context, string, decoder.AnalysisSpec, func(io.Reader) error) error {
				t.Fatal("frame-only request ran FFmpeg")
				return nil
			}
		},
		Probe: func(mediaanalysis.Input) mediaanalysis.Probe {
			return func(_ context.Context, _ string, spec decoder.AnalysisProbeSpec) ([]byte, error) {
				probes++
				if spec.Kind != decoder.ProbeFrameCadence {
					t.Fatal("H264 frame-only request ran another recipe", spec.Kind)
				}
				return []byte(`{"frames":[{"best_effort_timestamp_time":"0.0","interlaced_frame":1},{"best_effort_timestamp_time":"0.04","interlaced_frame":1}]}`), nil
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Close() })
	f.s.DeepAnalysis = service
	f.s.probe.Binary = "/this-basic-probe-must-never-run"
	job := f.finish(f.queue())
	if job.Status != "complete" || opens != 1 || probes != 1 {
		t.Fatal("policy upgrade repeated Basic or failed deferred handoff", job.Status, opens, probes)
	}
	var marker string
	if err = f.db.QueryRow(`SELECT playback_frames_revision FROM asset_stream_breadth WHERE asset_id=? AND stream_index=0`, asset).Scan(&marker); err != nil || marker != assets.PlaybackFramesRevision {
		t.Fatal("frame recipe was not acknowledged", marker, err)
	}
}
