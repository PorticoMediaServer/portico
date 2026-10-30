//go:build windows

package recordingmedia

import (
	"golang.org/x/sys/windows"
	"unsafe"
)

func freeBytes(path string) (int64, error) {
	p, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return 0, e
	}
	var free, total, available uint64
	e = windows.GetDiskFreeSpaceEx(p, (*uint64)(unsafe.Pointer(&free)), &total, &available)
	return int64(free), e
}
