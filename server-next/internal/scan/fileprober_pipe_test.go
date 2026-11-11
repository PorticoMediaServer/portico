//go:build unix

package scan_test

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

// A named pipe put where a film was is refused at once as not a media file: reading it does
// not wait for somebody to write to it.
func TestTheProberRefusesANamedPipeAtOnce(t *testing.T) {
	pipe := filepath.Join(t.TempDir(), "film.mkv")
	if err := syscall.Mkfifo(pipe, 0o644); err != nil {
		t.Skip("no named pipes here:", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := scan.FileProber{}.Probe(ctx, pipe)
		done <- err
	}()
	select {
	case err := <-done:
		if err != scan.ErrNotMedia {
			t.Fatalf("a named pipe: %v; want not a media file", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reading a named pipe did not return")
	}
}
