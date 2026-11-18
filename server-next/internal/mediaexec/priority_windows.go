//go:build windows

package mediaexec

import (
	"os/exec"
	"syscall"
)

// Windows has no limits shim, so the priority the shim sets on Unix (shim_unix.go) is given
// here: a viewer's conversion runs below the server, so pages and played-as-is files are
// served first when conversions fill the processor, and background work runs only when idle.
const (
	belowNormalPriorityClass = 0x00004000
	idlePriorityClass        = 0x00000040
)

func lowerPriority(cmd *exec.Cmd, background bool) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	if background {
		cmd.SysProcAttr.CreationFlags |= idlePriorityClass
	} else {
		cmd.SysProcAttr.CreationFlags |= belowNormalPriorityClass
	}
}
