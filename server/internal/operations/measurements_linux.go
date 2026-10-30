//go:build linux

package operations

import (
	"os"
	"strconv"
	"strings"
)

func processIO() map[string]Fact {
	out := map[string]Fact{"processReadBytes": unavailable("Process I/O counters unavailable."), "processWriteBytes": unavailable("Process I/O counters unavailable.")}
	b, e := os.ReadFile("/proc/self/io")
	if e != nil || len(b) > 4096 {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}
		key := ""
		switch parts[0] {
		case "read_bytes:":
			key = "processReadBytes"
		case "write_bytes:":
			key = "processWriteBytes"
		}
		if key != "" {
			if n, e := strconv.ParseUint(parts[1], 10, 64); e == nil {
				out[key] = measured(n, "storage bytes since startup")
			}
		}
	}
	return out
}
