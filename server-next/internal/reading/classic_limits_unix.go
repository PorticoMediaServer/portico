//go:build linux || darwin

package reading

import "golang.org/x/sys/unix"

func archiveLimits() error {
	reserved, err := archiveAddressSpace()
	if err != nil {
		return err
	}
	for _, limit := range []struct {
		kind int
		size uint64
	}{
		{unix.RLIMIT_AS, reserved + 1<<30}, {unix.RLIMIT_CPU, 60}, {unix.RLIMIT_FSIZE, maxArchiveSize}, {unix.RLIMIT_CORE, 0},
	} {
		if err := unix.Setrlimit(limit.kind, &unix.Rlimit{Cur: limit.size, Max: limit.size}); err != nil {
			return err
		}
	}
	return nil
}
