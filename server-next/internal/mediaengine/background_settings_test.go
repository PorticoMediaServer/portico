package mediaengine

import (
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/processpool"
	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
)

// A speed saved while the server is running reaches future background helpers, using the
// existing supervisor setting. No program needs to run to verify this connection.
func TestBackgroundSpeedReachesTheRunningEngine(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	docs, _, err := settings.Open(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	e, err := New(ctx, d, Options{Folder: t.TempDir(), FFmpeg: filepath.Join(t.TempDir(), "no-ffmpeg"), Settings: docs})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Shutdown(context.Background())
	if e.processes.BackgroundTaskPriority() != "lower" {
		t.Fatal("Normal lost the background priority")
	}
	if _, err := docs.Write(ctx, 0, "general", json.RawMessage(`{"backgroundSpeed":"fast"}`), -1, time.Now()); err != nil {
		t.Fatal(err)
	}
	if stats := e.processes.Stats(); stats.BackgroundCapacity != max(1, runtime.NumCPU()) || stats.ScanProbeCapacity != max(1, runtime.NumCPU()) {
		t.Fatalf("Fast did not raise background capacity: %+v", stats)
	}
	for _, key := range []string{"analysis:priority", "scan:priority"} {
		if err := e.processes.RunFunc(ctx, key, func(_ context.Context, lowPriority bool) error {
			if !lowPriority {
				t.Fatal("Fast raised background work to playback's OS priority")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if e.processes.BackgroundTaskPriority() != "lower" {
		t.Fatal("Fast lost the lower process priority")
	}
	if _, err := docs.Write(ctx, 0, "general", json.RawMessage(`{"backgroundSpeed":"gentle"}`), -1, time.Now()); err != nil {
		t.Fatal(err)
	}
	if e.processes.BackgroundTaskPriority() != "lower" || e.processes.Stats().BackgroundCapacity != max(1, processpool.DefaultBackgroundLimit) {
		t.Fatal("Gentle did not restore the lower background capacity and priority")
	}
}
