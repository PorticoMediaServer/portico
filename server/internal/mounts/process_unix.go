//go:build !windows

package mounts

import (
	"os"
	"os/exec"
	"syscall"

	"github.com/PorticoMediaServer/portico/server/internal/childprocess"
)

func ownProcess(c *exec.Cmd) { childprocess.Configure(c) }
func signalProcess(c *exec.Cmd, force bool) {
	if c.Process != nil {
		signal := syscall.SIGTERM
		if force {
			signal = syscall.SIGKILL
		}
		_ = syscall.Kill(-c.Process.Pid, signal)
	}
}

func lockMount(path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, err
	}
	return func() { file.Close() }, nil
}
