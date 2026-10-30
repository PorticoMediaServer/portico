//go:build !windows

package atomicfile

import "os"

// syncDirectory makes the rename itself durable. Without it the new name can be
// lost in a power cut even though the file's own bytes reached the disk.
func syncDirectory(path string) {
	directory, err := os.Open(path)
	if err != nil {
		return
	}
	_ = directory.Sync()
	_ = directory.Close()
}
