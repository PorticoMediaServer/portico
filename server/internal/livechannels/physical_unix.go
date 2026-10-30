//go:build !windows

package livechannels

import (
	"errors"
	"os"
	"syscall"
)

var ErrPhysicalBusy = errors.New("The previous source worker is still retiring.")

func lockPhysical(f *os.File) error {
	e := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if e == syscall.EWOULDBLOCK || e == syscall.EAGAIN {
		return ErrPhysicalBusy
	}
	return e
}
