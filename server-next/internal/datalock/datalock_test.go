package datalock

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// A second server on a data folder is refused, in words that name the folder, and may start
// once the first has stopped. A server on another folder is nobody's business.
func TestASecondServerOnTheFolderIsRefusedUntilTheFirstStops(t *testing.T) {
	folder := t.TempDir()
	release, err := Hold(folder, quiet)
	if err != nil {
		t.Fatalf("the first server: %v", err)
	}
	_, err = Hold(folder, quiet)
	if err == nil {
		t.Fatal("a second server started on the same data folder")
	}
	for _, want := range []string{"another Portico server", folder, "Nothing in the folder was changed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	other, err := Hold(t.TempDir(), quiet)
	if err != nil {
		t.Fatalf("a server on another folder: %v", err)
	}
	other()
	release()
	again, err := Hold(folder, quiet)
	if err != nil {
		t.Fatalf("after the first server stopped: %v", err)
	}
	again()
}

// The lock is between processes, and belongs to the process: when a server is killed the
// system lets the folder go, and nothing has to be cleared by hand. The other server here is
// this test binary run again as TestServerHoldingAFolder.
func TestAKilledServerLeavesNoLockBehind(t *testing.T) {
	folder := t.TempDir()
	server := exec.Command(os.Args[0], "-test.run=^TestServerHoldingAFolder$", "-test.count=1")
	server.Env = append(os.Environ(), "PORTICO_DATALOCK_FOLDER="+folder)
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer server.Process.Kill()
	for began := time.Now(); ; time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(filepath.Join(folder, "holding")); err == nil {
			break
		}
		if time.Since(began) > 20*time.Second {
			t.Fatal("the other server never took the folder")
		}
	}
	if _, err := Hold(folder, quiet); err == nil {
		t.Fatal("a second server started while another process had the folder")
	}
	server.Process.Kill()
	server.Wait()
	// The system may take a moment to notice the process is gone.
	for began := time.Now(); ; time.Sleep(20 * time.Millisecond) {
		release, err := Hold(folder, quiet)
		if err == nil {
			release()
			return
		}
		if time.Since(began) > 5*time.Second {
			t.Fatalf("after the other server was killed: %v", err)
		}
	}
}

// TestServerHoldingAFolder is the other server in TestAKilledServerLeavesNoLockBehind. It
// does nothing when run by itself.
func TestServerHoldingAFolder(t *testing.T) {
	folder := os.Getenv("PORTICO_DATALOCK_FOLDER")
	if folder == "" {
		t.Skip("run by TestAKilledServerLeavesNoLockBehind")
	}
	if _, err := Hold(folder, quiet); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(folder, "holding"), nil, 0o644)
	time.Sleep(time.Minute) // until killed
}

// Some network shares cannot lock files at all. The server still starts there, and the log
// says that nothing guards the folder.
func TestAFolderThatCannotLockStillStartsWithAWarning(t *testing.T) {
	usual := tryLock
	tryLock = func(*os.File) (bool, error) { return false, errors.ErrUnsupported }
	t.Cleanup(func() { tryLock = usual })
	var said strings.Builder
	release, err := Hold(t.TempDir(), slog.New(slog.NewTextHandler(&said, nil)))
	if err != nil || !strings.Contains(said.String(), "cannot lock files") {
		t.Fatalf("a folder that cannot lock: %q, %v", said.String(), err)
	}
	release()
}
