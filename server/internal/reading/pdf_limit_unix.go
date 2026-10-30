//go:build linux

package reading

import (
	"runtime/debug"

	"golang.org/x/sys/unix"
)

// limitPDFProcess is the helper's own hard budget, applied before any parser
// code runs. RLIMIT_AS contains allocation/decompression explosions in this
// child; RLIMIT_CPU contains parser CPU work. The parent still kills the process
// group on its deadline and waits for physical retirement.
func limitPDFProcess() error {
	zero := unix.Rlimit{}
	if err := unix.Setrlimit(unix.RLIMIT_CORE, &zero); err != nil {
		return err
	}
	memory := uint64(1 << 30)
	if err := unix.Setrlimit(unix.RLIMIT_AS, &unix.Rlimit{Cur: memory, Max: memory}); err != nil {
		return err
	}
	cpu := uint64(30)
	if err := unix.Setrlimit(unix.RLIMIT_CPU, &unix.Rlimit{Cur: cpu, Max: cpu + 5}); err != nil {
		return err
	}
	debug.SetMemoryLimit(192 << 20)
	return nil
}
