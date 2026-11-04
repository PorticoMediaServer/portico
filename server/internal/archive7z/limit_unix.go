//go:build linux || darwin

package archive7z

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"

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
	baseline := reservedAddressSpace()
	if runtime.GOOS == "darwin" {
		var err error
		baseline, err = darwinVMSize()
		if err != nil || baseline == 0 || baseline > ^uint64(0)-workerAddressBudget {
			return errors.New("native VM measurement unavailable")
		}
	}
	address := uint64(workerAddressBudget) + baseline
	fmt.Fprintf(os.Stderr, "baseline=%d allowance=%d address=%d\n", baseline, workerAddressBudget, address)
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
		var before unix.Rlimit
		getErr := unix.Getrlimit(limit.resource, &before)
		fmt.Fprintf(os.Stderr, "rlimit resource=%d want=%+v before=%+v geterr=%v\n", limit.resource, limit.value, before, getErr)
		if err := unix.Setrlimit(limit.resource, &limit.value); err != nil {
			fmt.Fprintf(os.Stderr, "setrlimit resource=%d errno=%v\n", limit.resource, err)
			return errors.New("resource limits unavailable")
		}
	}
	return nil
}

// reservedAddressSpace is this process's current virtual size (VmSize), or zero
// where /proc is not available.
func reservedAddressSpace() uint64 {
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(status), "\n") {
		if rest, ok := strings.CutPrefix(line, "VmSize:"); ok {
			kb, err := strconv.ParseUint(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest), "kB")), 10, 64)
			if err != nil {
				return 0
			}
			return kb << 10
		}
	}
	return 0
}
