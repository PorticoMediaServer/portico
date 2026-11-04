package playback

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestFirstHLSRelocationCancellationRetainsPhysicalRun(t *testing.T) {
	f := newRestartRetirementFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	f.spawn(func() { done <- f.h.startGeneration(ctx, f.id, 0, 3) })
	f.wait(f.ctx.Done()) // The exact old run was retired and cancelled.
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("relocation cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("first relocation ignored caller cancellation while physical exit was withheld")
	}
	if !f.h.occurrences.ownsRun(f.id, f.entry, f.run) {
		t.Fatal("caller cancellation released the old physical run")
	}
	f.h.mu.Lock()
	registered := f.h.windows[f.id] == f.window && f.window.relocating
	_, active := f.h.active[f.id]
	f.h.mu.Unlock()
	if !registered || !active {
		t.Fatal("cancelled waiter discarded or replaced the retiring window")
	}
	f.assertPlaying()
	// A later waiter can also cancel; neither waiter may launch a successor.
	later, laterCancel := context.WithCancel(context.Background())
	laterCancel()
	if err := f.h.startGeneration(later, f.id, 0, 3); !errors.Is(err, context.Canceled) {
		t.Fatalf("later cancellation: %v", err)
	}
	if !f.h.occurrences.ownsRun(f.id, f.entry, f.run) {
		t.Fatal("later cancellation released physical ownership")
	}
	f.stop()
	f.finish()
	if f.h.occurrences.ownsRun(f.id, f.entry, f.run) {
		t.Fatal("physically completed run remains registered")
	}
}
