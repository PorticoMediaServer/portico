//go:build windows

package telemetry

import (
	"syscall"
	"unsafe"
)

// VolumeUsage reports the bytes an unprivileged process may still write to the
// volume holding path — honouring any per-user quota, which is what
// GetDiskFreeSpaceExW's first output measures — and the volume's total size.
func VolumeUsage(path string) (int64, int64, bool) {
	if path == "" {
		return 0, 0, false
	}
	wide, e := syscall.UTF16PtrFromString(path)
	if e != nil {
		return 0, 0, false
	}
	var free, total, totalFree uint64
	ok, _, _ := getDiskFreeSpace.Call(uintptr(unsafe.Pointer(wide)), uintptr(unsafe.Pointer(&free)), uintptr(unsafe.Pointer(&total)), uintptr(unsafe.Pointer(&totalFree)))
	if ok == 0 {
		return 0, 0, false
	}
	return int64(free), int64(total), true
}
