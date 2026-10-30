//go:build linux || darwin

package reading

import (
	"os/exec"
	"syscall"
)

// startHelper gives the helper its own process group so a deadline or cancel
// reaches the whole group, not just the leader.
func startHelper(command *exec.Cmd) error {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return command.Start()
}

func killHelper(command *exec.Cmd) error {
	if command.Process == nil {
		return nil
	}
	_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	return command.Process.Kill()
}
