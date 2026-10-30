package sourceaccess

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "--portico-storage-helper" {
		if storage.Helper(os.Stdin, os.Stdout) != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func fixture(t *testing.T) (*Registry, Authority, context.CancelFunc) {
	t.Helper()
	binary, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	c := storage.New(binary)
	c.Timeout = time.Second
	root := t.TempDir()
	if e = os.WriteFile(filepath.Join(root, "tiny"), []byte("small"), 0600); e != nil {
		t.Fatal(e)
	}
	life, cancel := context.WithCancel(context.Background())
	registry := New(c, nil)
	t.Cleanup(func() {
		cancel()
		registry.Close()
		deadline := time.Now().Add(2 * time.Second)
		for c.Supervisor.Active() > 0 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if c.Supervisor.Active() != 0 {
			t.Fatal("helper not physically drained")
		}
	})
	return registry, Authority{OwnerID: "attempt-one", RootID: "root", RootPath: root, RelativePath: "tiny", Revision: "incarnation:1", Lifetime: life, Validate: func(ctx context.Context) error { return ctx.Err() }}, cancel
}
func TestRegistryRealRegistrationReuseAndInvalidation(t *testing.T) {
	r, a, cancel := fixture(t)
	first, e := r.Borrow(context.Background(), a)
	if e != nil {
		t.Fatal(e)
	}
	defer first.Close()
	second, e := r.Borrow(context.Background(), a)
	if e != nil {
		t.Fatal(e)
	}
	defer second.Close()
	if first.RegistrationID != second.RegistrationID || first.Directory.Fd() == second.Directory.Fd() {
		t.Fatal("not distinct borrows of retained registration")
	}
	cancel()
	if first.Lifetime.Err() == nil {
		t.Fatal("actual owner loss hidden by callback latency")
	}
	if l, e := r.Borrow(context.Background(), a); e == nil || l != nil {
		t.Fatal("invalidated authority resurrected")
	}
}
func TestRegistryPostAcquisitionSelectionRecheck(t *testing.T) {
	r, a, _ := fixture(t)
	calls := 0
	changed := errors.New("selected association changed")
	a.Validate = func(context.Context) error {
		calls++
		if calls > 1 {
			return changed
		}
		return nil
	}
	if lease, e := r.Borrow(context.Background(), a); !errors.Is(e, changed) || lease != nil {
		t.Fatal("changed selection lent descriptor", e)
	}
	r.mu.Lock()
	count := len(r.entries)
	r.mu.Unlock()
	if count != 0 {
		t.Fatal("rejected selection registered")
	}
}
func TestRegistryClosedAndRevisionReplacement(t *testing.T) {
	r, a, _ := fixture(t)
	first, e := r.Borrow(context.Background(), a)
	if e != nil {
		t.Fatal(e)
	}
	defer first.Close()
	a.Revision = "incarnation:2"
	second, e := r.Borrow(context.Background(), a)
	if e != nil {
		t.Fatal(e)
	}
	defer second.Close()
	if first.RegistrationID == second.RegistrationID {
		t.Fatal("new origin adopted old registration")
	}
	r.Close()
	if first.Lifetime.Err() == nil || second.Lifetime.Err() == nil {
		t.Fatal("shutdown did not cancel registrations")
	}
	if lease, e := r.Borrow(context.Background(), a); e == nil || lease != nil {
		t.Fatal("closed registry reopened")
	}
}

func TestRegistryIndependentOwnerLifetimes(t *testing.T) {
	r, a, cancel := fixture(t)
	first, e := r.Borrow(context.Background(), a)
	if e != nil {
		t.Fatal(e)
	}
	defer first.Close()
	secondLife, secondCancel := context.WithCancel(context.Background())
	defer secondCancel()
	a.OwnerID = "attempt-two"
	a.Lifetime = secondLife
	second, e := r.Borrow(context.Background(), a)
	if e != nil {
		t.Fatal(e)
	}
	defer second.Close()
	if first.RegistrationID == second.RegistrationID {
		t.Fatal("independent owners share physical registration lifetime")
	}
	cancel()
	if first.Lifetime.Err() == nil || second.Lifetime.Err() != nil {
		t.Fatal("first owner cancellation affected independent viewer")
	}
	again, e := r.Borrow(context.Background(), a)
	if e != nil {
		t.Fatal(e)
	}
	again.Close()
}
