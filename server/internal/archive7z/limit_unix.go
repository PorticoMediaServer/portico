//go:build linux || darwin

package archive7z

import (
	"errors"

	"golang.org/x/sys/unix"
)

// limitProcess bounds the worker's own address space, CPU, file growth and core
// dumps before any archive is read; a platform where these cannot be set refuses
// to parse rather than running unbounded.
func limitProcess() error {
	limits := []struct {
		resource int
		value    unix.Rlimit
	}{
		{unix.RLIMIT_AS, unix.Rlimit{Cur: 1 << 30, Max: 1 << 30}},
		{unix.RLIMIT_CPU, unix.Rlimit{Cur: 60, Max: 60}},
		{unix.RLIMIT_FSIZE, unix.Rlimit{Cur: MaxSource, Max: MaxSource}},
		{unix.RLIMIT_CORE, unix.Rlimit{Cur: 0, Max: 0}},
	}
	for _, limit := range limits {
		if err := unix.Setrlimit(limit.resource, &limit.value); err != nil {
			return errors.New("resource limits unavailable")
		}
	}
	return nil
}
