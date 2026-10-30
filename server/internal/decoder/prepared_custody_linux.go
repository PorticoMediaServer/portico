//go:build linux

package decoder

import (
	"fmt"
	"os"
	"syscall"
	"time"
)

// Open an independent description of the *same inode* before launching. It is
// not passed to the child. Path replacement cannot redirect the retirement wait.
func (c *PreparedFileCustody) waiter() (*os.File, error) {
	if c == nil || c.file == nil {
		return nil, ErrInvalidConfiguration
	}
	next, e := os.OpenFile(fmt.Sprintf("/proc/self/fd/%d", c.file.Fd()), os.O_RDWR, 0)
	if e != nil {
		return nil, e
	}
	a, e := c.file.Stat()
	b, be := next.Stat()
	if e != nil || be != nil || !a.Mode().IsRegular() || !os.SameFile(a, b) {
		next.Close()
		return nil, ErrInvalidConfiguration
	}
	return next, nil
}
func (c *PreparedFileCustody) retired(next *os.File) {
	// Do not LOCK_UN a shared description: that would unlock sandbox init too.
	_ = c.file.Close()
	c.file = next
	for {
		e := syscall.Flock(int(next.Fd()), syscall.LOCK_EX)
		if e == nil {
			return
		}
		// Interrupted waits retry. Any unexpected local lock failure retains custody
		// rather than manufacturing completion and deleting a live child's bytes.
		if e != syscall.EINTR {
			time.Sleep(time.Second)
		}
	}
}
