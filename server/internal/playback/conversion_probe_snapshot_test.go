package playback

import (
	"context"
	"encoding/json"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/decoder"
)

func TestConversionProbeDecodeCellsSurviveStoreLoadAndGraph(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("VAAPI graph requires Linux")
	}
	for _, tc := range []struct {
		name     string
		cells    map[string]bool
		hardware bool
	}{
		{"measured H2648", map[string]bool{"h264:8": true, "h264:10": false}, true},
		{"measured empty", map[string]bool{}, false},
		{"legacy missing", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, h, id := conversionOutputFixture(t)
			ctx := context.Background()
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			var source string
			if err = tx.QueryRow(`SELECT asset_id FROM playback_sessions WHERE id=?`, id).Scan(&source); err != nil {
				t.Fatal(err)
			}
			plan := DeliveryPlan{Profile: DeliveryProfileLegacy, Mode: "hls", Strategy: DeliveryVideoConversion, Reason: "conversion", SourceID: source, SourceSize: 1, SourceModifiedNS: 1, SourceVideoCodec: "h264", VideoAction: "convert", AudioAction: "none", OutputVideoCodec: "h264", OutputContainer: "mpegts_hls", HardwareBackend: "vaapi", HardwareNative: true,
				Trace: &DecisionTrace{Video: &TraceVideo{Codec: "h264", BitDepth: 8, Width: 1920, Height: 1080, FrameRate: 24, DynamicRange: "sdr"}},
			}
			if err = persistDeliveryPlan(tx, id, &plan); err != nil {
				t.Fatal(err)
			}
			target := decoder.ConversionTarget{VideoCodec: "h264", BitDepth: 8, DynamicRange: "sdr", Width: 1280, Height: 720, FrameRate: 24, RateControl: "quality", Packaging: "mpegts_hls"}
			probe := decoder.HardwareProbe{Backend: decoder.BackendVAAPI, Available: true, Identity: "measured-runtime", Device: "/dev/dri/renderD128", NativeFilters: true, DecodeCodecs: tc.cells, OutputEncoders: map[string]bool{"h264:8": true}, OutputQuality: map[string]bool{"h264:8": true}}
			if err = StoreConversionTarget(ctx, tx, id, target, probe); err != nil {
				t.Fatal(err)
			}
			if tc.cells == nil {
				// Recreate a pre-fix private snapshot, not a current decode-off record.
				if _, err = tx.Exec(`UPDATE playback_conversion_targets SET hardware_json=json_remove(hardware_json,'$.decodeCodecs') WHERE session_id=?`, id); err != nil {
					t.Fatal(err)
				}
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			loaded, err := loadDeliveryPlan(ctx, db, id)
			if err != nil {
				t.Fatal(err)
			}
			if (loaded.ConversionProbe.DecodeCodecs == nil) != (tc.cells == nil) {
				t.Fatal("missing and measured-empty cells were conflated")
			}
			graph, err := loaded.conversionGraph(nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(strings.Join(graph.Input, " "), "-hwaccel vaapi"); got != tc.hardware {
				t.Fatalf("stored probe lost source decode authority: %+v", graph)
			}
			if !strings.Contains(strings.Join(graph.Video, " "), "h264_vaapi") {
				t.Fatal("hardware encoder changed", graph)
			}
			if tc.cells == nil {
				// Re-measure a legacy target on the same runtime/device. The
				// controlled runner never executes FFmpeg; uncreated source
				// probe files yield measured false cells, never hardware claims.
				priorDetector := legacyTargetHardware
				detector := &decoder.HardwareDetector{Run: func(context.Context, *exec.Cmd) error { return nil }}
				legacyTargetHardware = detector
				defer func() { legacyTargetHardware = priorDetector }()
				h.binary = probeExecutable(t)
				fresh := detector.Probe(ctx, h.binary, decoder.BackendVAAPI, probe.Device)
				if !fresh.Available || fresh.DecodeCodecs == nil {
					t.Fatal("controlled re-probe unavailable", fresh)
				}
				loaded.ConversionProbe.Identity = fresh.Identity
				raw, err := json.Marshal(loaded.ConversionProbe)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = db.Exec(`UPDATE playback_conversion_targets SET hardware_json=? WHERE session_id=?`, string(raw), id); err != nil {
					t.Fatal(err)
				}
				if err = h.refreshLegacyConversionProbe(ctx, id, 3, loaded); err != nil {
					t.Fatal(err)
				}
				healed, err := loadDeliveryPlan(ctx, db, id)
				if err != nil || healed.ConversionProbe.DecodeCodecs == nil {
					t.Fatal("legacy snapshot did not self-heal", err)
				}
				// Restore the legacy shape and prove explicit owner-off wins.
				if _, err = db.Exec(`UPDATE playback_conversion_targets SET hardware_json=json_remove(hardware_json,'$.decodeCodecs') WHERE session_id=?`, id); err != nil {
					t.Fatal(err)
				}
				loaded, err = loadDeliveryPlan(ctx, db, id)
				if err != nil {
					t.Fatal(err)
				}
				cfg := DefaultDeliveryConfiguration()
				cfg.SoftwareDecode = true
				h.ConfigureSettings(fixedSettings{cfg})
				if err = h.refreshLegacyConversionProbe(ctx, id, 3, loaded); err != nil {
					t.Fatal(err)
				}
				healed, err = loadDeliveryPlan(ctx, db, id)
				if err != nil || healed.ConversionProbe.DecodeCodecs == nil || len(healed.ConversionProbe.DecodeCodecs) != 0 {
					t.Fatal("legacy owner-off snapshot did not heal conservatively", healed, err)
				}
			}
			wire, err := json.Marshal(probe)
			if err != nil || strings.Contains(string(wire), "decodeCodecs") {
				t.Fatal("private cells leaked into public HardwareProbe JSON", string(wire), err)
			}
		})
	}
}

func TestConversionProbeSnapshotRejectsUnmeasuredCellNames(t *testing.T) {
	for _, key := range []string{"vc1:8", "h264:0", "h264:12", "arbitrary:8"} {
		if _, err := conversionProbeJSON(decoder.HardwareProbe{DecodeCodecs: map[string]bool{key: true}}); err == nil {
			t.Fatal("accepted unmeasured cell", key)
		}
	}
}
