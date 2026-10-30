//go:build linux

package worker

import (
	"golang.org/x/sys/unix"
	"runtime"
	"testing"
)

func TestBackgroundPriorityKeepsForegroundThreadUnchanged(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	parent := unix.Gettid()
	before, err := unix.Getpriority(unix.PRIO_PROCESS, parent)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		tid, nice, io int
		err           error
	}
	done := make(chan result, 1)
	go func() {
		LowerPriority()
		tid := unix.Gettid()
		nice, err := unix.Getpriority(unix.PRIO_PROCESS, tid)
		io, _, ioerr := unix.Syscall(unix.SYS_IOPRIO_GET, 1, uintptr(tid), 0)
		if err == nil && ioerr != 0 {
			err = ioerr
		}
		done <- result{tid, nice, int(io), err}
		// No UnlockOSThread: the runtime must retire this thread on exit.
	}()
	got := <-done
	if got.err != nil {
		t.Fatal(got.err)
	}
	// Linux Getpriority returns 20 - nice, so nice 19 is returned as 1.
	if got.tid == parent || got.nice != 1 || got.io>>13 != 3 {
		t.Fatalf("background policy: %+v", got)
	}
	after, err := unix.Getpriority(unix.PRIO_PROCESS, parent)
	if err != nil || after != before {
		t.Fatalf("foreground priority changed: %d -> %d (%v)", before, after, err)
	}
}
