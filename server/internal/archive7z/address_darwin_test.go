package archive7z

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"testing"
	"unsafe"
)

func TestDarwinLimitedWorkerRefusesOverBudget(t *testing.T) {
	if os.Getenv("ARCHIVE7Z_AS_CHILD") == "1" {
		if err := limitProcess(); err != nil {
			t.Fatal(err)
		}
		small, err := unix.Mmap(-1, 0, 64<<20, unix.PROT_NONE, unix.MAP_PRIVATE|unix.MAP_ANON)
		if err != nil {
			t.Fatalf("within budget: %v", err)
		}
		if err = unix.Munmap(small); err != nil {
			t.Fatal(err)
		}
		large, err := unix.Mmap(-1, 0, 2<<30, unix.PROT_NONE, unix.MAP_PRIVATE|unix.MAP_ANON)
		if err == nil {
			unix.Munmap(large)
			t.Fatal("over-budget mapping succeeded")
		}
		if !errors.Is(err, unix.ENOMEM) {
			t.Fatalf("wrong refusal: %v", err)
		}
		t.Log("over-budget mapping refused with ENOMEM")
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestDarwinLimitedWorkerRefusesOverBudget$", "-test.v")
	cmd.Env = append(os.Environ(), "ARCHIVE7Z_AS_CHILD=1")
	out, err := cmd.CombinedOutput()
	t.Logf("child: %s", out)
	if err != nil {
		t.Fatal(err)
	}
}

// An unavailable or malformed native observation must never reach Setrlimit or
// parsing. Overflow includes Darwin's signed RLIM_INFINITY ceiling.
func TestDarwinWorkerAddressMeasurementFailsClosed(t *testing.T) {
	for name, tc := range map[string]struct {
		size uint64
		n    uintptr
		err  error
	}{
		"unavailable":     {n: 96, err: unix.EPERM},
		"zero":            {n: 96},
		"truncated":       {size: 1 << 30, n: 8},
		"oversized":       {size: 1 << 30, n: 104},
		"overflow":        {size: ^uint64(0), n: 96},
		"signed-overflow": {size: 1<<63 - 1, n: 96},
	} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			err := limitProcessWith(func() (uint64, error) {
				return darwinAddressLimit(func(info *darwinTaskInfo) (uintptr, error) { info.VirtualSize = tc.size; return tc.n, tc.err })
			}, func(int, *unix.Rlimit) error { calls++; return nil })
			if err == nil || calls != 0 {
				t.Fatalf("invalid measurement reached install: err=%v calls=%d", err, calls)
			}
		})
	}
	limit, err := darwinAddressLimit(func(info *darwinTaskInfo) (uintptr, error) {
		info.VirtualSize = 400 << 30
		return unsafe.Sizeof(*info), nil
	})
	if err != nil || limit != 401<<30 {
		t.Fatalf("baseline+budget: %d %v", limit, err)
	}
}

func TestDarwinWorkerLimitInstallationFailsClosed(t *testing.T) {
	for failAt := 0; failAt < 4; failAt++ {
		calls := 0
		err := limitProcessWith(func() (uint64, error) { return 2 << 30, nil }, func(int, *unix.Rlimit) error {
			calls++
			if calls == failAt+1 {
				return unix.EPERM
			}
			return nil
		})
		if err == nil || calls != failAt+1 {
			t.Fatalf("install failure %d: err=%v calls=%d", failAt, err, calls)
		}
	}
}
