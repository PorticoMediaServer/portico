//go:build !darwin && !linux && !windows

package processpool

import "os/exec"

func lowerBackgroundPriority(cmd *exec.Cmd) {}
func applyBackgroundPriority(cmd *exec.Cmd) {}

// lowerCurrentThread is unused here: only Linux and Darwin roots are served in
// process (mountIsLocal).
func lowerCurrentThread() {}
