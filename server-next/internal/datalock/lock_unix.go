//go:build !windows

package datalock

import (
	"errors"
	"os"
	"syscall"
)

func systemLock(file *os.File) (heldByAnother bool, err error) {
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		switch {
		case err == nil:
			return false, nil
		case errors.Is(err, syscall.EWOULDBLOCK):
			return true, nil
		case errors.Is(err, syscall.EINTR):
			continue
		}
		return false, err
	}
}
