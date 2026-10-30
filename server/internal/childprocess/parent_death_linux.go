//go:build linux

package childprocess

import (
	"os/exec"
	"syscall"
)

func configureParentDeath(cmd *exec.Cmd) { cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL }
