//go:build windows

package storage

import (
	"os/exec"

	"github.com/PorticoMediaServer/portico/server/internal/childprocess"
)

func configureProcess(cmd *exec.Cmd) { childprocess.Configure(cmd) }
func killProcess(cmd *exec.Cmd)      { childprocess.Kill(cmd) }
