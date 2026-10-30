//go:build unix

package mounts

import (
	"os"
	"syscall"
)

// openConfigFile opens the fixed configuration path without waiting for a writer
// or device. The caller verifies the same descriptor's mode before reading, so a
// FIFO or device swapped in after any earlier check is refused, not read.
func openConfigFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
