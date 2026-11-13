package reading

import (
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

func archiveAddressSpace() (uint64, error) {
	// proc_taskinfo includes Go's reserved virtual address space.
	var task struct {
		virtual uint64
		rest    [88]byte
	}
	n, _, errno := syscall.Syscall6(syscall.SYS_PROC_INFO, 2, uintptr(os.Getpid()), 4, 0, uintptr(unsafe.Pointer(&task)), unsafe.Sizeof(task))
	runtime.KeepAlive(&task)
	if errno != 0 {
		return 0, errno
	}
	if n != unsafe.Sizeof(task) || task.virtual == 0 || task.virtual > 1<<63 {
		return 0, errDamaged
	}
	return task.virtual, nil
}
