package dbwork

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSnapshotLifetimesAreCappedAndTheLongPermitIsSingle(t *testing.T) {
	db := readerFixture(t)
	ctx := context.Background()
	check := func(ctx context.Context, want time.Duration) {
		// The deadline is set when the snapshot opens: allow the time since.
		t.Helper()
		if err := WithReadSnapshot(ctx, db, func(inner context.Context) error {
			deadline, ok := inner.Deadline()
			if !ok {
				t.Fatal("snapshot without a deadline")
			}
			if left := time.Until(deadline); left > want || left < want-200*time.Millisecond {
				t.Fatalf("snapshot lifetime %s, want about %s", left, want)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	check(ctx, ForegroundSnapshotLifetime*snapshotScale)
	check(WithClass(ctx, ClassBackgroundMedia), BackgroundSnapshotLifetime*snapshotScale)
	// A caller with a longer deadline still gets the cap; a shorter one keeps it.
	long, cancel := context.WithTimeout(ctx, time.Hour)
	defer cancel()
	check(long, ForegroundSnapshotLifetime*snapshotScale)
	short, cancelShort := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancelShort()
	check(short, 300*time.Millisecond)
	permitted, release, err := AcquireLongRead(ctx, LongReadIntegrity)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = AcquireLongRead(ctx, LongReadRepair); !errors.Is(err, ErrLongReadBusy) {
		t.Fatalf("a second whole-database read: %v", err)
	}
	check(permitted, LongReadLifetime)
	release()
	release()
	if _, _, err = AcquireLongRead(ctx, LongRead("export")); err == nil {
		t.Fatal("an operation outside the list took the long permit")
	}
	again, releaseAgain, err := AcquireLongRead(ctx, LongReadRestoreStaging)
	if err != nil || again == nil {
		t.Fatalf("permit not released: %v", err)
	}
	releaseAgain()
	if report := Integrity(ctx, db); report.Error != "" || report.QuickCheck != "ok" {
		t.Fatalf("integrity on the long permit: %+v", report)
	}
}

// Claude's condition on the owner-long permit: a ten-minute snapshot pins the
// WAL, so a hard-pressure recovery ends it with the retryable ErrReadPressure,
// and the log is reset afterwards.
func TestHardPressureCancelsTheLongReadAndShrinksTheWAL(t *testing.T) {
	db := readerFixture(t)
	ctx := context.Background()
	write := func() {
		t.Helper()
		if _, err := ExecWrite(ctx, db, ClassInteractive, `INSERT INTO sample SELECT value FROM json_each('[1,2,3,4,5,6,7,8]')`); err != nil {
			t.Fatal(err)
		}
	}
	write()
	long, release, err := AcquireLongRead(ctx, LongReadRestoreStaging)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	started := make(chan struct{})
	ended := make(chan error, 1)
	go func() {
		ended <- WithReadSnapshot(long, db, func(snapshot context.Context) error {
			var n int
			if err := QueryRow(snapshot, db, `SELECT count(*) FROM sample`).Scan(&n); err != nil {
				return err
			}
			close(started)
			<-snapshot.Done()
			return QueryRow(snapshot, db, `SELECT count(*) FROM sample`).Scan(&n)
		})
	}()
	<-started
	write()
	if walFileBytes(ctx, db) == 0 {
		t.Fatal("fixture has no WAL to shrink")
	}
	result := checkpoint(ctx, db, schedulePressureFile(t))
	if err := <-ended; !errors.Is(err, ErrReadPressure) {
		t.Fatalf("the long read ended with %v, want the retryable read-pressure refusal", err)
	}
	if result.Err != nil || result.Outcome != "truncated" {
		t.Fatalf("hard pressure did not reset past the long read: %+v", result)
	}
	if size := walFileBytes(ctx, db); size != 0 {
		t.Fatalf("WAL kept %d bytes after the reset", size)
	}
}

// The lifetime starts when the snapshot opens: waiting for a snapshot permit
// is bounded by the caller's deadline, not by the snapshot's own cap.
func TestSnapshotLifetimeStartsAfterAdmission(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db := readerFixture(t)
	ctx := context.Background()
	// Hold every foreground permit, then free one after longer than the cap.
	held := 0
	for ; held < cap(foregroundSnapshotPermits); held++ {
		foregroundSnapshotPermits <- struct{}{}
	}
	defer func() {
		for ; held > 0; held-- {
			<-foregroundSnapshotPermits
		}
	}()
	go func() {
		time.Sleep(ForegroundSnapshotLifetime*snapshotScale + 200*time.Millisecond)
		<-foregroundSnapshotPermits
	}()
	waitCtx, cancel := context.WithTimeout(ctx, ForegroundSnapshotLifetime*snapshotScale*3)
	defer cancel()
	err := WithReadSnapshot(waitCtx, db, func(snapshot context.Context) error {
		var n int
		return QueryRow(snapshot, db, `SELECT count(*) FROM sample`).Scan(&n)
	})
	held--
	if err != nil {
		t.Fatalf("a snapshot that waited for admission lost its lifetime: %v", err)
	}
}
