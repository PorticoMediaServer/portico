package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func registrationFixture(t *testing.T) (*Client, string) {
	t.Helper()
	binary, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	c := New(binary)
	c.Timeout = time.Second
	root := t.TempDir()
	if e = os.WriteFile(filepath.Join(root, "tiny"), []byte("small"), 0600); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { waitReaderExit(t, c) })
	return c, root
}
func TestRootRegistrationRealBorrowAndLifetime(t *testing.T) {
	c, root := registrationFixture(t)
	owner, cancel := context.WithCancel(context.Background())
	defer cancel()
	setup, stop := context.WithCancel(context.Background())
	registration, e := c.RegisterRoot(setup, root, "", "", owner)
	if e != nil {
		t.Fatal(e)
	}
	defer registration.Close()
	stop()
	lease, e := registration.Borrow("tiny")
	if e != nil {
		t.Fatal(e)
	}
	reader, e := c.OpenObservedPlayback(context.Background(), lease)
	if e != nil {
		t.Fatal(e)
	}
	defer reader.Close()
	b, e := reader.ReadExtent(context.Background(), 0, 5)
	if e != nil || string(b) != "small" {
		t.Fatal(string(b), e)
	}
	cancel()
	if _, e = registration.Borrow("tiny"); !errors.Is(e, ErrRootLease) {
		t.Fatal("owner cancellation admitted borrow", e)
	}
	if b, e = reader.ReadExtent(context.Background(), 0, 1); e == nil || len(b) != 0 {
		t.Fatal("owner cancellation returned bytes", e)
	}
}
func TestRootRegistrationClosePreservesBorrowDescriptor(t *testing.T) {
	c, root := registrationFixture(t)
	owner, cancel := context.WithCancel(context.Background())
	defer cancel()
	registration, e := c.RegisterRoot(context.Background(), root, "", "", owner)
	if e != nil {
		t.Fatal(e)
	}
	lease, e := registration.Borrow("tiny")
	if e != nil {
		t.Fatal(e)
	}
	defer lease.Close()
	if e = registration.Close(); e != nil {
		t.Fatal(e)
	}
	// Inspect only a private test descriptor: base Close must not reuse/close the
	// actual borrowed handle still owned by a source helper.
	if info, e := lease.Directory.Stat(); e != nil || !info.IsDir() {
		t.Fatal("borrow prematurely closed", e)
	}
	if lease.Lifetime.Err() == nil {
		t.Fatal("closed registration authority alive")
	}
	lease.Close()
	lease.Close()
	if _, e = lease.Directory.Stat(); e == nil {
		t.Fatal("borrow release did not close")
	}
}
func TestRootRegistrationRejectsInvalidAndCanceled(t *testing.T) {
	c, root := registrationFixture(t)
	owner, cancel := context.WithCancel(context.Background())
	cancel()
	if r, e := c.RegisterRoot(context.Background(), root, "", "", owner); e == nil || r != nil {
		t.Fatal("canceled owner registered")
	}
	if r, e := c.RegisterRoot(context.Background(), filepath.Join(root, "tiny"), "", "", context.Background()); e == nil || r != nil {
		t.Fatal("file registered as directory")
	}
	if r, e := c.RegisterRoot(context.Background(), root, root, "expected", context.Background()); e == nil || r != nil {
		t.Fatal("ordinary directory accepted as managed mount")
	}
	waitReaderExit(t, c)
}

func TestRootRegistrationFailureDoesNotWaitForTimeout(t *testing.T) {
	c, root := registrationFixture(t)
	c.Timeout = 5 * time.Second
	for _, path := range []string{filepath.Join(root, "missing"), filepath.Join(root, "tiny")} {
		registration, err := c.RegisterRoot(context.Background(), path, "", "", context.Background())
		if registration != nil || err == nil || errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("invalid root did not report its registration failure: %v %v", registration, err)
		}
	}
}
