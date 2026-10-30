package playback

import (
	"context"
	"testing"
)

func TestAudioOccurrenceLateRefundAndReleaseCannotClearSuccessor(t *testing.T) {
	s := &Service{prefetchOwned: make(map[string]*prefetchRecord)}
	old := &prefetchRecord{service: s, id: "same", generation: 1, used: 80, readers: 1}
	successor := &prefetchRecord{service: s, id: "same", generation: 2, used: 37, readers: 1}
	ctx, cancel := context.WithCancel(context.Background())
	a := &AudioOccurrence{ctx: ctx, cancel: cancel, service: s, record: old, done: make(chan struct{}), entry: &occurrenceEntry{}, run: &occurrenceRun{}}
	s.prefetchOwned[old.id] = old
	old.retire()
	s.prefetchOwned[old.id] = successor
	a.Refund(80)
	a.Close()
	a.Close()
	if s.prefetchOwned["same"] != successor || successor.used != 37 || successor.readers != 1 {
		t.Fatal("late old cleanup changed successor budget")
	}
	if a.Take(10, 100) != 0 || a.Charge(1, 100) {
		t.Fatal("retired reader regranted old budget")
	}
}

func TestOccurrenceSlotLateFinalizerKeepsSuccessor(t *testing.T) {
	h := &HLS{active: map[string]context.CancelFunc{}}
	old, next := &occurrenceRun{}, &occurrenceRun{}
	h.installOccurrenceLocked("same", old, func() {})
	h.installOccurrenceLocked("same", next, func() {})
	h.removeOccurrenceLocked("same", old)
	if h.activeRuns["same"] != next || h.active["same"] == nil {
		t.Fatal("old finalizer erased successor")
	}
	h.removeOccurrenceLocked("same", next)
	if h.active["same"] != nil {
		t.Fatal("owner did not release its own slot")
	}
}
