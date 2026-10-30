//go:build linux || darwin

package telemetry

import "syscall"

// VolumeUsage reports the bytes an unprivileged process may still write to the
// volume holding path, and the volume's total size.
func VolumeUsage(path string) (free, total int64, ok bool) {
	var stat syscall.Statfs_t
	if path == "" || syscall.Statfs(path, &stat) != nil {
		return 0, 0, false
	}
	return int64(stat.Bavail) * int64(stat.Bsize), int64(stat.Blocks) * int64(stat.Bsize), true
}
