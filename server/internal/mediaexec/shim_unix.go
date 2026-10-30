//go:build linux || darwin

package mediaexec

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// execShim applies the process policy to this process and replaces it with
// inner. Limits and an ignored SIGXFSZ survive exec, so they bind the sandbox
// and everything it starts: a write past the file bound fails with EFBIG rather
// than killing a job that holds other output.
func execShim(spec shimSpec, inner []string) error {
	// The ledger entry is written first: the file limit below would refuse it.
	if spec.Ledger != "" {
		if err := recordSelf(spec.Ledger, spec.Instance); err != nil {
			return fmt.Errorf("media process ledger: %w", err)
		}
	}
	_ = unix.Setrlimit(unix.RLIMIT_CORE, &unix.Rlimit{})
	if spec.FileBytes != 0 {
		limit := uint64(0)
		if spec.FileBytes > 0 {
			limit = uint64(spec.FileBytes)
		}
		signal.Ignore(syscall.SIGXFSZ)
		if err := unix.Setrlimit(unix.RLIMIT_FSIZE, &unix.Rlimit{Cur: limit, Max: limit}); err != nil {
			return fmt.Errorf("media process file limit: %w", err)
		}
	}
	if spec.CPUSeconds > 0 {
		limit := uint64(spec.CPUSeconds)
		if err := unix.Setrlimit(unix.RLIMIT_CPU, &unix.Rlimit{Cur: limit, Max: limit + 5}); err != nil {
			return fmt.Errorf("media process CPU limit: %w", err)
		}
	}
	if spec.Background {
		// Background work yields to viewers; it is never paused.
		_ = unix.Setpriority(unix.PRIO_PROCESS, 0, 10)
	}
	return execInPlace(inner)
}

func execInPlace(argv []string) error {
	if len(argv) == 0 || !filepath.IsAbs(argv[0]) {
		return ErrInvalidJob
	}
	return syscall.Exec(argv[0], argv, os.Environ())
}

// A ledger entry names one media process group this server started: its
// leader's PID, which is also the group ID, and the leader's start time and the
// boot it started in, so a recycled PID is never mistaken for it.
type ledgerEntry struct {
	instance string
	pid      int
	identity processIdentity
}

const ledgerHeader = "portico-media-process v1"

func recordSelf(dir, instance string) error {
	pid := os.Getpid()
	identity, err := identityOf(pid)
	if err != nil {
		return err
	}
	// The leader of the group this job runs in is this process: the server
	// starts every media process (and every helper that execs one) in a new
	// group.
	if pgid, err := unix.Getpgid(pid); err != nil || pgid != pid {
		return errors.New("media process is not its own process group")
	}
	text := strings.Join([]string{ledgerHeader, instance, strconv.Itoa(pid), identity.start, identity.boot}, "\n") + "\n"
	path := filepath.Join(dir, strconv.Itoa(pid))
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if _, err = f.WriteString(text); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func readEntry(path string) (ledgerEntry, error) {
	var entry ledgerEntry
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) > 512 {
		return entry, ErrInvalidJob
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) != 5 || lines[0] != ledgerHeader {
		return entry, ErrInvalidJob
	}
	pid, err := strconv.Atoi(lines[2])
	if err != nil || pid <= 1 || strconv.Itoa(pid) != filepath.Base(path) {
		return entry, ErrInvalidJob
	}
	return ledgerEntry{instance: lines[1], pid: pid, identity: processIdentity{start: lines[3], boot: lines[4]}}, nil
}

// reapLedger walks the ledger. At startup (kill) it stops every group a
// previous run left alive; otherwise it only forgets entries whose process has
// ended. A group is killed only when its leader is provably the process that
// was recorded: same PID, same start time, same boot, still its own group.
func reapLedger(dir, instance string, kill bool) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	reaped := 0
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		entry, err := readEntry(path)
		if err != nil {
			// A shim may be writing it right now; only an old unreadable entry is
			// junk.
			if info, statErr := e.Info(); statErr == nil && time.Since(info.ModTime()) > time.Minute {
				_ = os.Remove(path)
			}
			continue
		}
		current, err := identityOf(entry.pid)
		alive := err == nil && current == entry.identity
		if alive {
			if pgid, err := unix.Getpgid(entry.pid); err != nil || pgid != entry.pid {
				alive = false
			}
		}
		switch {
		case alive && (!kill || entry.instance == instance):
			continue
		case alive:
			if unix.Kill(-entry.pid, unix.SIGKILL) == nil {
				reaped++
			}
		}
		_ = os.Remove(path)
	}
	return reaped, nil
}
