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
