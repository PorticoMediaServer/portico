//go:build windows

package storage

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
