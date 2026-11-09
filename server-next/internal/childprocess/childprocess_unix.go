//go:build !windows

package childprocess

import (
	"os/exec"
	"syscall"
)

// A new process group is what makes `kill(-pid)` reach the whole tree, which is
// the only way to end a sandbox wrapper together with what it wrapped.
func configure(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	configureParentDeath(cmd)
}

func kill(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	// Negative pid is the group. If the group was never created — a caller that
	// forgot Configure — fall back to the process itself rather than signalling
	// nothing.
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill()
	}
}

// Unix already gives the server a way to reach its own descendants, and a
// process group per child is the finer-grained version of it.
func adoptTree() error { return nil }
