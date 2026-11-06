package main

import (
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/stateownership"
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
var errInstanceLockHeld = stateownership.ErrHeld

// errInstanceLockUnsupported means the filesystem cannot answer the question.
// Some network and bind-mounted filesystems do not honour advisory locks. State
// on those filesystems is unsafe; startup must refuse before opening it.
var errInstanceLockUnsupported = stateownership.ErrUnsupported

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

// instanceLock reuses the actual shared native state lock. Trusted store
// composition may borrow it; shutdown cannot unlock borrowed physical work.
type instanceLock = stateownership.Lock

func acquireInstanceLock(path string) (*instanceLock, error) {
	return stateownership.Acquire(path)
}
