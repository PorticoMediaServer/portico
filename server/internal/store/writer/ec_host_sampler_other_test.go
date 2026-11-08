//go:build !linux

package writer

// The host sampler reads Linux /proc and cgroup files; other platforms only
// record read errors, and no device is resolved.
func deviceNumbers(string) (major, minor uint32, ok bool) { return 0, 0, false }
