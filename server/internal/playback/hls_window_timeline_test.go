package playback

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

type timelineWindow struct {
	probe func(context.Context, []string) ([]byte, error)
}

func (w timelineWindow) Probe(ctx context.Context, args []string) ([]byte, error) {
	return w.probe(ctx, args)
}
func (timelineWindow) Run(context.Context, string, func(string) (subtitles.WindowCommand, error)) error {
	return errors.New("unexpected producer run")
}
func (timelineWindow) Close() error { return nil }

func windowTimelineFixture(t *testing.T) (*HLS, string, string) {
	t.Helper()
	db, h, id := conversionOutputFixture(t)
	h.timelines = map[string]*copyTimeline{}
	var asset string
	if err := db.QueryRow(`SELECT asset_id FROM playback_sessions WHERE id=?`, id).Scan(&asset); err != nil {
		t.Fatal(err)
	}
	mustAudioExec(t, db, `INSERT INTO subtitle_remote_sessions VALUES(?,'remote-version')`, id)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	plan := DeliveryPlan{SourceSize: 1, SourceModifiedNS: 1, Profile: DeliveryProfileLegacy, SourceID: asset, SourceContainer: "strm", Duration: 19, Mode: "hls", Strategy: DeliveryCopyRemux, Reason: "copy", VideoAction: "copy", AudioAction: "copy", OutputContainer: "mpegts_hls", OutputVideoCodec: "h264", OutputAudioCodec: "aac", AudioChannels: 2}
	if err := persistDeliveryPlan(tx, id, &plan); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// The descriptor identity deliberately matches an unrelated cached timeline.
	mustAudioExec(t, db, `INSERT INTO playback_copy_timelines VALUES(?,1,1,'{"boundaries":[0,19]}',0)`, asset)
	// A write attempt to that cache is an error, even if production ignores it.
	mustAudioExec(t, db, `CREATE TRIGGER refuse_window_cache_update BEFORE UPDATE ON playback_copy_timelines BEGIN SELECT RAISE(ABORT,'window used asset cache'); END`)
	return h, id, asset
}

func TestWindowCopyTimelineUsesSessionCacheAndReloads(t *testing.T) {
	h, id, asset := windowTimelineFixture(t)
	calls := 0
	window := timelineWindow{probe: func(_ context.Context, args []string) ([]byte, error) {
		calls++
		if reflect.DeepEqual(args, []string{"-show_entries", "format=start_time", "-of", "csv=p=0"}) {
			return []byte("1.5\n"), nil
		}
		want := []string{"-select_streams", "v:0", "-show_entries", "packet=pts_time,flags", "-of", "csv=p=0", "-read_intervals", "7.500000%+#1,13.500000%+#1,19.500000%+#1"}
		if !reflect.DeepEqual(args, want) {
			t.Fatalf("unexpected probe query: %q", args)
		}
		return []byte("5.500000,K_\n13.500000,K_\n17.500000,K_\n"), nil
	}}
	ctx := context.Background()
	want := &copyTimeline{Boundaries: []float64{0, 4, 12, 16, 19}}
	timeline, err := h.copyTimelineFor(ctx, id, asset, &assets.PlaybackInput{}, window, 19)
	if err != nil || !reflect.DeepEqual(timeline, want) || calls != 2 {
		t.Fatalf("timeline=%+v calls=%d err=%v", timeline, calls, err)
	}
	var raw string
	if err := h.db.QueryRow(`SELECT boundaries FROM playback_copy_timelines WHERE asset_id=?`, asset).Scan(&raw); err != nil || raw != `{"boundaries":[0,19]}` {
		t.Fatalf("asset cache changed: %s %v", raw, err)
	}
	// All maps are empty, as after a server restart. Neither readback nor a fresh
	// producer uses the descriptor's cached timeline or another physical probe.
	restarted := &HLS{db: h.db, root: h.root, timelines: map[string]*copyTimeline{}}
	if got := restarted.sessionTimeline(ctx, id); !reflect.DeepEqual(got, want) {
		t.Fatalf("restart timeline: %+v", got)
	}
	if got, err := restarted.copyTimelineFor(ctx, id, asset, &assets.PlaybackInput{}, window, 19); err != nil || !reflect.DeepEqual(got, want) || calls != 2 {
		t.Fatalf("reused timeline: %+v %v, probes=%d", got, err, calls)
	}
	// A conversion plan must not reuse the saved copy cuts.
	mustAudioExec(t, h.db, `UPDATE playback_delivery_plans SET video_action='convert' WHERE session_id=?`, id)
	if got := restarted.sessionTimeline(ctx, id); got != nil {
		t.Fatalf("conversion reused copy timeline: %+v", got)
	}
	mustAudioExec(t, h.db, `UPDATE playback_delivery_plans SET video_action='copy' WHERE session_id=?`, id)
	// Even this session's disk copy is rejected if its pinned evidence differs.
	mustAudioExec(t, h.db, `UPDATE subtitle_remote_sessions SET evidence='different-version' WHERE session_id=?`, id)
	if got := restarted.sessionTimeline(ctx, id); got != nil {
		t.Fatalf("stale session cache: %+v", got)
	}
	// A missing session artifact also cannot fall through to the asset cache.
	if err := os.Remove(filepath.Join(h.root, id, "copy-timeline.json")); err != nil {
		t.Fatal(err)
	}
	if got := restarted.sessionTimeline(ctx, id); got != nil {
		t.Fatalf("missing session cache used asset cache: %+v", got)
	}
	if _, err := restarted.copyTimelineFor(ctx, id, asset, &assets.PlaybackInput{}, window, 19); err != nil || calls != 4 {
		t.Fatalf("missing timeline not re-probed: probes=%d err=%v", calls, err)
	}
}

func TestWindowCopyTimelineRefusesPacketFallback(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, failure := range []string{"command", "row count", "not keyframe", "invalid stamp"} {
		t.Run(failure, func(t *testing.T) {
			h, id, asset := windowTimelineFixture(t)
			calls := 0
			window := timelineWindow{probe: func(_ context.Context, args []string) ([]byte, error) {
				calls++
				query := strings.Join(args, " ")
				if strings.Contains(query, "format=start_time") {
					return []byte("0\n"), nil
				}
				if !strings.Contains(query, "-read_intervals") {
					t.Fatal("whole-file packet fallback was called")
				}
				switch failure {
				case "command":
					return nil, errors.New("index not available")
				case "row count":
					return []byte("4,K_\n"), nil
				case "not keyframe":
					return []byte("4,__\n12,K_\n16,K_\n"), nil
				default:
					return []byte("N/A,K_\n12,K_\n16,K_\n"), nil
				}
			}}
			if got, err := h.copyTimelineFor(context.Background(), id, asset, &assets.PlaybackInput{}, window, 19); got != nil || !errors.Is(err, errCopyTimeline) || calls != 2 {
				t.Fatalf("timeline=%+v probes=%d err=%v", got, calls, err)
			}
			if _, err := os.Stat(filepath.Join(h.root, id, "copy-timeline.json")); !os.IsNotExist(err) {
				t.Fatal("failed probe persisted a timeline", err)
			}
		})
	}
}
