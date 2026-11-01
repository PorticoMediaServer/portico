//go:build windows

package librarychannels

import "time"

// cpuTime has no getrusage on Windows; the CPU-cost measurement only runs
// where it does (PORTICO_LC_COST=1 on Linux or macOS).
func cpuTime() time.Duration { return 0 }
