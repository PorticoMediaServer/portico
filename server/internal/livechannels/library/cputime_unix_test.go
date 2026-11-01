//go:build !windows

package librarychannels

import (
	"syscall"
	"time"
)

// cpuTime is this process's user plus system CPU time: on a one-core host it,
// not wall time on a fast disk, is what a generation costs everyone else.
func cpuTime() time.Duration {
	var u syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &u)
	return time.Duration(u.Utime.Nano() + u.Stime.Nano())
}
