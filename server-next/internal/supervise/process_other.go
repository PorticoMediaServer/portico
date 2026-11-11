//go:build !windows

package supervise

import "os/exec"

func noConsole(*exec.Cmd) {}
