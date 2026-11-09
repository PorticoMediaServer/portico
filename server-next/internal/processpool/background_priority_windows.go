//go:build windows

package processpool

import (
	"os/exec"
	"syscall"
)

const idlePriorityClass = 0x00000040

func lowerBackgroundPriority(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= idlePriorityClass
}

func applyBackgroundPriority(*exec.Cmd) {}

// lowerCurrentThread is unused here: only Linux and Darwin roots are served in
// process (mountIsLocal).
func lowerCurrentThread() {}
