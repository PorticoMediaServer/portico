//go:build !windows

package main

import (
	"errors"
	"os"
	"syscall"
)

// flock is advisory, per open file description, and released by the kernel when
// the process dies for any reason — which is exactly the property a
// single-instance guard needs and a pid file does not have.
func lockInstanceFile(file *os.File) error {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, syscall.EWOULDBLOCK), errors.Is(err, syscall.EAGAIN), errors.Is(err, syscall.EACCES):
		return errInstanceLockHeld
	case errors.Is(err, syscall.ENOTSUP), errors.Is(err, syscall.EOPNOTSUPP), errors.Is(err, syscall.ENOLCK), errors.Is(err, syscall.EINVAL), errors.Is(err, syscall.ENOSYS):
		// A bind-mounted state directory on some network filesystems answers this
		// way. Refusing to start would be worse than running unguarded.
		return errInstanceLockUnsupported
	default:
		return err
	}
}

func unlockInstanceFile(file *os.File) {
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
