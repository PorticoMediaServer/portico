//go:build darwin

package hostlimits

import (
	"strconv"
	"strings"
	"syscall"
)

// macOS reports a hard RLIMIT_NOFILE of "unlimited" but refuses any soft limit
// above kern.maxfilesperproc, so the straightforward "raise soft to hard" fails
// outright and leaves the process at 256. This reads the real ceiling.
func platformFileCeiling() (uint64, bool) {
	raw, err := syscall.Sysctl("kern.maxfilesperproc")
	if err != nil {
		return 0, false
	}
	value, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
	if err != nil || value == 0 {
		return 0, false
	}
	return value, true
}

func rlimitValue(v uint64) uint64 { return v }
