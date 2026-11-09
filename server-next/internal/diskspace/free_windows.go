//go:build windows

package diskspace

import (
	"golang.org/x/sys/windows"
)

func freeBytes(path string) (int64, error) {
	p, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return 0, e
	}
	// The first value is what this process may use, which is the one that matters
	// under a disk quota; total and free-on-volume are read but not needed.
	var available, total, free uint64
	if e = windows.GetDiskFreeSpaceEx(p, &available, &total, &free); e != nil {
		return 0, e
	}
	if available > uint64(1)<<62 {
		return 1 << 62, nil
	}
	return int64(available), nil
}
