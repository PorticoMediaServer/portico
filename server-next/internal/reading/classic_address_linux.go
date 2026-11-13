package reading

import (
	"os"
	"strconv"
	"strings"
)

func archiveAddressSpace() (uint64, error) {
	raw, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return 0, errDamaged
	}
	pages, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil || pages > 1<<40 {
		return 0, errDamaged
	}
	return pages * uint64(os.Getpagesize()), nil
}
