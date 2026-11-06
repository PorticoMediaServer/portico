package stateownership

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

type testFileLocker func(*os.File) error

func (f testFileLocker) Lock(file *os.File) error { return f(file) }

func nativeLock(t *testing.T, path string) *Lock {
	t.Helper()
	l, err := Acquire(path)
	if errors.Is(err, ErrUnsupported) {
		t.Skipf("native state storage lacks advisory locking: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := l.Close(); err != nil {
			t.Error(err)
		}
	})
	return l
}

func requireHeld(t *testing.T, path string) {
	t.Helper()
	l, err := Acquire(path)
	if l != nil {
		_ = l.Close()
	}
	if l != nil || !errors.Is(err, ErrHeld) {
		t.Fatalf("native lock was released early: lock=%v error=%v", l, err)
	}
}

func TestStateOwnershipRetainsPhysicalBorrowersDuringClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.lock")
	l := nativeLock(t, path)
	first, err := l.Borrow()
	if err != nil {
		t.Fatal(err)
	}
	second, err := l.Borrow()
	if err != nil {
		t.Fatal(err)
	}
	// Consumer aliases cannot create or release another physical holding.
	copyOfFirst := *first
	if got, err := first.LockPath(); err != nil || got != path {
		t.Fatal(got, err)
	}
	if err := l.Close(); !errors.Is(err, ErrBusy) {
		t.Fatal("close lost physical custody", err)
	}
	if v, err := l.Borrow(); v != nil || !errors.Is(err, ErrClosing) {
		t.Fatal("closing owner minted ingress", v, err)
	}
	if err := second.Check(); !errors.Is(err, ErrClosing) {
		t.Fatal("closing owner did not revoke ingress", err)
	}
	if v, err := second.LockPath(); v != "" || !errors.Is(err, ErrClosing) {
		t.Fatal("closing owner returned scope", v, err)
	}
	requireHeld(t, path)
	if err := copyOfFirst.Close(); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	requireHeld(t, path) // copy release MUST NOT decrement the second holding
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	next := nativeLock(t, path)
	if err := next.Close(); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("ownership must not delete the durable lock pathname", err)
	}
}

func TestStateOwnershipRefusesZeroAndCopiedIssuers(t *testing.T) {
	for _, l := range []*Lock{nil, {}} {
		if value, err := l.Borrow(); value != nil || !errors.Is(err, ErrInvalid) {
			t.Fatal(value, err)
		}
	}
	for _, v := range []*Lease{nil, {}} {
		if !errors.Is(v.Check(), ErrInvalid) {
			t.Fatal("zero lease authorized ingress")
		}
		if path, err := v.LockPath(); path != "" || !errors.Is(err, ErrInvalid) {
			t.Fatal(path, err)
		}
	}
	path := filepath.Join(t.TempDir(), "server.lock")
	l := nativeLock(t, path)
	copyOfIssuer := *l
	if value, err := copyOfIssuer.Borrow(); value != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal(value, err)
	}
	if err := copyOfIssuer.Close(); !errors.Is(err, ErrInvalid) {
		t.Fatal("copied issuer released actual lock", err)
	}
	requireHeld(t, path)
	v, err := l.Borrow()
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Close(); err != nil {
		t.Fatal(err)
	}
	if err := v.Check(); !errors.Is(err, ErrClosing) {
		t.Fatal("released lease reauthorized", err)
	}
	requireHeld(t, path) // releasing a borrow does not close its live root
}

func TestStateOwnershipRejectsUnavailableAndSilentLocking(t *testing.T) {
	for _, silent := range []bool{false, true} {
		t.Run(fmt.Sprint(silent), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "server.lock")
			calls := 0
			l, err := acquire(path, testFileLocker(func(*os.File) error {
				calls++
				if silent {
					return nil
				}
				return ErrUnsupported
			}))
			if l != nil || !errors.Is(err, ErrUnsupported) {
				t.Fatal(l, err)
			}
			want := 1
			if silent {
				want = 2
			}
			if calls != want {
				t.Fatal(calls, want)
			}
			nativeLock(t, path) // failure closed both descriptions
		})
	}
}

