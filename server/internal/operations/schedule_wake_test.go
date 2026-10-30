package operations

import (
	"context"
	"database/sql"
	"sync/atomic"
	"testing"
	"time"
)

func TestSchedulerStartsTenSecondScheduleOnTime(t *testing.T) {
	store, owner, auth := consoleFixture(t)
	var clock atomic.Int64
	clock.Store(time.Date(2026, 9, 23, 12, 0, 50, 0, time.UTC).UnixMilli())
	store.Now = func() time.Time { return time.UnixMilli(clock.Load()) }
	scheduler := NewScheduler(store)
	started := make(chan time.Time, 1)
	if err := scheduler.Register(Adapter{Kind: "test-due", Lane: "maintenance", ValidateTx: func(context.Context, *sql.Tx, string) error { return nil }, Maintenance: func(context.Context, string) error { started <- store.Now(); return nil }}); err != nil {
		t.Fatal(err)
	}
	due := store.Now().Add(10 * time.Second)
	_, err := scheduler.SaveSchedule(context.Background(), owner, auth, ScheduleChange{IdempotencyKey: "ten-seconds", Value: Schedule{ID: "ten-seconds", Kind: "test-due", Enabled: true, Timezone: "UTC", StartMinute: 12*60 + 1, WindowMinutes: 1}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	scheduler.tick(ctx)
	select {
	case <-started:
		t.Fatal("started early")
	default:
	}
	wait := scheduler.nextWake(ctx)
	if wait != 10*time.Second {
		t.Fatalf("timer reset to %s instead of 10s", wait)
	}
	clock.Add(wait.Milliseconds())
	scheduler.tick(ctx)
	select {
	case at := <-started:
		if at.Before(due) || at.Sub(due) > time.Second {
			t.Fatalf("started at %s, due %s", at, due)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("due job did not start")
	}
	deadline := time.Now().Add(5 * time.Second)
	for scheduler.activeJobs() > 0 {
		if time.Now().After(deadline) {
			t.Fatal("job did not stop")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestScheduleWakeTracksEditsAndDeferredJobs(t *testing.T) {
	store, owner, auth := consoleFixture(t)
	scheduler := NewScheduler(store)
	if err := scheduler.Register(Adapter{Kind: "test-due", Lane: "maintenance", ValidateTx: func(context.Context, *sql.Tx, string) error { return nil }, Maintenance: func(context.Context, string) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	value := Schedule{ID: "edit", Kind: "test-due", Enabled: true, Timezone: "UTC", StartMinute: 12*60 + 10, WindowMinutes: 1}
	schedule, err := scheduler.SaveSchedule(ctx, owner, auth, ScheduleChange{IdempotencyKey: "new", Value: value})
	if err != nil {
		t.Fatal(err)
	}
	if got := scheduler.nextWake(ctx); got != 10*time.Minute {
		t.Fatal(got)
	}
	value.StartMinute = 12*60 + 1
	if _, err = scheduler.SaveSchedule(ctx, owner, auth, ScheduleChange{ExpectedRevision: schedule.Revision, IdempotencyKey: "edit", Value: value}); err != nil {
		t.Fatal(err)
	}
	if got := scheduler.nextWake(ctx); got != time.Minute {
		t.Fatal(got)
	}
	job, err := scheduler.Enqueue(ctx, owner, auth, RunJob{Kind: "test-due", IdempotencyKey: "job"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.DB.Exec(`UPDATE console_operations SET next_ms=? WHERE id=?`, store.now()+10000, job.ID); err != nil {
		t.Fatal(err)
	}
	if got := scheduler.nextWake(ctx); got != 10*time.Second {
		t.Fatal(got)
	}
}

func TestNextScheduleDueUsesCalendarAndDST(t *testing.T) {
	for _, test := range []struct {
		now, want string
		start     int
		catchup   bool
	}{
		{"2026-03-08T01:59:50-05:00", "2026-03-08T03:00:00-04:00", 150, true},
		{"2026-11-01T01:59:50-04:00", "2026-11-01T01:30:00-05:00", 90, false},
	} {
		now, _ := time.Parse(time.RFC3339, test.now)
		want, _ := time.Parse(time.RFC3339, test.want)
		got := nextScheduleDue(Schedule{Enabled: true, Timezone: "America/New_York", StartMinute: test.start, WindowMinutes: 120, CatchUp: test.catchup}, now)
		if !got.Equal(want) {
			t.Fatalf("%s got %s want %s", test.now, got, want)
		}
	}
}
