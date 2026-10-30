package hosted

import (
	"context"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func eventRows(t *testing.T, f *claimFixture) map[string][3]int64 {
	t.Helper()
	rows, e := f.db.Query(`SELECT kind,revision,attempts,next_at FROM hosted_server_events`)
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	out := map[string][3]int64{}
	for rows.Next() {
		var kind string
		var revision, attempts, next int64
		if e = rows.Scan(&kind, &revision, &attempts, &next); e != nil {
			t.Fatal(e)
		}
		out[kind] = [3]int64{revision, attempts, next}
	}
	return out
}

// A41: with nothing queued, the outbox is nothing to do. It must not produce an
// error or a one-minute deadline that keeps the control loop spinning.
func TestAnEmptyEventOutboxIsNothingDue(t *testing.T) {
	f := newClaimFixture(t)
	var due time.Time
	var stepErr error
	if e := f.runner.Do(context.Background(), func(ctx context.Context) error {
		due, stepErr = f.service.sendServerEvents(ctx)
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if stepErr != nil || !due.IsZero() {
		t.Fatalf("empty outbox reported due=%v err=%v", due, stepErr)
	}
}

// A46: an event Hosted can't take now backs off on its own schedule, and rows
// left by an earlier claim are dropped instead of retried for ever.
func TestServerEventsBackOffAndDropStaleClaims(t *testing.T) {
	f := newClaimFixture(t)
	if e := f.service.QueueServerEvent(context.Background(), "storage_nearly_full"); e != nil {
		t.Fatal(e)
	}
	if _, e := f.db.Exec(`INSERT INTO hosted_server_events(operation_id,kind,payload,revision) VALUES('an_earlier_claim','rename','{"kind":"rename"}',1)`); e != nil {
		t.Fatal(e)
	}
	var due time.Time
	var stepErr error
	start := time.Now()
	if e := f.runner.Do(context.Background(), func(ctx context.Context) error {
		due, stepErr = f.service.sendServerEvents(ctx)
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if stepErr == nil {
		t.Fatal("the fixture's Hosted is unreachable; the send should fail")
	}
	rows := eventRows(t, f)
	if _, stale := rows["rename"]; stale || len(rows) != 1 {
		t.Fatalf("stale-claim event kept: %v", rows)
	}
	storage := rows["storage_nearly_full"]
	if storage[1] != 1 || time.Unix(storage[2], 0).Before(start.Add(59*time.Second)) {
		t.Fatalf("no backoff recorded: attempts=%d next=%v", storage[1], time.Unix(storage[2], 0))
	}
	if due.Before(start.Add(59 * time.Second)) {
		t.Fatalf("the loop was asked back after %v, not after the backoff", due.Sub(start))
	}
	// A second pass before the deadline sends nothing.
	if e := f.runner.Do(context.Background(), func(ctx context.Context) error {
		_, stepErr = f.service.sendServerEvents(ctx)
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if stepErr != nil || eventRows(t, f)["storage_nearly_full"][1] != 1 {
		t.Fatal("an event was retried before its backoff elapsed")
	}
	// A new change resets the backoff so it goes out promptly.
	if e := f.service.QueueServerEvent(context.Background(), "storage_nearly_full"); e != nil {
		t.Fatal(e)
	}
	if got := eventRows(t, f)["storage_nearly_full"]; got[0] != 2 || got[1] != 0 || got[2] != 0 {
		t.Fatalf("requeued event kept the old backoff: %v", got)
	}
}

// A61: a settings save queues a rename only when the name changed.
func TestOnlyANameChangeQueuesARename(t *testing.T) {
	f := newClaimFixture(t)
	ctx := context.Background()
	f.service.NotifySettingsChanged(ctx)
	first := eventRows(t, f)["rename"]
	f.service.NotifySettingsChanged(ctx)
	if again := eventRows(t, f)["rename"]; again[0] != first[0] {
		t.Fatalf("an unchanged name queued another rename: %v then %v", first, again)
	}
	if e := persistence.Set(f.db, "name", "Den"); e != nil {
		t.Fatal(e)
	}
	f.service.NotifySettingsChanged(ctx)
	if renamed := eventRows(t, f)["rename"]; renamed[0] != first[0]+1 {
		t.Fatalf("a real rename was not queued: %v then %v", first, renamed)
	}
}