func TestStateOwnershipConcurrentLeaseCopiesReleaseOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.lock")
	l := nativeLock(t, path)
	first, err := l.Borrow()
	if err != nil {
		t.Fatal(err)
	}
	last, err := l.Borrow()
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	var joined sync.WaitGroup
	for range 32 {
		copyOfFirst := *first
		joined.Go(func() {
			if err := copyOfFirst.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	joined.Wait()
	requireHeld(t, path)
	if err := last.Close(); err != nil {
		t.Fatal(err)
	}
	nativeLock(t, path)
}

func TestStateOwnershipDetectsLockPathReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.lock")
	l := nativeLock(t, path)
	v, err := l.Borrow()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = v.Close() })
	if err := os.Rename(path, path+".old"); err != nil {
		t.Skipf("platform refuses to rename locked native handle: %v", err)
	}
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := v.Check(); !errors.Is(err, ErrChanged) {
		t.Fatal("replaced path retained ingress authority", err)
	}
	if value, err := v.LockPath(); value != "" || err == nil {
		t.Fatal("replaced path returned authority", value, err)
	}
	if value, err := l.Borrow(); value != nil || err == nil {
		t.Fatal("replaced path minted authority", value, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".old", path); err != nil {
		t.Fatal(err)
	}
	if err := v.Check(); !errors.Is(err, ErrChanged) {
		t.Fatal("restored pathname resurrected authority", err)
	}
}

func TestStateOwnershipProcessExitReleasesNativeOwnership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.lock")
	// Start a genuine separate process; killing it must release the native lock,
	// not leave a PID/tombstone that a restart needs to clean up.
	cmd := exec.Command(os.Args[0], "-test.run=^TestStateOwnershipChild$")
	cmd.Env = append(os.Environ(), "PORTICO_TEST_STATE_LOCK_CHILD="+path)
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	joined := false
	t.Cleanup(func() {
		_ = in.Close()
		if !joined {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil || line != "owned\n" {
		t.Fatal("child did not establish native ownership", line, err)
	}
	requireHeld(t, path)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("child unexpectedly exited cleanly before kill")
	}
	joined = true
	nativeLock(t, path)
}

func TestStateOwnershipChild(t *testing.T) {
	path := os.Getenv("PORTICO_TEST_STATE_LOCK_CHILD")
	if path == "" {
		return
	}
	l, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	fmt.Fprintln(os.Stdout, "owned")
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func TestStateOwnershipOneStoreClaimAndRevokedLifetime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.lock")
	l := nativeLock(t, path)
	store, err := l.ClaimStore()
	if err != nil {
		t.Fatal(err)
	}
	if v, err := l.ClaimStore(); v != nil || !errors.Is(err, ErrBusy) {
		t.Fatal("two live store openings", v, err)
	}
	borrow, err := l.Borrow()
	if err != nil {
		t.Fatal(err)
	}
	if err := borrow.Close(); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(borrow.Lifetime().Err(), context.Canceled) || store.Lifetime().Err() != nil {
		t.Fatal("borrow release did not isolate its lifetime")
	}
	if v, err := l.ClaimStore(); v != nil || !errors.Is(err, ErrBusy) {
		t.Fatal("unrelated borrower released store claim", v, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if store.Lifetime().Err() == nil {
		t.Fatal("released store lifetime survived")
	}
	store, err = l.ClaimStore()
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := l.Borrow()
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	if !errors.Is(store.Lifetime().Err(), context.Canceled) || !errors.Is(sibling.Lifetime().Err(), context.Canceled) {
		t.Fatal("closing root did not revoke all borrowed lifetimes")
	}
	requireHeld(t, path)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	requireHeld(t, path)
	if err := sibling.Close(); err != nil {
		t.Fatal(err)
	}
	nativeLock(t, path)
}
