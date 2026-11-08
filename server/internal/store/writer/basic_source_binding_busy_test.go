package writer

import (
	"errors"
	"testing"
	"time"
)

// The driver actor is taken with TryLock: a driver already owned by another
// caller must refuse immediately instead of waiting for it. Replacing it with
// Lock keeps every uncontended control green, so this holds the actor from a
// separate goroutine and requires the typed refusal, never a blocked call.
func TestBasicSourceBindingOwnedDriverRefusesInsteadOfWaiting(t *testing.T) {
	f, lease, last := basicSourceIssuerFixture(t)
	d := f.driver
	d.mu.Lock()
	done := make(chan error, 1)
	go func() {
		_, _, _, err := prepareBasicSource(t.Context(), d, lease, last, BasicAccept)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrBusy) {
			d.mu.Unlock()
			t.Fatalf("owned driver: %v", err)
		}
	case <-time.After(10 * time.Second):
		d.mu.Unlock()
		t.Fatal("owned driver waited instead of refusing")
	}
	d.mu.Unlock()
}
