package dbwork

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func checkpointScheduleFixture(t *testing.T) (*sql.DB, *CheckpointSchedule) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "catalog with spaces.sqlite")
	db, err := OpenHandle(path, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err = ExecWrite(context.Background(), db, ClassInteractive, `CREATE TABLE schedule_sample(id INTEGER); INSERT INTO schedule_sample VALUES(1)`); err != nil {
		t.Fatal(err)
	}
	s, err := NewCheckpointSchedule(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(s.walPath) != filepath.Clean(path+"-wal") {
		t.Fatalf("scheduler did not resolve native filename: %q", s.walPath)
	}
	return db, s
}

func TestCheckpointSchedulePollsPressureWithoutSQL(t *testing.T) {
	_, s := checkpointScheduleFixture(t)
	ctx, measured := Measure(context.Background())
	for range 100 {
		if result, ran := s.Tick(ctx); ran {
			t.Fatalf("ordinary pressure poll ran SQL checkpoint: %+v", result)
		}
	}
	if cost := measured(); cost.Statements != 0 || cost.Transactions != 0 || cost.Acquisitions != 0 {
		t.Fatalf("one-second stat polls performed SQL: %+v", cost)
	}
	if CheckpointPollInterval != time.Second || CheckpointRegularInterval != 30*time.Second {
		t.Fatal("production polling envelope changed")
	}
	// The native database path is operational state, never public diagnostics.
	raw, err := json.Marshal(s)
	if err != nil || string(raw) != "{}" {
		t.Fatalf("schedule exposed private state: %s %v", raw, err)
	}
}

func TestCheckpointScheduleIdleCompletionAndNewCommit(t *testing.T) {
	db, s := checkpointScheduleFixture(t)
	s.lastAttempt = time.Now().Add(-CheckpointRegularInterval)
	if result, ran := s.Tick(context.Background()); !ran || !CheckpointSettled(result) || !s.settled {
		t.Fatalf("regular checkpoint did not complete: %+v %t", result, ran)
	}
	s.lastAttempt = time.Now().Add(-CheckpointRegularInterval)
	s.nextAttempt = time.Time{}
	ctx, measured := Measure(context.Background())
	for range 100 {
		if result, ran := s.Tick(ctx); ran {
			t.Fatalf("idle settled database repeated checkpoint: %+v", result)
		}
	}
	if cost := measured(); cost.Statements != 0 || cost.Acquisitions != 0 {
		t.Fatalf("idle schedule performed SQL: %+v", cost)
	}
	if _, err := ExecWrite(context.Background(), db, ClassInteractive, `INSERT INTO schedule_sample VALUES(2)`); err != nil {
		t.Fatal(err)
	}
	if result, ran := s.Tick(ctx); !ran || !s.settled || result.Err != nil {
		t.Fatalf("new committed work did not resume regular care: %+v %t", result, ran)
	}
}

func TestCheckpointScheduleLookupCancellationRetainsRegularCare(t *testing.T) {
	db := readerFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s, err := NewCheckpointSchedule(ctx, db)
	if s == nil || !errors.Is(err, context.Canceled) || s.resolved {
		t.Fatalf("cancelled lookup lost usable scheduler: %p %v", s, err)
	}
	s.lastResolve = time.Now().Add(-CheckpointRegularInterval)
	s.lastAttempt = time.Now().Add(-CheckpointRegularInterval)
	if result, ran := s.Tick(context.Background()); !ran || result.Err != nil || !s.resolved {
		t.Fatalf("regular care did not recover filename lookup: %+v %t", result, ran)
	}
	before := s.lastAttempt
	if _, ran := s.Tick(ctx); ran || s.lastAttempt != before {
		t.Fatal("cancelled poll began work or changed scheduling")
	}
}

func TestCheckpointSchedulePathLookupHonorsPoolWaitCancellation(t *testing.T) {
	db := readerFixture(t)
	ctx := WithClass(context.Background(), ClassMaintenance)
	// Checkpoint control must not wait for the single background connection:
	// it can be occupied by the long read that hard recovery must cancel.
	// Saturate its actual foreground pool to exercise the same bounded wait.
	pool := db
	limit := pool.Stats().MaxOpenConnections
	if limit <= 0 {
		t.Fatal("checkpoint control pool has no connection bound")
	}
	var held []*sql.Conn
	defer func() {
		for _, conn := range held {
			_ = conn.Close()
		}
	}()
	for i := 0; i < limit; i++ {
		conn, err := pool.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, conn)
	}
	limited, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel()
	start := time.Now()
	s, err := NewCheckpointSchedule(limited, db)
	if s == nil || !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatalf("path lookup ignored bounded pool wait: %p %v", s, err)
	}
}

