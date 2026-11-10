//go:build unix

package scan

import (
	"io/fs"
	"syscall"
)

// device is the number of the disk a file or folder is on, or 0 when it cannot be told.
func device(info fs.FileInfo) int64 {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return int64(stat.Dev)
	}
	return 0
}
