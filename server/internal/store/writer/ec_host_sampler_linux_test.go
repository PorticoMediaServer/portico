//go:build linux

package writer

import (
	"os"
	"syscall"
)

// deviceNumbers returns the major/minor of the device holding path.
func deviceNumbers(path string) (major, minor uint32, ok bool) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, 0, false
	}
	st, isStat := info.Sys().(*syscall.Stat_t)
	if !isStat {
		return 0, 0, false
	}
	dev := uint64(st.Dev)
	major = uint32((dev>>8)&0xfff | (dev>>32)&^uint64(0xfff))
	minor = uint32(dev&0xff | (dev>>12)&^uint64(0xff))
	return major, minor, true
}

// processCPUNS is the whole process's user+system CPU time (rusage), 0 if unavailable.
func processCPUNS() int64 {
	var ru syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &ru) != nil {
		return 0
	}
	return ru.Utime.Nano() + ru.Stime.Nano()
}
