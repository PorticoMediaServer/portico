//go:build linux || darwin

package operations

import (
	"fmt"
	"os"
	"syscall"
)

func processCPU() Fact {
	var r syscall.Rusage
	if e := syscall.Getrusage(syscall.RUSAGE_SELF, &r); e != nil {
		return unavailable("Process CPU measurement failed.")
	}
	seconds := float64(r.Utime.Sec+r.Stime.Sec) + float64(r.Utime.Usec+r.Stime.Usec)/1e6
	return measured(seconds, "CPU seconds since startup")
}
func volumeAvailable(path string) Fact {
	_, free, err := mediaVolume(path)
	if err != nil {
		return unavailable("State volume could not be measured.")
	}
	return measured(free, "bytes")
}

func mediaVolume(path string) (string, uint64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", 0, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", 0, fmt.Errorf("volume identity unavailable")
	}
	var filesystem syscall.Statfs_t
	if err = syscall.Statfs(path, &filesystem); err != nil {
		return "", 0, err
	}
	return fmt.Sprintf("device-%x", stat.Dev), uint64(filesystem.Bavail) * uint64(filesystem.Bsize), nil
}
