package storagepaths

import "golang.org/x/sys/unix"

// getfsstat includes volumes mounted below a directory, including same-device
// mount points that comparing st_dev during a walk would miss.
func cleanupMounts() ([]Mount, error) {
	for attempt := 0; attempt < 3; attempt++ {
		count, err := unix.Getfsstat(nil, unix.MNT_NOWAIT)
		if err != nil {
			return nil, err
		}
		stats := make([]unix.Statfs_t, count+16)
		count, err = unix.Getfsstat(stats, unix.MNT_NOWAIT)
		if err != nil {
			return nil, err
		}
		if count >= len(stats) {
			continue
		}
		out := make([]Mount, 0, count)
		for _, stat := range stats[:count] {
			out = append(out, Mount{unix.ByteSliceToString(stat.Mntonname[:]), unix.ByteSliceToString(stat.Fstypename[:])})
		}
		return out, nil
	}
	return nil, ErrCleanupUnavailable
}
