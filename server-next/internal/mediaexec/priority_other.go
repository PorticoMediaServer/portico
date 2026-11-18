//go:build !windows

package mediaexec

import "os/exec"

// On Unix the limits shim sets a media process's priority before it starts (shim_unix.go).
func lowerPriority(*exec.Cmd, bool) {}
