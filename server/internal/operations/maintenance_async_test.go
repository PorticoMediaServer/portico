package operations

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// WJ6: a slow cleanup hook runs beside the control loop, holding its lane, so
// the tick that started it returns at once and the job still finishes.
func TestSlowMaintenanceDoesNotBlockTheControlLoop(t *testing.T) {
	store, owner, auth := consoleFixture(t)
	scheduler := NewScheduler(store)
	entered, release := make(chan struct{}), make(chan struct{})
	if err := scheduler.Register(Adapter{Kind: "slow-cleanup", Lane: "maintenance", ValidateTx: func(context.Context, *sql.Tx, string) error { return nil }, Maintenance: func(ctx context.Context, _ string) error {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil
	}}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	job, err := scheduler.Enqueue(ctx, owner, auth, RunJob{Kind: "slow-cleanup", IdempotencyKey: "slow"})
	if err != nil {
		t.Fatal(err)
	}
	ticked := make(chan struct{})
	go func() { scheduler.tick(ctx); close(ticked) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("maintenance did not start")
	}
	select {
	case <-ticked:
	case <-time.After(2 * time.Second):
		t.Fatal("the control loop waited for a running cleanup hook")
	}
	if scheduler.activeJobs() != 1 {
		t.Fatalf("active jobs %d while the hook runs", scheduler.activeJobs())
	}
	// A second tick while the hook runs must not start it again.
	scheduler.tick(ctx)
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		var state string
		if err := store.DB.QueryRow(`SELECT state FROM console_operations WHERE id=?`, job.ID).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if state == "succeeded" && scheduler.activeJobs() == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job state %s, active %d", state, scheduler.activeJobs())
		}
		time.Sleep(5 * time.Millisecond)
	}
}
