package dbwork

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestGateClassStatsSeparateHoldersQueueCancellationAndReset(t *testing.T) {
	g := NewGate()
	release, err := g.Acquire(context.Background(), ClassMaintenance)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Millisecond)
	snapshot := g.Stats()
	maintenance := snapshot.ByClass[ClassMaintenance.String()]
	if !maintenance.Active || maintenance.CurrentHeldMilli < 2 || maintenance.Acquired != 1 || maintenance.HeldMilli != 0 {
		t.Fatalf("active maintenance hold missing: %+v", maintenance)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancelled := make(chan error, 1)
	go func() { _, err := g.Acquire(ctx, ClassBackgroundMedia); cancelled <- err }()
	waitForWaiters(t, g, 1)
	cancel()
	if err := <-cancelled; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	completed := make(chan struct{})
	go func() {
		free, err := g.Acquire(context.Background(), ClassSecurityFence)
		if err != nil {
			t.Error(err)
			close(completed)
			return
		}
		time.Sleep(2 * time.Millisecond)
		free()
		free()
		close(completed)
	}()
	waitForWaiters(t, g, 1)
	release()
	release()
	<-completed
	snapshot = g.Stats()
	maintenance = snapshot.ByClass[ClassMaintenance.String()]
	fence := snapshot.ByClass[ClassSecurityFence.String()]
	background := snapshot.ByClass[ClassBackgroundMedia.String()]
	if maintenance.Active || maintenance.HeldMilli < 3 || maintenance.MaxHeldMilli != maintenance.HeldMilli || fence.Active || fence.Acquired != 1 || fence.Queued != 1 || fence.HeldMilli < 2 || background.Cancelled != 1 || background.Acquired != 0 {
		t.Fatalf("class accounting mismatch: %+v", snapshot.ByClass)
	}
	if snapshot.Acquired != maintenance.Acquired+fence.Acquired || snapshot.Cancelled != background.Cancelled {
		t.Fatal("class counts do not match aggregate counters")
	}
	g.ResetPeak()
	if g.Stats().ByClass[ClassMaintenance.String()].MaxHeldMilli != 0 || g.Stats().ByClass[ClassSecurityFence.String()].MaxHeldMilli != 0 {
		t.Fatal("measurement window inherited an old class hold peak")
	}
}
