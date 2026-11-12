package mediaengine

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
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
	if e.processes.BackgroundTaskPriority() != "normal" {
		t.Fatal("Fast did not reach the running supervisor")
	}
	if _, err := docs.Write(ctx, 0, "general", json.RawMessage(`{"backgroundSpeed":"gentle"}`), -1, time.Now()); err != nil {
		t.Fatal(err)
	}
	if e.processes.BackgroundTaskPriority() != "lower" {
		t.Fatal("Gentle did not restore the background priority")
	}
}
