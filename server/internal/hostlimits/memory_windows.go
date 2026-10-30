//go:build windows

package hostlimits

import (
	"syscall"
	"unsafe"
)

var globalMemoryStatus = syscall.NewLazyDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")

type memoryStatus struct {
	Length, MemoryLoad                                 uint32
	TotalPhys, AvailPhys, TotalPageFile, AvailPageFile uint64
	TotalVirtual, AvailVirtual, AvailExtendedVirtual   uint64
}

func physicalMemoryBytes() (uint64, bool) {
	status := memoryStatus{}
	status.Length = uint32(unsafe.Sizeof(status))
	ok, _, _ := globalMemoryStatus.Call(uintptr(unsafe.Pointer(&status)))
	if ok == 0 || status.TotalPhys == 0 || status.TotalPhys > 1<<63-1 {
		return 0, false
	}
	return status.TotalPhys, true
}
