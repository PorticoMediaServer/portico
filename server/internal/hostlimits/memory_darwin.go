//go:build darwin

package hostlimits

import "golang.org/x/sys/unix"

func physicalMemoryBytes() (uint64, bool) {
	total, err := unix.SysctlUint64("hw.memsize")
	if err != nil || total == 0 || total > 1<<63-1 {
		return 0, false
	}
	return total, true
}
