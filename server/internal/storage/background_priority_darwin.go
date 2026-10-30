//go:build darwin

package storage

import "os/exec"

// nice 19 is the lowest CPU scheduling priority available to an unprivileged
// process. taskpolicy -b gives the child background (throttled) disk IO. Both
// wrappers exec the confined command; playback is never sent through them.
func lowerBackgroundPriority(cmd *exec.Cmd) {
	original := append([]string{cmd.Path}, cmd.Args[1:]...)
	cmd.Path = "/usr/bin/nice"
	cmd.Args = append([]string{"nice", "-n", "19", "/usr/sbin/taskpolicy", "-b"}, original...)
}

func applyBackgroundPriority(*exec.Cmd) {}
