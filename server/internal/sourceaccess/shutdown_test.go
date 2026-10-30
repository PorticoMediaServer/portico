package sourceaccess

import (
	"context"
	"testing"
	"time"
)

func TestRegistryShutdownWaitsOnlyOwnedPhysicalDebt(t *testing.T) {
	r, a, _ := fixture(t)
	lease, err := r.Borrow(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := r.storage.OpenObservedPlayback(context.Background(), lease)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	// A distinct registry deliberately shares the same physical supervisor.
	other := New(r.storage, nil)
	a.OwnerID = "independent-runtime"
	otherLease, err := other.Borrow(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	otherReader, err := other.storage.OpenObservedPlayback(context.Background(), otherLease)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		otherReader.Close()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := other.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	deadline, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = r.Shutdown(deadline); err != nil {
		t.Fatal(err)
	}
	if r.operations.Active() != 0 {
		t.Fatal("shutdown acknowledged active owned helper")
	}
	if other.operations.Active() == 0 || r.storage.Supervisor.Active() == 0 {
		t.Fatal("shutdown changed unrelated runtime sharing supervisor")
	}
	data, err := otherReader.ReadExtent(context.Background(), 0, 5)
	if err != nil || string(data) != "small" {
		t.Fatal("other runtime lost source", err)
	}
}
