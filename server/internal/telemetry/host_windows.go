//go:build windows

package telemetry

import (
	"context"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

// Windows host facts come from kernel32 through the standard library's lazy DLL
// loader: no cgo, no elevated access, and no reporter process for CPU or memory.

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	getSystemTimes   = kernel32.NewProc("GetSystemTimes")
	globalMemoryEx   = kernel32.NewProc("GlobalMemoryStatusEx")
	getDiskFreeSpace = kernel32.NewProc("GetDiskFreeSpaceExW")
)

func fileTime(v syscall.Filetime) uint64 {
	return uint64(v.HighDateTime)<<32 | uint64(v.LowDateTime)
}

func sampleCPU(ctx context.Context, previous *cpuCounter) Metric {
	var idle, kernel, user syscall.Filetime
	ok, _, _ := getSystemTimes.Call(uintptr(unsafe.Pointer(&idle)), uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user)))
	if ok == 0 {
		return Unavailable("Windows did not report system processor times.")
	}
	// Kernel time includes idle time, so total is kernel plus user and busy is
	// that total minus idle.
	total := fileTime(kernel) + fileTime(user)
	if total == 0 {
		return Unavailable("Windows reported empty processor times.")
	}
	return deltaCPU(previous, total-fileTime(idle), total)
}

type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

func sampleMemory(ctx context.Context) (Metric, int64, int64) {
	status := memoryStatusEx{}
	status.Length = uint32(unsafe.Sizeof(status))
	ok, _, _ := globalMemoryEx.Call(uintptr(unsafe.Pointer(&status)))
	if ok == 0 || status.TotalPhys == 0 || status.AvailPhys > status.TotalPhys {
		return Unavailable("Windows did not report physical memory."), 0, 0
	}
	total := int64(status.TotalPhys)
	used := total - int64(status.AvailPhys)
	return Available(percent(float64(used) / float64(total) * 100)), used, total
}

// Windows exposes per-volume I/O only through performance counters, whose
// collection costs far more than this sampler's budget allows.
func readDiskCounter(context.Context, string) (counter, string) {
	return counter{}, "Windows does not expose per-volume read and write counters within this sampler's cost budget."
}

// netstat -e prints the adapter byte totals since boot in two columns, which is
// exactly the cumulative pair a rate needs.
func readNetCounter(ctx context.Context) (counter, string) {
	raw, e := runCommand(ctx, "netstat", "-e")
	if e != nil {
		return counter{}, "Windows did not expose interface byte counters."
	}
	for _, line := range strings.Split(raw, "\n") {
		parts := fields(line)
		if len(parts) != 3 || !strings.EqualFold(parts[0], "Bytes") {
			continue
		}
		received, e1 := strconv.ParseUint(parts[1], 10, 64)
		sent, e2 := strconv.ParseUint(parts[2], 10, 64)
		if e1 != nil || e2 != nil {
			continue
		}
		return counter{read: received, write: sent, known: true}, ""
	}
	return counter{}, "Windows reported no interface byte totals."
}
