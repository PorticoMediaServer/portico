//go:build unix

package networking

import (
	"os"
	"syscall"
	"testing"
)

// TestRouteIdentityDescriptorExhaustion reproduces the production shape: with
// every descriptor below the soft limit in use, the proof fails instantly with
// a 503 while the database connection it already holds keeps working, the log
// names EMFILE, and the proof recovers as soon as descriptors are released.
func TestRouteIdentityDescriptorExhaustion(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newRouteIdentityFixture(t)
	f.expectSuccess(t)
	var saved syscall.Rlimit
	if e := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &saved); e != nil {
		t.Fatal(e)
	}
	probe, e := os.Open(os.DevNull)
	if e != nil {
		t.Fatal(e)
	}
	lowest := probe.Fd()
	probe.Close()
	lowered := saved
	lowered.Cur = uint64(lowest) + 64
	if lowered.Cur >= saved.Cur {
		t.Skip("soft descriptor limit already too low to lower safely")
	}
	if e = syscall.Setrlimit(syscall.RLIMIT_NOFILE, &lowered); e != nil {
		t.Skipf("cannot lower the descriptor limit: %v", e)
	}
	var held []*os.File
	release := func() {
		for _, h := range held {
			h.Close()
		}
		held = nil
	}
	t.Cleanup(func() {
		release()
		_ = syscall.Setrlimit(syscall.RLIMIT_NOFILE, &saved)
	})
	for {
		h, e := os.Open(os.DevNull)
		if e != nil {
			break
		}
		held = append(held, h)
		if len(held) > 4096 {
			t.Fatal("descriptor limit did not take effect")
		}
	}
	f.expectFailure(t, "EMFILE")
	release()
	if e = syscall.Setrlimit(syscall.RLIMIT_NOFILE, &saved); e != nil {
		t.Fatal(e)
	}
	f.expectSuccess(t)
}
