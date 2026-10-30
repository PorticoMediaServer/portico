package dbwork

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestColdReadPriorityRealReaderLeases(t *testing.T) {
	db := readerFixture(t)
	waitReaderCondition(t, func() bool { return !ColdReadShouldYield() })
	for _, class := range []Class{ClassInteractive, ClassPlaybackStart, ClassBackgroundMedia, ClassMaintenance} {
		ctx := WithClass(context.Background(), class)
		for _, snapshot := range []bool{false, true} {
			var closeRead func()
			if snapshot {
				tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				var n int
				if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM sample`).Scan(&n); err != nil {
					t.Fatal(err)
				}
				closeRead = func() { _ = tx.Rollback() }
			} else {
				rows, err := db.QueryContext(ctx, `SELECT id FROM sample`)
				if err != nil {
					t.Fatal(err)
				}
				closeRead = func() { _ = rows.Close() }
			}
			if got, want := ColdReadShouldYield(), class != ClassMaintenance; got != want {
				closeRead()
				t.Fatalf("class=%v snapshot=%v yield=%v want=%v", class, snapshot, got, want)
			}
			closeRead()
			waitReaderCondition(t, func() bool { return !ColdReadShouldYield() })
		}
	}
}

func TestColdReadPriorityQueuedReaderAndCancellation(t *testing.T) {
	db := readerFixture(t)
	scope := readerScopeForTest(t, db)
	waitReaderCondition(t, func() bool { return !ColdReadShouldYield() })
	reopen, _, backoff := scope.pause()
	if reopen == nil || backoff != 0 {
		t.Fatal("could not pause reader admission")
	}
	defer reopen(true)
	ctx, cancel := context.WithCancel(WithClass(context.Background(), ClassInteractive))
	defer cancel()
	result := make(chan error, 1)
	go func() {
		rows, err := db.QueryContext(ctx, `SELECT id FROM sample`)
		if rows != nil {
			_ = rows.Close()
		}
		result <- err
	}()
	waitReaderCondition(t, func() bool { return scope.stats().AdmissionWaiting == 1 })
	if !ColdReadShouldYield() {
		t.Fatal("queued interactive reader was invisible")
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	waitReaderCondition(t, func() bool { return !ColdReadShouldYield() })
}

func TestColdReadPriorityRealWriterAndQueuedWriter(t *testing.T) {
	db := readerFixture(t)
	waitReaderCondition(t, func() bool { return !ColdReadShouldYield() })
	w, err := Begin(context.Background(), db, ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Rollback()
	if _, err = w.Tx().Exec(`INSERT INTO sample VALUES(3)`); err != nil {
		t.Fatal(err)
	}
	if !ColdReadShouldYield() {
		t.Fatal("active interactive writer was invisible")
	}
	if err = w.Rollback(); err != nil {
		t.Fatal(err)
	}
	waitReaderCondition(t, func() bool { return !ColdReadShouldYield() })

	maintenance, err := Begin(context.Background(), db, ClassMaintenance)
	if err != nil {
		t.Fatal(err)
	}
	defer maintenance.Rollback()
	if ColdReadShouldYield() {
		t.Fatal("maintenance writer counted as higher priority")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		queued, err := Begin(ctx, db, ClassInteractive)
		if queued != nil {
			_ = queued.Rollback()
		}
		result <- err
	}()
	waitReaderCondition(t, func() bool { return WriteGate().Admission().UserQueued > 0 })
	if !ColdReadShouldYield() {
		t.Fatal("queued interactive writer was invisible")
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err = maintenance.Rollback(); err != nil {
		t.Fatal(err)
	}
	waitReaderCondition(t, func() bool { return !ColdReadShouldYield() })
}

func TestColdReadPriorityHTTPProbeAndContendedGate(t *testing.T) {
	RegisterForegroundProbe("cold-read-test", func() bool { return true })
	if !ColdReadShouldYield() {
		t.Fatal("foreground HTTP work was invisible")
	}
	RegisterForegroundProbe("cold-read-test", nil)
	t.Cleanup(func() { RegisterForegroundProbe("cold-read-test", nil) })
	pressureMu.Lock()
	yield := ColdReadShouldYield()
	pressureMu.Unlock()
	if !yield {
		t.Fatal("contended HTTP probe registration should conservatively yield")
	}
	g := WriteGate()
	g.mu.Lock()
	// Must return immediately even with the writer bookkeeping mutex occupied.
	yield = ColdReadShouldYield()
	g.mu.Unlock()
	if !yield {
		t.Fatal("contended gate should conservatively yield")
	}
	waitReaderCondition(t, func() bool { return !ColdReadShouldYield() })
}
