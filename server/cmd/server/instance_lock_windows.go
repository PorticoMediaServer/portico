//go:build windows

package main

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// LockFileEx with LOCKFILE_FAIL_IMMEDIATELY is Windows' advisory equivalent: the
// lock lives with the file handle, so closing the process releases it however the
// process ended.
func lockInstanceFile(file *os.File) error {
	overlapped := new(windows.Overlapped)
	err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, overlapped)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, windows.ERROR_LOCK_VIOLATION), errors.Is(err, windows.ERROR_SHARING_VIOLATION), errors.Is(err, windows.ERROR_IO_PENDING):
		return errInstanceLockHeld
	case errors.Is(err, windows.ERROR_INVALID_FUNCTION), errors.Is(err, windows.ERROR_NOT_SUPPORTED):
		// A network redirector that does not implement byte-range locks.
		return errInstanceLockUnsupported
	default:
		return err
	}
}

func unlockInstanceFile(file *os.File) {
	overlapped := new(windows.Overlapped)
	_ = windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, overlapped)
}
