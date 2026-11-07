package writer

import (
	"context"
	"errors"
	"testing"
)

func TestScanIssuerBusyDoesNotReadMutableReleaseState(t *testing.T) {
	f := scanIssuerRuntime(t)
	d := f.driver
	op, err := d.PrepareCreate(t.Context(), ScanRunInput{7, "lifecycle-library", "lifecycle-new-source", "first-import"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := op.Apply(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// Model Close's private transition while it owns the mutex. A concurrent
	// caller must fail TryLock before inspecting that mutable preparation state.
	ended, cancel := context.WithCancel(t.Context())
	cancel()
	d.mu.Lock()
	oldLife := op.preparation.lifetime
	op.preparation.lifetime, op.closed = ended, true
	calls := []struct {
		name string
		call func() error
	}{
		{"apply", func() error {
			value, err := op.Apply(t.Context())
			if value != nil {
				t.Error("busy Apply returned a result")
			}
			return err
		}},
		{"resolve", func() error {
			value, err := op.Resolve(t.Context())
			if value != nil {
				t.Error("busy Resolve returned a result")
			}
			return err
		}},
		{"snapshot", func() error { _, err := result.Snapshot(t.Context()); return err }},
		{"ack", func() error { _, err := op.Ack(t.Context()); return err }},
		{"lease", func() error {
			value, err := op.Lease(t.Context())
			if value != nil {
				t.Error("busy Lease returned authority")
			}
			return err
		}},
	}
	for _, c := range calls {
		err := c.call()
		if !errors.Is(err, ErrBusy) || errors.Is(err, context.Canceled) || errors.Is(err, ErrInvalid) {
			t.Errorf("%s inspected unowned mutable state: %v", c.name, err)
		}
	}
	op.preparation.lifetime, op.closed = oldLife, false
	d.mu.Unlock()
	if err = result.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = op.Ack(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = op.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}
