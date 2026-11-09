//go:build !windows

package processpool

import (
	"os/exec"

	"github.com/PorticoMediaServer/portico/server-next/internal/childprocess"
)

func configureProcess(cmd *exec.Cmd) { childprocess.Configure(cmd) }
func killProcess(cmd *exec.Cmd)      { childprocess.Kill(cmd) }
