package operations

import (
	"context"
	"database/sql"
	"sync/atomic"
	"testing"
	"time"
)

func TestConsoleJobAsyncStepDoesNotBlockControlAndDrainsOnShutdown(t *testing.T) {
	store, p, auth := consoleFixture(t)
	scheduler := NewScheduler(store)
	started := make(chan struct{})
	var calls atomic.Int32
	if e := scheduler.Register(Adapter{Kind: "async-test", Lane: "background-media", Resource: LaneBackground, AsyncStep: true, ValidateTx: func(context.Context, *sql.Tx, string) error { return nil }, Step: func(ctx context.Context, _ string) (JobObservation, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-ctx.Done()
		return JobObservation{}, ctx.Err()
	}}); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	j, e := scheduler.Enqueue(ctx, p, auth, RunJob{Kind: "async-test", Resource: "test", IdempotencyKey: "async-start"})
	if e != nil {
		t.Fatal(e)
	}
	stopped := make(chan struct{})
	go func() { defer close(stopped); scheduler.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(5 * time.Second):
			t.Error("scheduler left work running")
		}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("async step not started")
	}
	// Re-observation of the same queued job must not create another producer.
	scheduler.advance(ctx, j)
	if calls.Load() != 1 {
		t.Fatal("duplicated step", calls.Load())
	}
	if !scheduler.laneSet().holds(j.ID) {
		t.Fatal("released lane while step active")
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not drain")
	}
	if scheduler.activeJobs() != 0 || scheduler.laneSet().holds(j.ID) {
		t.Fatal("work/slot left after shutdown")
	}
}
