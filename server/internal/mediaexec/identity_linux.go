//go:build linux

package mediaexec

import (
	"os"
	"strconv"
	"strings"
)

type processIdentity struct{ start, boot string }

// identityOf is the process's start time in clock ticks since boot (field 22 of
// /proc/<pid>/stat) and the kernel's boot ID.
func identityOf(pid int) (processIdentity, error) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return processIdentity{}, err
	}
	text := string(raw)
	end := strings.LastIndexByte(text, ')')
	if end < 0 {
		return processIdentity{}, ErrInvalidJob
	}
	fields := strings.Fields(text[end+1:])
	// fields[0] is field 3 (state), so field 22 is fields[19].
	if len(fields) < 20 {
		return processIdentity{}, ErrInvalidJob
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return processIdentity{}, err
	}
	return processIdentity{start: fields[19], boot: strings.TrimSpace(string(boot))}, nil
}
