//go:build unix

package libraryfile

import (
	"errors"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A named pipe put where a scanned file was is refused at once. A plain open
// would wait for ever for somebody to write to it.
func TestANamedPipeIsRefusedWithoutWaiting(t *testing.T) {
	library := t.TempDir()
	pipe := filepath.Join(library, "film.mkv")
	if err := syscall.Mkfifo(pipe, 0600); err != nil {
		t.Skip(err)
	}
	done := make(chan error, 1)
	go func() {
		file, err := Open(pipe, []string{library})
		if file != nil {
			file.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrOutside) {
			t.Fatalf("a named pipe opened: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("opening a named pipe waited")
	}
}