func schedulePressureFile(t *testing.T) string {
	t.Helper()
	// This sparse stand-in belongs only to scheduler failure tests; it never
	// replaces or mutates SQLite's live WAL. Native retained-WAL convergence is
	// independently covered with real frames in reader_pressure_test.go.
	path := filepath.Join(t.TempDir(), "allocation-probe")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = file.Truncate(CheckpointHardFileBytes); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCheckpointSchedulePressureIgnoresSettledButBacksOffOnFailure(t *testing.T) {
	db, s := checkpointScheduleFixture(t)
	s.settled, s.settledAt = true, ChangingCommits()
	s.walPath = schedulePressureFile(t)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if result, ran := s.Tick(context.Background()); !ran || result.Err == nil || s.settled {
		t.Fatalf("large allocation incorrectly settled or skipped: %+v %t", result, ran)
	}
	if remaining := time.Until(s.nextAttempt); remaining <= 0 || remaining > time.Second {
		t.Fatalf("pressure failure cooldown: %s", remaining)
	}
	for range 100 {
		if result, ran := s.Tick(context.Background()); ran {
			t.Fatalf("unchanged failed pressure hammered checkpoint: %+v", result)
		}
	}
	for want := 2; want <= 8; want *= 2 {
		s.nextAttempt = time.Time{}
		if result, ran := s.Tick(context.Background()); !ran || result.Err == nil {
			t.Fatalf("pressure retry disappeared: %+v %t", result, ran)
		}
		remaining := time.Until(s.nextAttempt)
		if remaining <= time.Duration(want-1)*time.Second || remaining > time.Duration(want)*time.Second {
			t.Fatalf("failure cooldown%d: %s", want, remaining)
		}
	}
}

func TestCheckpointScheduleCopiedCountsCannotSettleUnshrunkAllocation(t *testing.T) {
	_, s := checkpointScheduleFixture(t)
	s.walPath = schedulePressureFile(t)
	result, ran := s.Tick(context.Background())
	if !ran || !CheckpointSettled(result) || s.settled {
		t.Fatalf("completion without allocation reclamation suppressed retry: %+v %t settled%t", result, ran, s.settled)
	}
	if _, ran = s.Tick(context.Background()); ran {
		t.Fatal("unshrunk allocation retried without cooldown")
	}
}

func TestCheckpointReservationPressureUsesReservedRecoveryBelowPhysicalLimit(t *testing.T) {
	db, s := checkpointScheduleFixture(t)
	l := ledgerFor(db)
	l.mu.Lock()
	l.stats.AdmissionBytes = walSoftBytes - 4096
	l.mu.Unlock()
	r := &walRequest{ledger: l, bytes: 32768, qualified: true, class: ClassBackgroundMedia}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		release, e := WriteGate().Acquire(context.WithValue(ctx, walRequestKey{}, r), ClassBackgroundMedia)
		if e == nil {
			release()
		}
		result <- e
	}()
	deadline := time.Now().Add(time.Second)
	for !l.reservationPressure() {
		if time.Now().After(deadline) {
			t.Fatal("no pressure from unready reservation")
		}
		time.Sleep(time.Millisecond)
	}
	if bytes, _ := checkpointFileBytes(s.walPath); bytes >= CheckpointHardFileBytes {
		t.Fatal("fixture already physically hard", bytes)
	}
	out, ran := s.Tick(ctx)
	if !ran || out.Err != nil || l.snapshot().Resets == 0 {
		t.Fatalf("reservation pressure did not use reset: ran%v %+v ledger%+v", ran, out, l.snapshot())
	}
	if e := <-result; e != nil {
		t.Fatal("reset did not release retained writer", e)
	}
	if l.reservationPressure() {
		t.Fatal("verified reset retained pressure")
	}
}
