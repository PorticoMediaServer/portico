package playback

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"strings"
	"testing"
	"time"
)

// The real production HLS converter runs slowly enough to observe while active.
// The wrapper changes only the fixture's input cadence, not the measured value.
func TestActivityRealConverterProducesFreshSpeedAndCleansObservation(t *testing.T) {
	binary := decodertest.QualifiedFFmpeg(t)
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	input := filepath.Join(root, "tone.flac")
	if out, err := exec.CommandContext(ctx, binary, "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=8", "-c:a", "flac", input).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	facts, err := (assets.Probe{}).Inspect(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(input)
	if err != nil {
		t.Fatal(err)
	}
	db, err := persistence.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, item, _ := catalogFixture(t, db, "lib", root, compactcatalog.Track, "song", "Tone", compactcatalog.Asset{Path: input, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(), Container: facts.Container, VideoCodec: facts.VideoCodec, AudioCodec: facts.AudioCodec, Width: facts.Width, Height: facts.Height, Duration: facts.Duration})
	wrapper := filepath.Join(root, "slow-ffmpeg")
	script := "#!/bin/sh\nexec '" + strings.ReplaceAll(binary, "'", "'\\''") + "' -readrate 0.5 \"$@\"\n"
	if err = os.WriteFile(wrapper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	h, err := NewHLS(ctx, db, filepath.Join(root, "hls"), wrapper)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cancel()
		stop, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		if err := h.Shutdown(stop); err != nil {
			t.Error(err)
		}
	}()
	player := New(db)
	player.ConfigureHLS(h)
	p := identity.Principal{Hash: "session", Viewer: identity.Viewer{AccountID: "owner", ProfileID: "profile", Authority: "local", Role: "owner"}}
	insertFixtureAccount(t, db, p.AccountID, p.ProfileID)
	session, err := player.Create(p, item, "automatic", "speed-fixture")
	if err != nil || session.Mode != "hls" {
		t.Fatal(session, err)
	}
	ready := make(chan error, 1)
	go func() { ready <- player.Ready(ctx, session) }()
	deadline := time.Now().Add(12 * time.Second)
	var speed float64
	var observed int64
	found := false
	for time.Now().Before(deadline) {
		err = db.QueryRow(`SELECT speed,observed_ms FROM playback_conversion_observations WHERE session_id=? ORDER BY observed_ms DESC LIMIT 1`, session.ID).Scan(&speed, &observed)
		if err == nil {
			found = true
			break
		}
		select {
		case err := <-ready:
			if err != nil {
				t.Fatalf("producer ended before report: %v", err)
			}
		case <-time.After(25 * time.Millisecond):
		}
	}
	if !found || speed <= 0 || speed >= 0.95 || time.Now().UnixMilli()-observed > 2000 {
		t.Fatalf("actual slow converter report: found=%v speed=%f observed=%d error=%v", found, speed, observed, err)
	}
	t.Logf("actual FFmpeg converter session %s measured media/wall speed %.4fx, observed %s, cadence 1000ms", session.ID, speed, time.UnixMilli(observed).UTC().Format(time.RFC3339Nano))
	cancel()
	stop, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	if err = h.Shutdown(stop); err != nil {
		t.Fatal(err)
	}
	var remaining, hints int
	if err = db.QueryRow(`SELECT count(*) FROM playback_conversion_observations WHERE session_id=?`, session.ID).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatal("stopped producer retained speed", remaining, err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM api_events WHERE audience='admin' AND type='admin.sessions' AND resource_id=? AND data=''`, session.ID).Scan(&hints); err != nil || hints < 2 {
		t.Fatal("missing safe progress/removal existing-feed hints", hints, err)
	}
}
