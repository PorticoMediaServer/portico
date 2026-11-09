//go:build windows

package hostlimits

// Windows has no per-process descriptor rlimit: handles are bounded by the
// kernel pool and by a per-process ceiling of 16,711,680, neither of which this
// server approaches. Reporting zero says "there is no limit to read" rather than
// inventing one, and there is nothing to raise.
func openFileLimits() (uint64, uint64) { return 0, 0 }

func raiseOpenFiles() (uint64, uint64, bool) { return 0, 0, false }
