package archive7z

import (
	"errors"
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

// proc_taskinfo from <sys/proc_info.h>: six uint64s followed by twelve int32s.
// Virtual size includes the Go runtime and dyld reservations, not just RSS.
// https://github.com/apple-oss-distributions/xnu/blob/xnu-11417.140.69/bsd/sys/proc_info.h#L119
// Keeping the native call here also supports the CGO_ENABLED=0 server build.
type darwinTaskInfo struct {
	VirtualSize uint64
	_           [88]byte
}

func workerAddressLimit() (uint64, error) { return darwinAddressLimit(readDarwinTaskInfo) }

func darwinAddressLimit(read func(*darwinTaskInfo) (uintptr, error)) (uint64, error) {
	var task darwinTaskInfo
	n, err := read(&task)
	if err != nil || n != unsafe.Sizeof(task) || task.VirtualSize == 0 || task.VirtualSize > uint64(1<<63-1)-workerAddressBudget {
		return 0, errors.New("native address space measurement unavailable")
	}
	return task.VirtualSize + workerAddressBudget, nil
}

func readDarwinTaskInfo(task *darwinTaskInfo) (uintptr, error) {
	// proc_info(PROC_INFO_CALL_PIDINFO=2, pid, PROC_PIDTASKINFO=4, 0, buffer, size).
	n, _, errno := syscall.Syscall6(syscall.SYS_PROC_INFO, 2, uintptr(os.Getpid()), 4, 0, uintptr(unsafe.Pointer(task)), unsafe.Sizeof(*task))
	runtime.KeepAlive(task)
	if errno != 0 {
		return 0, errno
	}
	return n, nil
}
