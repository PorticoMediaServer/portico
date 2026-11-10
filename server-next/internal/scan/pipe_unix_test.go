//go:build unix

package scan

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// The scanner reads small files beside the media (an NFO, a subtitle). A named
// pipe with such a name is refused at once and does not hang the scan.
func TestTheScannerRefusesANamedPipeWithoutWaiting(t *testing.T) {
	folder := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(folder, "film.nfo"), 0600); err != nil {
		t.Skip(err)
	}
	done := make(chan error, 1)
	go func() {
		file, err := (Local{Root: folder}).Open(context.Background(), "film.nfo")
		if file != nil {
			file.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a named pipe opened")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("opening a named pipe waited")
	}
}
