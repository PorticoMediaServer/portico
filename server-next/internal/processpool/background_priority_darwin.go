//go:build darwin

package processpool

import (
	"os/exec"

	"golang.org/x/sys/unix"
)

// nice 19 is the lowest CPU scheduling priority available to an unprivileged
// process. taskpolicy -b gives the child background (throttled) disk IO. Both
// wrappers exec the confined command; playback is never sent through them.
func lowerBackgroundPriority(cmd *exec.Cmd) {
	original := append([]string{cmd.Path}, cmd.Args[1:]...)
	cmd.Path = "/usr/bin/nice"
	cmd.Args = append([]string{"nice", "-n", "19", "/usr/sbin/taskpolicy", "-b"}, original...)
}

func applyBackgroundPriority(*exec.Cmd) {}

// lowerCurrentThread puts the calling thread in the Darwin background band:
// lowest CPU priority and throttled disk IO (setpriority PRIO_DARWIN_THREAD,
// PRIO_DARWIN_BG from <sys/resource.h>). The caller is one of
// runLowPriority's threads, locked for the life of the process.
func lowerCurrentThread() {
	const prioDarwinThread, prioDarwinBG = 3, 0x1000
	_ = unix.Setpriority(prioDarwinThread, 0, prioDarwinBG)
}
