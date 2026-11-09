//go:build !linux

package hostlimits

// macOS and Windows have no cgroup, so there is no ceiling to read and nothing
// to derive a soft heap limit from. Docker on either host runs the server inside
// a Linux virtual machine, where the Linux implementation applies.
func cgroupMemoryLimit() (uint64, bool) { return 0, false }
