//go:build !linux

package operations

func processIO() map[string]Fact {
	return map[string]Fact{"processReadBytes": unavailable("Process I/O counters are unavailable on this platform."), "processWriteBytes": unavailable("Process I/O counters are unavailable on this platform.")}
}
