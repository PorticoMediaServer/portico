package datalock

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func systemLock(file *os.File) (heldByAnother bool, err error) {
	// The first byte of the file, which need not exist to be locked.
	err = windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
	switch {
	case err == nil:
		return false, nil
	case errors.Is(err, windows.ERROR_LOCK_VIOLATION), errors.Is(err, windows.ERROR_IO_PENDING):
		return true, nil
	}
	return false, err
}
