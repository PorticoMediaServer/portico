//go:build unix

package scan

import (
	"io/fs"
	"strconv"
	"syscall"
)

// folderID names the folder itself, however it was reached, or is "" when it cannot be told.
func folderID(info fs.FileInfo) string {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return strconv.FormatUint(uint64(stat.Dev), 10) + ":" + strconv.FormatUint(uint64(stat.Ino), 10)
	}
	return ""
}
