package networking

import (
	"testing"
	"time"
)

// What setupflow reads about applying an approval: running while something tries,
// with its failed tries and next try; running while a wait is still open for the
// decision; not tracked once everything stopped, which setup reads as exhausted.
func TestSetupDeliveryReportsWhatIsStillTrying(t *testing.T) {
	h := &ClaimHandler{recoveryWake: make(chan struct{}, 1)}
	if _, ok := h.SetupDelivery("op"); ok {
		t.Fatal("an untracked claim reported delivery")
	}

	open := &webClaimWatch{done: make(chan struct{})}
	h.webWatches.Store("op", open)
	if d, ok := h.SetupDelivery("op"); !ok || !d.Running {
		t.Fatalf("an open wait: %+v %v", d, ok)
	}
	// Try again does nothing while the wait is still open.
	h.RetrySetupDelivery("op")
	if _, ok := h.webWatches.Load("op"); !ok {
		t.Fatal("Try again dropped a wait that was still open")
	}
	select {
	case <-h.recoveryWake:
		t.Fatal("Try again woke recovery while a wait was open")
	default:
	}

	d := h.trackDelivery("op")
	first := d.since
	next := time.Now().Add(4 * time.Second)
	d.failed(next)
	d.failed(next)
	got, ok := h.SetupDelivery("op")
	if !ok || !got.Running || got.Attempts != 2 || !got.Since.Equal(first) || got.NextRetryAt.UnixMilli() != next.UnixMilli() {
		t.Fatalf("tracked delivery: %+v %v", got, ok)
	}
	// A later approval of the same claim keeps the first approval's time.
	if again := h.trackDelivery("op"); !again.since.Equal(first) || again.nextAt.Load() != 0 {
		t.Fatal("re-tracking reset when the approval arrived")
	}

	// Everything stopped.
	d.running.Store(false)
	close(open.done)
	if got, ok = h.SetupDelivery("op"); !ok || got.Running || !got.NextRetryAt.IsZero() {
		t.Fatalf("stopped delivery: %+v %v", got, ok)
	}
	h.deliveries.Delete("op")
	if _, ok = h.SetupDelivery("op"); ok {
		t.Fatal("a finished wait with no tracker reported delivery")
	}

	// Try again drops the finished wait and wakes recovery to wait again.
	h.RetrySetupDelivery("op")
	if _, ok = h.webWatches.Load("op"); ok {
		t.Fatal("the finished wait was kept")
	}
	select {
	case <-h.recoveryWake:
	default:
		t.Fatal("Try again did not wake recovery")
	}
	var none *ClaimHandler
	none.RetrySetupDelivery("op")
	if _, ok = none.SetupDelivery("op"); ok {
		t.Fatal("a server without networking reported delivery")
	}
}
