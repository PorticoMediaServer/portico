package archive7z

import (
	"os"
	"strconv"
	"strings"
)

func workerAddressLimit() (uint64, error) {
	return uint64(workerAddressBudget) + reservedAddressSpace(), nil
}

// reservedAddressSpace is this process's current virtual size (VmSize), or zero
// where /proc is not available.
func reservedAddressSpace() uint64 {
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(status), "\n") {
		if rest, ok := strings.CutPrefix(line, "VmSize:"); ok {
			kb, err := strconv.ParseUint(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest), "kB")), 10, 64)
			if err != nil {
				return 0
			}
			return kb << 10
		}
	}
	return 0
}
