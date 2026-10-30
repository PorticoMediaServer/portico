//go:build linux

package storage

import (
	"os"
	"os/exec"
	"strconv"

	"golang.org/x/sys/unix"
)

// Linux has no pre-exec priority field in os/exec. Apply the kernel policies
// directly to the child after Start, without depending on /usr/bin/nice or
// ionice existing on BusyBox and NixOS hosts.
func lowerBackgroundPriority(*exec.Cmd) {}

// Linux nice and I/O priorities are per thread, and a new thread inherits them
// from the thread that creates it. Lowering only the child's PID (its main
// thread) left any thread the child had already started at normal priority
// (B64), and everything those threads spawned inherited it. So every thread
// listed under /proc/<pid>/task is lowered, twice, to catch a thread created
// by a not-yet-lowered thread during the first sweep; threads created after
// that inherit the lowered policy.
func applyBackgroundPriority(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	lowerThread(pid)
	for sweep := 0; sweep < 2; sweep++ {
		tasks, err := os.ReadDir("/proc/" + strconv.Itoa(pid) + "/task")
		if err != nil {
			return
		}
		for _, task := range tasks {
			if tid, err := strconv.Atoi(task.Name()); err == nil {
				lowerThread(tid)
			}
		}
	}
}

func lowerThread(tid int) {
	_ = unix.Setpriority(unix.PRIO_PROCESS, tid, 19)
	const (
		ioprioWhoProcess = 1
		ioprioClassIdle  = 3
		ioprioClassShift = 13
	)
	_, _, _ = unix.Syscall(unix.SYS_IOPRIO_SET, uintptr(ioprioWhoProcess), uintptr(tid), uintptr(ioprioClassIdle<<ioprioClassShift))
}
