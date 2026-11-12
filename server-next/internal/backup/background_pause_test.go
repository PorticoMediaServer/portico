package backup

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// A manual backup still works during the pause; scheduled work waits and remains cancellable.
func TestTheBackgroundPauseHoldsScheduledBackupsOnly(t *testing.T) {
	s := newService(t)
	s.Settings.PauseBackground(s.Now(), true)
	if err := s.make(t.Context(), "portico-20261112-010000", false); err != nil {
		t.Fatalf("the pause held a manual backup: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if err := s.make(ctx, "portico-20261112-020000", true); err == nil || ctx.Err() == nil {
		t.Fatalf("a scheduled backup did not wait for the pause: %v", err)
	}
	archiveCtx, archiveCancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer archiveCancel()
	files := []archived{{name: "main.db", path: filepath.Join(s.DataDir, "main.db")}}
	if err := s.writeArchive(archiveCtx, filepath.Join(t.TempDir(), "paused.tar.gz"), files, true); err == nil || archiveCtx.Err() == nil {
		t.Fatalf("scheduled archive writing did not wait for the pause: %v", err)
	}
	s.Settings.PauseBackground(s.Now(), false)
	if err := s.make(t.Context(), "portico-20261112-030000", true); err != nil {
		t.Fatalf("scheduled backup stayed paused after Resume: %v", err)
	}
}
