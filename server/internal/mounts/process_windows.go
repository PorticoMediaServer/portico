//go:build windows

package mounts

import (
	"errors"
	"os/exec"

	"github.com/PorticoMediaServer/portico/server/internal/childprocess"
)

func ownProcess(c *exec.Cmd)                { childprocess.Configure(c) }
func signalProcess(c *exec.Cmd, force bool) { childprocess.Kill(c) }

func lockMount(path string) (func(), error) {
	return nil, errors.New("managed mount locking unsupported")
}
