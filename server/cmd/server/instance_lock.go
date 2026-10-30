package main

import (
	"errors"
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
// Some network and bind-mounted filesystems do not honour advisory locks; that
// is a reason to warn, not a reason to refuse to start.
var errInstanceLockUnsupported = errors.New("this filesystem does not support advisory locking")

// instanceLock is a held exclusive lock on the state directory's lock file.
type instanceLock struct{ file *os.File }

// acquireInstanceLock takes the lock, or reports why it could not.
func acquireInstanceLock(path string) (*instanceLock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = lockInstanceFile(file); err != nil {
		file.Close()
		return nil, err
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
