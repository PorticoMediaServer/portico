//go:build linux

package telemetry

import "syscall"

// memoryTranscodeDirectory names the same preferred tmpfs mount as startup.
// A writable candidate can still be unavailable: playback checks its kernel
// ceiling, the configured cap and the host/cgroup memory floor separately.
func memoryTranscodeDirectory() string {
	return memoryTranscodeCandidate(func(path string) bool {
		var fs syscall.Statfs_t
		return syscall.Statfs(path, &fs) == nil && fs.Type == 0x01021994 && writable(path)
	})
}

func memoryTranscodeCandidate(usable func(string) bool) string {
	for _, path := range []string{"/memory-transcode", "/dev/shm"} {
		if usable(path) {
			return path
		}
	}
	return ""
}
