//go:build unix

package scan

import (
	"io/fs"
	"strconv"
	"syscall"
)

// device is the number of the disk a file or folder is on, or 0 when it cannot be told.
func device(info fs.FileInfo) int64 {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return int64(stat.Dev)
	}
	return 0
}

// folderID names the folder itself, however it was reached, or is "" when it cannot be told.
func folderID(info fs.FileInfo) string {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return strconv.FormatUint(uint64(stat.Dev), 10) + ":" + strconv.FormatUint(uint64(stat.Ino), 10)
	}
	return ""
}
