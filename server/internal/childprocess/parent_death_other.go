//go:build !linux && !windows

package childprocess

import "os/exec"

func configureParentDeath(cmd *exec.Cmd) {}
