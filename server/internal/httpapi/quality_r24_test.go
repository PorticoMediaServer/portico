package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"os/exec"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
	"github.com/PorticoMediaServer/portico/server/internal/testtier"
	"testing"
	"time"
)

func TestQualityR24NineOptionsAndAboveSourceNoConversion(t *testing.T) {
	f := replanFixtureV1(t, 1)
	filmStreams(t, f, 0, 6_000_000)
	var options playbackv1.Options
	f.call("GET", "/v1/items/"+f.items[0]+"/playback-options", nil, nil, 200, &options)
	want := []string{"original", "automatic", "2160p", "1080p-high", "1080p-low", "720p-high", "720p-low", "480p", "240p"}
	if len(options.Qualities) != 9 {
		t.Fatal(options.Qualities)
	}
	for i, q := range options.Qualities {
		id := q.ID
		if id == "" {
			id = q.Mode
		}
		if id != want[i] {
			t.Fatalf("choice %d %+v", i, q)
		}
	}
	// These ceilings exceed both source dimensions and its actual rate. Turning
	// conversion off proves selecting one cannot silently start a converter.
	if _, err := f.db.Exec(`UPDATE playback_owner_policy SET transcoding_enabled=0`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"2160p", "1080p-high", "1080p-low"} {
		var s playbackv1.SessionView
		f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "quality-r24-" + id}, startBody(f.items[0], map[string]any{"quality": map[string]any{"mode": "fixed", "id": id}}), 201, &s)
		if s.Presentation.Mode != "direct" {
			t.Fatalf("above-source converted %+v", s.Presentation)
		}
		f.call("DELETE", "/v1/playback/sessions/"+s.ID, nil, nil, 204, nil)
	}
	// A genuinely smaller step requires conversion; it cannot be served as
	// an over-budget original merely because all nine rows remain visible.
	w := f.raw("POST", "/v1/playback/sessions", f.owner.AccessToken, map[string]string{"Idempotency-Key": "quality-r24-small-refused"}, startBody(f.items[0], map[string]any{"quality": map[string]any{"mode": "fixed", "id": "720p-low"}}))
	if w.Code < 400 || w.Code >= 500 {
		t.Fatalf("disabled conversion bypassed %d %s", w.Code, w.Body)
	}
	for _, id := range []string{"1080p", "720p", "1440p", "360p"} {
		f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "quality-r24-gone-" + id}, startBody(f.items[0], map[string]any{"quality": map[string]any{"mode": "fixed", "id": id}}), 400, nil)
	}
}

// This tier generates a genuine playable file, asks real handlers to select
// each above-source step with conversion disabled, then reads identical bytes.
func TestQualityR24LinuxOriginalBytesForAboveSource(t *testing.T) {
	testtier.Media(t, "real quality no-conversion media response")
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal(err)
	}
	f := replanFixtureV1(t, 1)
	var path string
	if err = f.db.QueryRow(`SELECT path FROM catalog_assets WHERE token=?`, f.records[0].Token).Scan(&path); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffmpeg, "-v", "error", "-nostdin", "-y", "-f", "lavfi", "-i", "testsrc2=s=320x180:r=12:d=1", "-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-c:v", "libx264", "-preset", "ultrafast", "-b:v", "80k", "-c:a", "aac", "-b:a", "32k", "-movflags", "+faststart", path)
	if raw, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("fixture %v %s", e, raw)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	f.catalogTest.Write(func(ctx context.Context, tx *sql.Tx) error {
		_, _, e := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(), Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 320, Height: 180, Duration: 1})
		return e
	})
	f.catalogTest.Drain()
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	err = assets.PersistStreams(tx, f.records[0].Token, info.Size(), info.ModTime().UnixNano(), []assets.Stream{{Index: 0, Type: "video", Codec: "h264", Detail: assets.StreamDetail{Profile: "Constrained Baseline", Level: 12, Width: 320, Height: 180, BitDepth: 8, FrameRate: 12, DynamicRange: assets.RangeSDR}}, {Index: 1, Type: "audio", Codec: "aac", Channels: 1, Detail: assets.StreamDetail{SampleRate: 44100}, Default: true}})
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`UPDATE playback_owner_policy SET transcoding_enabled=0`); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"2160p", "1080p-high", "1080p-low", "720p-high", "720p-low", "480p", "240p"} {
		var s playbackv1.SessionView
		f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "quality-linux-" + id}, startBody(f.items[0], map[string]any{"quality": map[string]any{"mode": "fixed", "id": id}}), 201, &s)
		if s.Presentation.Mode != "direct" {
			t.Fatalf("quality converted %+v", s.Presentation)
		}
		response := f.call("GET", s.Presentation.URL, nil, nil, 200, nil)
		if !bytes.Equal(response.Body.Bytes(), original) {
			t.Fatalf("quality %s changed original bytes", id)
		}
		f.call("DELETE", "/v1/playback/sessions/"+s.ID, nil, nil, 204, nil)
	}
}
