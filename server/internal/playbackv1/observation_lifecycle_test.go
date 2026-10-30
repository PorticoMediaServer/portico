package playbackv1

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestObservationFlushBeforeBackupWritesNotYetDueSamplesOnce(t *testing.T) {
	f := newObservationFixture(t)
	ids := f.cloneSessions(observationBatch + 1)
	epochs := make(map[string]string)
	for _, id := range ids {
		epochs[id] = f.claim(id, "")
	}
	f.clock.add(time.Second)
	for _, id := range ids {
		if _, err := f.tick(id, epochs[id], 1, "playing"); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := f.service.FlushObservations(ctx); err != nil {
		t.Fatal(err)
	}
	o := f.service.observations()
	o.mu.Lock()
	for _, id := range ids {
		if e := o.entries[id]; e == nil || e.dirty || e.busy || e.applied != e.credited() {
			t.Errorf("pending sample after flush: %s", id)
		}
	}
	o.mu.Unlock()
	before := f.proved.total()
	if err := f.service.FlushObservations(ctx); err != nil {
		t.Fatal(err)
	}
	if f.proved.total() != before {
		t.Fatal("flush applied the same credit twice")
	}
	if err := f.service.CloseObservations(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestObservationCloseJoinsWorkerAndFlushesLatestWindow(t *testing.T) {
	f := newObservationFixture(t)
	id := f.session.ID
	epoch := f.claim(id, "")
	f.clock.add(time.Second)
	if _, err := f.tick(id, epoch, 1, "playing"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := f.service.CloseObservations(ctx); err != nil {
		t.Fatal(err)
	}
	o := f.service.observations()
	o.mu.Lock()
	running, dirty := o.running, o.entries[id].dirty
	o.mu.Unlock()
	if running || dirty {
		t.Fatal("close left a worker or pending sample")
	}
	if _, err := f.tick(id, epoch, 2, "playing"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := f.service.ClaimObserver(ctx, f.caller, id, ObserverClaim{Generation: f.generation}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := f.service.CloseObservations(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestObservationFlushCancellationKeepsPendingSample(t *testing.T) {
	f := newObservationFixture(t)
	id := f.session.ID
	epoch := f.claim(id, "")
	f.clock.add(time.Second)
	if _, err := f.tick(id, epoch, 1, "playing"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := f.service.FlushObservations(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	o := f.service.observations()
	o.mu.Lock()
	dirty := o.entries[id].dirty
	o.mu.Unlock()
	if !dirty {
		t.Fatal("canceled flush discarded pending sample")
	}
	if err := f.service.CloseObservations(context.Background()); err != nil {
		t.Fatal(err)
	}
}
