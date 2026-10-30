//go:build linux

package worker

import (
	"golang.org/x/sys/unix"
	"log"
	"runtime"
)

// LowerPriority dedicates the calling background goroutine's OS thread to low
// CPU and idle I/O priority. It must run only in a worker-owned goroutine. Never
// unlock this thread: an unprivileged process cannot restore nice 0, so letting
// it return to the runtime could put a foreground request on a low-priority
// thread. Go retires a locked thread when its goroutine exits.
func LowerPriority() {
	runtime.LockOSThread()
	tid := unix.Gettid()
	if err := unix.Setpriority(unix.PRIO_PROCESS, tid, 19); err != nil {
		log.Printf("Background worker CPU priority: %v", err)
	}
	const idle = 3 << 13
	if _, _, err := unix.Syscall(unix.SYS_IOPRIO_SET, 1, uintptr(tid), idle); err != 0 {
		log.Printf("Background worker I/O priority: %v", err)
	}
}
