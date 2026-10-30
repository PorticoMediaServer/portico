//go:build !windows

package mounts

import (
	"os"
	"path/filepath"
	"syscall"
)

// The OS releases this lock on crash. It fences allocation I/O against startup
// recovery even if another server process is accidentally pointed at this state.
func (s *Service) allocationLock() (func(), error) {
	f, e := os.OpenFile(filepath.Join(s.private, "allocation.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, e
	}
	return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}
