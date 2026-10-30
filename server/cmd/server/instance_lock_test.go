package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestInstanceLockRefusesASecondHolderAndReleasesOnClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.lock")
	first, err := acquireInstanceLock(path)
	if errors.Is(err, errInstanceLockUnsupported) {
		t.Skipf("this filesystem does not support advisory locking: %v", err)
	}
	if err != nil {
		t.Fatalf("acquiring the first instance lock failed: %v", err)
	}
	// A second acquisition in the same process exercises the same flock/LockFileEx
	// call a second process would make: both hold their own open file description.
	second, err := acquireInstanceLock(path)
	if err == nil {
		second.Close()
		first.Close()
		t.Fatal("two instances locked one state directory")
	}
	if !errors.Is(err, errInstanceLockHeld) {
		first.Close()
		t.Fatalf("a held lock reported %v rather than errInstanceLockHeld", err)
	}
	if err = first.Close(); err != nil {
		t.Fatalf("releasing the lock failed: %v", err)
	}
	// Releasing must actually release: an owner who restarts the server must not
	// be told their own dead process still owns the directory.
	third, err := acquireInstanceLock(path)
	if err != nil {
		t.Fatalf("the lock was not released on close: %v", err)
	}
	third.Close()
	// The lock file itself is left behind deliberately; unlike a pid file, a
	// leftover file is not evidence of anything and never blocks a later start.
	if _, err = os.Stat(path); err != nil {
		t.Fatalf("the lock file should survive: %v", err)
	}
}

func TestInstanceLockRejectsUnavailableAndSilentLocking(t *testing.T) {
	for _, silent := range []bool{false, true} {
		t.Run(map[bool]string{false: "unavailable", true: "silent success"}[silent], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "server.lock")
			calls := 0
			lock, err := acquireInstanceLockWith(path, func(*os.File) error {
				calls++
				if silent {
					return nil
				}
				return errInstanceLockUnsupported
			})
			if lock != nil || !errors.Is(err, errInstanceLockUnsupported) {
				t.Fatalf("unsupported lock accepted: lock=%v error=%v", lock, err)
			}
			want := 1
			if silent {
				want = 2
			}
			if calls != want {
				t.Fatalf("lock used %d file descriptions, want %d", calls, want)
			}
			// Refusal closes both descriptions; an ordinary later acquisition
			// must not inherit a stale exclusive lock from the failed attempt.
			actual, err := acquireInstanceLock(path)
			if errors.Is(err, errInstanceLockUnsupported) {
				t.Skipf("real filesystem lacks locking: %v", err)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := actual.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
