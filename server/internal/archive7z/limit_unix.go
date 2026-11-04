//go:build linux || darwin

package archive7z

import (
	"errors"

	"golang.org/x/sys/unix"
)

// workerAddressBudget is the address space the worker may add to what it has
// already reserved.
const workerAddressBudget = 1 << 30

// limitProcess bounds the worker's own address space, CPU, file growth and core
// dumps before any archive is read; a platform where these cannot be set refuses
// to parse rather than running unbounded.
//
// The Go runtime reserves address space before main runs (about 1.2 GiB with
// go1.27 on linux/amd64, before a page is used). A flat 1 GiB RLIMIT_AS set
// after that refused every later mapping, so the worker died on its first
// sizeable allocation (OPS, 2 Nov). The limit is what is already reserved plus
// the worker's 1 GiB budget.
func limitProcess() error {
	return limitProcessWith(workerAddressLimit, unix.Setrlimit)
}

// Measurement and all limit installations must succeed before parsing starts.
func limitProcessWith(addressLimit func() (uint64, error), install func(int, *unix.Rlimit) error) error {
	address, err := addressLimit()
	if err != nil {
		return err
	}
	limits := []struct {
		resource int
		value    unix.Rlimit
	}{
		{unix.RLIMIT_AS, unix.Rlimit{Cur: address, Max: address}},
		{unix.RLIMIT_CPU, unix.Rlimit{Cur: 60, Max: 60}},
		{unix.RLIMIT_FSIZE, unix.Rlimit{Cur: MaxSource, Max: MaxSource}},
		{unix.RLIMIT_CORE, unix.Rlimit{Cur: 0, Max: 0}},
	}
	for _, limit := range limits {
		if err := install(limit.resource, &limit.value); err != nil {
			return errors.New("resource limits unavailable")
		}
	}
	return nil
}
