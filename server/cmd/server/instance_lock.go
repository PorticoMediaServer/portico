package main

import (
	"errors"
	"fmt"
	"os"
)

// The whole architecture rests on "one process, one SQLite file, one writer",
// and the write gate that enforces the second half of that sentence is a
// process-local mutex. Two servers pointed at one state directory arbitrate
// nothing: SQLite's own locking keeps the file structurally intact, but you get
// SQLITE_BUSY reaching callers despite the gate, two scanners writing
// conflicting catalogue rows, two converters writing the same
// <state>/hls/<session>/ paths, two discovery responders claiming one identity,
// and two sets of ffmpeg children.
//
// The lock is advisory and held for the life of the process, so the OS releases
// it on a crash. A pid file with O_EXCL would be the wrong shape: a crash leaves
// the file behind and the next start fails for a process that no longer exists.
//
// It is taken *after* the listener binds, so the second instance's honest error
// is something an operator can see, and *before* the database opens, so the
// second instance never touches the file at all.

// errInstanceLockHeld means another process holds the state directory.
var errInstanceLockHeld = errors.New("another Portico server is already using this state directory")

// errInstanceLockUnsupported means the filesystem cannot answer the question.
// Some network and bind-mounted filesystems do not honour advisory locks. State
// on those filesystems is unsafe; startup must refuse before opening it.
var errInstanceLockUnsupported = errors.New("this filesystem does not support advisory locking")

const instanceLockStorageAdvice = "Portico's state folder must be on storage with working file locks: a local disk or a Docker volume. Media libraries can stay on network storage. Two servers must never share one state folder, including from different machines."

// instanceLockFailure is the narrowly scoped operator diagnostic permitted on
// the startup recovery page. Other startup errors still redact private causes.
type instanceLockFailure struct {
	state string
	cause error
}

func (e *instanceLockFailure) Error() string {
	return fmt.Sprintf("Cannot lock state folder %q: %v. %s", e.state, e.cause, instanceLockStorageAdvice)
}

func (e *instanceLockFailure) Unwrap() error { return e.cause }

// instanceLock is a held exclusive lock on the state directory's lock file.
type instanceLock struct{ file *os.File }

// acquireInstanceLock takes the lock, or reports why it could not.
func acquireInstanceLock(path string) (*instanceLock, error) {
	return acquireInstanceLockWith(path, lockInstanceFile)
}

func acquireInstanceLockWith(path string, lock func(*os.File) error) (*instanceLock, error) {
	// Go opens these descriptors close-on-exec. Keep that default: FFmpeg and
	// other children must not inherit the lock and extend its owner's lifetime.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = lock(file); err != nil {
		file.Close()
		return nil, err
	}
	// A successful syscall is not enough on filesystems that silently ignore
	// advisory locks. A distinct open-file description must be refused. This
	// cannot detect cross-host NFS configured with host-local flock semantics;
	// sharing one state folder between machines remains unsupported.
	probe, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		file.Close()
		return nil, err
	}
	firstInfo, firstErr := file.Stat()
	probeInfo, probeErr := probe.Stat()
	if firstErr != nil || probeErr != nil || !os.SameFile(firstInfo, probeInfo) {
		probe.Close()
		file.Close()
		return nil, fmt.Errorf("%w: lock file changed while checking exclusivity", errInstanceLockUnsupported)
	}
	err = lock(probe)
	closeErr := probe.Close()
	if !errors.Is(err, errInstanceLockHeld) || closeErr != nil {
		file.Close()
		if err == nil {
			return nil, fmt.Errorf("%w: a second handle acquired the exclusive lock", errInstanceLockUnsupported)
		}
		return nil, fmt.Errorf("cannot verify exclusive state locking: %w", errors.Join(err, closeErr))
	}
	return &instanceLock{file: file}, nil
}

// Close releases the lock. The OS would release it anyway when the process ends;
// this makes an orderly shutdown orderly.
func (l *instanceLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockInstanceFile(l.file)
	err := l.file.Close()
	l.file = nil
	return err
}
