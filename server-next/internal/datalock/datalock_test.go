package datalock

import (
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A second server on a data folder is refused, in words that name the folder, and may start
// once the first has stopped. A server on another folder is nobody's business.
func TestASecondServerOnTheFolderIsRefusedUntilTheFirstStops(t *testing.T) {
	folder := t.TempDir()
	first, err := Hold(folder)
	if err != nil {
		t.Fatalf("the first server: %v", err)
	}
	_, err = Hold(folder)
	if err == nil {
		t.Fatal("a second server started on the same data folder")
	}
	for _, want := range []string{"another Portico server", folder, "Nothing in the folder was changed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	other, err := Hold(t.TempDir())
	if err != nil {
		t.Fatalf("a server on another folder: %v", err)
	}
	other.Release()
	first.Release()
	again, err := Hold(folder)
	if err != nil {
		t.Fatalf("after the first server stopped: %v", err)
	}
	again.Release()
}

// A server refused a folder it had taken leaves no lock file there, and the folder can be
// taken again.
func TestAServerRefusedAfterTakingTheFolderLeavesNoLockFile(t *testing.T) {
	folder := t.TempDir()
	held, err := Hold(folder)
	if err != nil {
		t.Fatal(err)
	}
	held.Drop()
	held.Release()
	if _, err := os.Stat(filepath.Join(folder, FileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the lock file is still there: %v", err)
	}
	again, err := Hold(folder)
	if err != nil {
		t.Fatalf("taking the folder again: %v", err)
	}
	again.Release()
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
	if _, err := Hold(folder); err == nil {
		t.Fatal("a second server started while another process had the folder")
	}
	server.Process.Kill()
	server.Wait()
	// The system may take a moment to notice the process is gone.
	for began := time.Now(); ; time.Sleep(20 * time.Millisecond) {
		held, err := Hold(folder)
		if err == nil {
			held.Release()
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
	if _, err := Hold(folder); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(folder, "holding"), nil, 0o644)
	time.Sleep(time.Minute) // until killed
}

// Some network shares cannot lock files at all, and on others the lock counts on this
// machine only. The server still starts there, and its log says what does not guard the
// folder. A folder on a local disk that locks gets no warning.
func TestAFolderTheLockDoesNotFullyGuardStillStartsWithAWarning(t *testing.T) {
	usualLock, usualShare := tryLock, networkShare
	t.Cleanup(func() { tryLock, networkShare = usualLock, usualShare })
	warned := func() string {
		held, err := Hold(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		defer held.Release()
		var said strings.Builder
		held.Warn(slog.New(slog.NewTextHandler(&said, nil)))
		return said.String()
	}
	networkShare = func(string) bool { return false }
	if said := warned(); said != "" {
		t.Errorf("a local folder that locks was warned about: %s", said)
	}
	networkShare = func(string) bool { return true }
	if said := warned(); !strings.Contains(said, "network file system") || !strings.Contains(said, "another machine") {
		t.Errorf("a folder on a network share: %q", said)
	}
	tryLock = func(*os.File) (bool, error) { return false, errors.ErrUnsupported }
	if said := warned(); !strings.Contains(said, "cannot lock files") {
		t.Errorf("a folder that cannot lock: %q", said)
	}
}
