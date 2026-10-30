//go:build !linux && !darwin && !windows

package telemetry

// VolumeUsage has no portable implementation on this platform, so callers
// report the figure as unavailable rather than as zero.
func VolumeUsage(string) (int64, int64, bool) { return 0, 0, false }
