//go:build !windows

package main

import (
	"os"
	"syscall"
)

// restartSelf replaces this process with a new run of the same program: the same process id,
// so a service manager or a container sees no change.
func restartSelf() error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	return syscall.Exec(self, os.Args, os.Environ())
}
