//go:build windows

package supervise

import (
	"os/exec"
	"syscall"
)

// detachedProcess is Windows's DETACHED_PROCESS: the new process has no console.
const detachedProcess = 0x00000008

// noConsole starts the worker without a console. A service has none, so Windows would give
// its worker a new one, and every process with a console is told when anyone signs out of
// Windows. Go turns that into a request to stop, and the server would stop for good each
// time somebody signed out.
func noConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess}
}
