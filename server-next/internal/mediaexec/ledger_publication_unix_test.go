//go:build linux || darwin

package mediaexec

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/storagepaths"
	"golang.org/x/sys/unix"
)

// Hold a real child/group's ledger at the boundary recordSelf exposes between
// creating its pathname and writing its identity. No production hook, timing
// delay, polling or retry is involved. Pipe signals provide the explicit publication barriers.
func TestLedgerPublicationChild(t *testing.T) {
	if os.Getenv("PORTICO_TEST_LEDGER_PUBLICATION") != "1" {
		return
	}
	dir := os.Getenv("PORTICO_TEST_LEDGER_DIR")
	path := filepath.Join(dir, strconv.Itoa(os.Getpid()))
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	created := os.NewFile(3, "ledger-created")
	release := os.NewFile(4, "publish-ledger")
	published := os.NewFile(5, "ledger-published")
	if err = created.Close(); err != nil {
		t.Fatal(err)
	}
	var signal [1]byte
	if n, err := release.Read(signal[:]); n != 1 || err != nil {
		t.Fatalf("publication release: %d %v", n, err)
	}
	if err = recordSelf(dir, "previous"); err != nil {
		t.Fatal(err)
	}
	if err = published.Close(); err != nil {
		t.Fatal(err)
	}
	// The parent kills this exact proven process group and joins us. The open
	// pipe also lets failure cleanup unblock us without a sleep or busy loop.
	_, _ = release.Read(signal[:])
}

func TestLedgerPathVisibilityPrecedesPublication(t *testing.T) {
	resetForTest(t)
	state := t.TempDir()
	configureForTest(t, state)
	dir := storagepaths.Path(state, "run", "media-processes")
	createdR, createdW := ledgerBarrier(t)
	releaseR, releaseW := ledgerBarrier(t)
	publishedR, publishedW := ledgerBarrier(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self, "-test.run=^TestLedgerPublicationChild$")
	cmd.Env = append(os.Environ(), "PORTICO_TEST_LEDGER_PUBLICATION=1", "PORTICO_TEST_LEDGER_DIR="+dir)
	cmd.ExtraFiles = []*os.File{createdW, releaseR, publishedW}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stderr = os.Stderr
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unix.Kill(-cmd.Process.Pid, unix.SIGKILL); _ = cmd.Wait() })
	_ = createdW.Close()
	_ = releaseR.Close()
	_ = publishedW.Close()
	awaitLedgerBarrier(t, createdR)
	pid := strconv.Itoa(cmd.Process.Pid)
	// This is the OLD test's exact readiness predicate, deliberately made true
	// before publication. Its old want-1 assertion now fails deterministically.
	if !contains(ledgerEntries(t, state), pid) {
		t.Fatal("negative control did not expose the pathname")
	}
	if _, err = readEntry(filepath.Join(dir, pid)); err == nil {
		t.Fatal("incomplete identity parsed")
	}
	reaped := configureForTest(t, state)
	if reaped != 0 {
		t.Fatalf("killed %d groups without proven identity", reaped)
	}
	// Opt-in negative control executes the old assertion at the forced gap.
	// It must fail with the reported symptom, rather than rely on scheduling.
	if os.Getenv("PORTICO_TEST_LEDGER_PATH_ONLY") == "1" && reaped != 1 {
		t.Fatalf("reaped %d groups, want 1", reaped)
	}
	if err = unix.Kill(cmd.Process.Pid, 0); err != nil {
		t.Fatalf("unproven child was killed: %v", err)
	}
	if !contains(ledgerEntries(t, state), pid) {
		t.Fatal("fresh incomplete entry was removed")
	}
	t.Log("old path-only readiness gives reaped 0 groups, want 1")
	if _, err = releaseW.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	awaitLedgerBarrier(t, publishedR)
	if _, err = readEntry(filepath.Join(dir, pid)); err != nil {
		t.Fatal(err)
	}
	if reaped := configureForTest(t, state); reaped != 1 {
		t.Fatalf("reaped %d groups after publication, want 1", reaped)
	}
}

func ledgerBarrier(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
	return r, w
}

func awaitLedgerBarrier(t *testing.T, r *os.File) {
	t.Helper()
	if err := r.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var signal [1]byte
	if n, err := r.Read(signal[:]); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("ledger barrier: n=%d error=%v", n, err)
	}
}
