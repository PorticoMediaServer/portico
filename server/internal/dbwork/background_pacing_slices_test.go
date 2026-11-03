package dbwork

import (
	"context"
	"testing"
	"time"
)

// Under foreground pressure a loop of millisecond quanta used to pause after
// every one, each pause costing more than the quantum. A sliced loop pauses
// once per paceSliceMinimum of work, and the pause stays proportional to it.
func TestShortQuantaArePacedAsOneSlice(t *testing.T) {
	RegisterForegroundProbe(t.Name(), func() bool { return true })
	defer RegisterForegroundProbe(t.Name(), nil)
	pace := func(ctx context.Context, quanta int) uint64 {
		before := yields.Load()
		for range quanta {
			if !PaceBackground(ctx, time.Now().Add(-time.Millisecond)) {
				t.Fatal("pacing refused a live context")
			}
		}
		return yields.Load() - before
	}
	if paused := pace(context.Background(), 10); paused != 10 {
		t.Fatalf("an unsliced loop paused %d times for 10 quanta, want 10", paused)
	}
	sliced := WithPaceSlices(context.Background())
	if paused := pace(sliced, 4); paused != 0 {
		t.Fatalf("a sliced loop paused after %d of 4 one-millisecond quanta", paused)
	}
	if paused := pace(sliced, 1); paused != 1 {
		t.Fatalf("a sliced loop paused %d times at 5 ms of work, want once", paused)
	}
	if paused := pace(sliced, 4); paused != 0 {
		t.Fatalf("a sliced loop paused %d times in a new slice's first 4 ms", paused)
	}
	cancelled, cancel := context.WithCancel(sliced)
	cancel()
	if PaceBackground(cancelled, time.Now()) {
		t.Fatal("a cancelled sliced loop was told to continue")
	}
}
