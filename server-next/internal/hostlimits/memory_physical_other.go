//go:build !linux && !windows && !darwin

package hostlimits

func physicalMemoryBytes() (uint64, bool) { return 0, false }
