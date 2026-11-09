//go:build !windows

package artifactlease

import (
	"errors"
	"os"
	"syscall"
)

func lock(f *os.File, mode int) error {
	if f == nil {
		return ErrUnsupported
	}
	e := syscall.Flock(int(f.Fd()), mode|syscall.LOCK_NB)
	if errors.Is(e, syscall.EWOULDBLOCK) || errors.Is(e, syscall.EAGAIN) {
		return ErrBusy
	}
	return e
}
func Shared(f *os.File) error    { return lock(f, syscall.LOCK_SH) }
func Exclusive(f *os.File) error { return lock(f, syscall.LOCK_EX) }
