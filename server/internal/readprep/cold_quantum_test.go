package readprep

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

func TestColdQuantumSharesStoreLaneAndChargedDelivery(t *testing.T) {
	store := storeFor(t, Config{Workers: 2})
	handle := submit(t, store, "command", Key{Namespace: "action-preview", Basis: "b", Canonical: "accepted"}, simplePlan("ready"))
	lease := ready(t, store, "command", handle)
	if err := lease.ResizeDelivery(4096); err != nil {
		t.Fatal(err)
	}
	release, acquired, err := store.TryColdQuantum(lease)
	if err != nil || !acquired {
		t.Fatal(acquired, err)
	}
	defer release()
	if _, acquired, err = store.TryColdQuantum(lease); err != nil || acquired {
		t.Fatalf("second cold owner %v %v", acquired, err)
	}
	var cold atomic.Bool
	plan := simplePlan("cold")
	plan.ColdSearch = true
	factory := plan.New
	plan.New = func(ctx context.Context, b *Budget) (Computation, error) { cold.Store(true); return factory(ctx, b) }
	coldHandle := submit(t, store, "cold", Key{Namespace: "search", Basis: "b", Canonical: "waiting"}, plan)
	ordinary := submit(t, store, "ordinary", Key{Namespace: "ordinary", Basis: "b", Canonical: "bypass"}, simplePlan("ordinary"))
	ready(t, store, "ordinary", ordinary).Release()
	if cold.Load() {
		t.Fatal("cold computation bypassed confirmed capture owner")
	}
	release()
	release()
	ready(t, store, "cold", coldHandle).Release()
	if !cold.Load() {
		t.Fatal("released lane did not resume cold preparation")
	}
	lease.Release()
	if _, _, err = store.TryColdQuantum(lease); !errors.Is(err, ErrStale) {
		t.Fatal(err)
	}
}
func TestColdQuantumReleaseAfterStoreClose(t *testing.T) {
	store := storeFor(t, Config{})
	handle := submit(t, store, "caller", testKey, simplePlan("lease"))
	lease := ready(t, store, "caller", handle)
	release, ok, err := store.TryColdQuantum(lease)
	if err != nil || !ok {
		t.Fatal(err)
	}
	store.Close()
	release()
	lease.Release()
	if _, _, err = store.TryColdQuantum(lease); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}
