package archive7z

import (
	"errors"
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

// proc_info(PROC_INFO_CALL_PIDINFO, pid, PROC_PIDTASKINFO, 0, buffer, size).
// proc_taskinfo begins with six uint64s and twelve int32s (96 bytes).
func darwinVMSize() (uint64, error) {
	var task [12]uint64
	n, _, errno := syscall.Syscall6(syscall.SYS_PROC_INFO, 2, uintptr(os.Getpid()), 4, 0, uintptr(unsafe.Pointer(&task)), unsafe.Sizeof(task))
	runtime.KeepAlive(&task)
	if errno != 0 {
		return 0, errno
	}
	if n != unsafe.Sizeof(task) || task[0] == 0 {
		return 0, errors.New("invalid native VM observation")
	}
	return task[0], nil
}
