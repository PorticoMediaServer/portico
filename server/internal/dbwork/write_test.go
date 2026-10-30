package dbwork

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := OpenHandle(filepath.Join(t.TempDir(), "test.sqlite"), DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err = db.Exec(`CREATE TABLE rows(id INTEGER PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestPolicyIsEffectiveOnTheHandle(t *testing.T) {
	db := testDB(t)
	policy := DefaultPolicy()
	stats := db.Stats()
	if stats.MaxOpenConnections != policy.MaxOpenConns {
		t.Fatalf("pool opened with %d connections, policy says %d", stats.MaxOpenConnections, policy.MaxOpenConns)
	}
	// Every pooled connection must carry the pragmas, not just the first. Reserve
	// the whole pool at once and read the per-connection values from each.
	var conns []*sql.Conn
	for i := 0; i < policy.MaxOpenConns; i++ {
		conn, err := db.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, conn)
	}
	for index, conn := range conns {
		var cache, mmap, temp, busy int64
		var journal, sync string
		if err := conn.QueryRowContext(context.Background(), `PRAGMA cache_size`).Scan(&cache); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRowContext(context.Background(), `PRAGMA mmap_size`).Scan(&mmap); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRowContext(context.Background(), `PRAGMA temp_store`).Scan(&temp); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRowContext(context.Background(), `PRAGMA busy_timeout`).Scan(&busy); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRowContext(context.Background(), `PRAGMA journal_mode`).Scan(&journal); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRowContext(context.Background(), `PRAGMA synchronous`).Scan(&sync); err != nil {
			t.Fatal(err)
		}
		if cache != -policy.CacheSizeKiB {
			t.Fatalf("connection %d cache_size %d, want %d", index, cache, -policy.CacheSizeKiB)
		}
		if mmap != 0 {
			t.Fatalf("connection %d has mmap enabled: %d", index, mmap)
		}
		if temp != 2 {
			t.Fatalf("connection %d temp_store %d, want MEMORY", index, temp)
		}
		if busy != int64(policy.BusyTimeoutMilli) {
			t.Fatalf("connection %d busy_timeout %d", index, busy)
		}
		if journal != "wal" {
			t.Fatalf("connection %d journal_mode %q", index, journal)
		}
		if sync != "1" {
			t.Fatalf("connection %d synchronous %q, want NORMAL", index, sync)
		}
	}
	for _, conn := range conns {
		conn.Close()
	}
}

func TestPolicyRejectsAnUnvalidatedPreset(t *testing.T) {
	cases := []Policy{}
	over := DefaultPolicy()
	over.MaxOpenConns = 64
	cases = append(cases, over)
	mmap := DefaultPolicy()
	mmap.MmapSizeBytes = 1 << 20
	cases = append(cases, mmap)
	cache := DefaultPolicy()
	cache.CacheSizeKiB = 64 * 1024
	cases = append(cases, cache)
	idle := DefaultPolicy()
	idle.MaxIdleConns = 32
	cases = append(cases, idle)
	life := DefaultPolicy()
	life.ConnMaxLifetime = 0
	cases = append(cases, life)
	for index, policy := range cases {
		if err := policy.Validate(); !errors.Is(err, ErrPolicy) {
			t.Fatalf("case %d accepted an out-of-range policy: %v", index, err)
		}
	}
}

func TestWriteHelperReusesTheContextTransaction(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	// The inner helper must join the outer transaction. If it opened its own it
	// would queue behind a gate the outer call already holds and never return.
	done := make(chan error, 1)
	go func() {
		done <- WithWriteTxContext(ctx, db, ClassInteractive, func(inner context.Context, tx *sql.Tx) error {
			if _, err := tx.ExecContext(inner, `INSERT INTO rows(value) VALUES('outer')`); err != nil {
				return err
			}
			return WithWriteTx(inner, db, ClassBackgroundMedia, func(nested *sql.Tx) error {
				if nested != tx {
					return errors.New("nested write opened a second transaction")
				}
				_, err := nested.ExecContext(inner, `INSERT INTO rows(value) VALUES('inner')`)
				return err
			})
		})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a re-entrant write helper deadlocked on the write gate")
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM rows`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected both rows committed once, found %d", count)
	}
	if WriteGate().ActiveOrWaiting() {
		t.Fatal("the gate was still held after the transaction committed")
	}
}

func TestNestedRollbackPreventsTheOuterCommit(t *testing.T) {
	db := testDB(t)
	err := WithWriteTxContext(context.Background(), db, ClassInteractive, func(inner context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(inner, `INSERT INTO rows(value) VALUES('doomed')`); err != nil {
			return err
		}
		nested, err := Begin(inner, db, ClassInteractive)
		if err != nil {
			return err
		}
		return nested.Rollback()
	})
	if !errors.Is(err, ErrAborted) {
		t.Fatalf("the outer commit did not report the nested rollback: %v", err)
	}
	// The nested rollback marked the shared transaction aborted, so nothing from
	// it may be durable.
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM rows`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("a nested rollback still committed %d rows", count)
	}
}

func TestStrictModeRefusesNesting(t *testing.T) {
	t.Setenv(StrictEnv, "1")
	db := testDB(t)
	err := WithWriteTxContext(context.Background(), db, ClassInteractive, func(inner context.Context, tx *sql.Tx) error {
		_, err := Begin(inner, db, ClassInteractive)
		return err
	})
	if !errors.Is(err, ErrNested) {
		t.Fatalf("strict mode accepted a nested transaction: %v", err)
	}
}

func TestGateIsReleasedBeforeFollowUpCallbacks(t *testing.T) {
	db := testDB(t)
	var heldDuringCallback atomic.Bool
	w, err := Begin(context.Background(), db, ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Tx().Exec(`INSERT INTO rows(value) VALUES('committed')`); err != nil {
		t.Fatal(err)
	}
	if err = w.Commit(); err != nil {
		t.Fatal(err)
	}
	// This is the invalidation/notification position. The writer must already be
	// free, so a queued playback control write does not wait on cache work.
	heldDuringCallback.Store(WriteGate().ActiveOrWaiting())
	if heldDuringCallback.Load() {
		t.Fatal("the write gate was still held when follow-up callbacks would run")
	}
	// ExecWrite has the same contract.
	if _, err = ExecWrite(context.Background(), db, ClassInteractive, `INSERT INTO rows(value) VALUES('exec')`); err != nil {
		t.Fatal(err)
	}
	if WriteGate().ActiveOrWaiting() {
		t.Fatal("ExecWrite returned while still holding the gate")
	}
}

func TestReadSnapshotSeesOneConsistentState(t *testing.T) {
	db := testDB(t)
	if _, err := db.Exec(`INSERT INTO rows(value) VALUES('one')`); err != nil {
		t.Fatal(err)
	}
	err := WithReadSnapshot(context.Background(), db, func(snap context.Context) error {
		var before int
		if err := QueryRow(snap, db, `SELECT count(*) FROM rows`).Scan(&before); err != nil {
			return err
		}
		// A committed write from elsewhere must not change what this snapshot sees.
		if _, err := ExecWrite(context.Background(), db, ClassInteractive, `INSERT INTO rows(value) VALUES('two')`); err != nil {
			return err
		}
		var after int
		if err := QueryRow(snap, db, `SELECT count(*) FROM rows`).Scan(&after); err != nil {
			return err
		}
		if before != after {
			return errors.New("the snapshot moved under a concurrent commit")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentReadersAndWritersNeverEscapeAsBusy(t *testing.T) {
	db := testDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	var failures atomic.Int64
	var busy atomic.Int64
	// Readers open long multi-query snapshots while writers of every class commit
	// small transactions. Nothing here may surface SQLITE_BUSY: the gate removes
	// writer contention and WAL keeps readers out of the writer's way.
	for reader := 0; reader < 24; reader++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				err := WithReadSnapshot(ctx, db, func(snap context.Context) error {
					for q := 0; q < 3; q++ {
						var count int
						if err := QueryRow(snap, db, `SELECT count(*) FROM rows`).Scan(&count); err != nil {
							return err
						}
					}
					return nil
				})
				if err != nil {
					failures.Add(1)
					if Retryable(err) {
						busy.Add(1)
					}
					return
				}
			}
		}()
	}
	classes := []Class{ClassSecurityFence, ClassEstablishedPlayback, ClassInteractive, ClassBackgroundMedia, ClassMaintenance}
	for writer := 0; writer < 10; writer++ {
		wg.Add(1)
		class := classes[writer%len(classes)]
		go func() {
			defer wg.Done()
			for i := 0; i < 30; i++ {
				err := WithWriteTx(ctx, db, class, func(tx *sql.Tx) error {
					_, err := tx.Exec(`INSERT INTO rows(value) VALUES(?)`, class.String())
					return err
				})
				if err != nil {
					failures.Add(1)
					if Retryable(err) {
						busy.Add(1)
					}
					return
				}
			}
		}()
	}
	wg.Wait()
	if busy.Load() != 0 {
		t.Fatalf("%d operations escaped as SQLITE_BUSY/LOCKED", busy.Load())
	}
	if failures.Load() != 0 {
		t.Fatalf("%d operations failed under mixed load", failures.Load())
	}
	stats := Pool(db)
	if stats.OpenConnections > stats.MaxOpenConnections {
		t.Fatalf("pool exceeded its policy: %#v", stats)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM rows`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 300 {
		t.Fatalf("expected 300 committed writes, found %d", count)
	}
}

func TestRetryClassifiesAndBacksOff(t *testing.T) {
	budget := Retry{Attempts: 3, Base: time.Millisecond, Max: 2 * time.Millisecond}
	attempts := 0
	stats, err := budget.Do(context.Background(), func() error {
		attempts++
		return errors.New("database is locked")
	})
	if err == nil {
		t.Fatal("a persistent lock was reported as success")
	}
	if attempts != budget.Attempts {
		t.Fatalf("made %d attempts, budget is %d", attempts, budget.Attempts)
	}
	if stats.Retries != budget.Attempts-1 || stats.Wait <= 0 {
		t.Fatalf("retry stats not recorded: %#v", stats)
	}
	// A constraint failure is the caller's fault and must not be retried.
	attempts = 0
	if _, err = budget.Do(context.Background(), func() error {
		attempts++
		return errors.New("UNIQUE constraint failed: rows.id")
	}); err == nil || attempts != 1 {
		t.Fatalf("a constraint failure was retried %d times", attempts)
	}
	// Cancellation stops the budget rather than spending it.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	attempts = 0
	if _, err = InstallRetry.Do(ctx, func() error {
		attempts++
		return errors.New("database is locked")
	}); !errors.Is(err, context.Canceled) || attempts != 0 {
		t.Fatalf("a cancelled retry made %d attempts: %v", attempts, err)
	}
}

func TestClassFromContextTravelsWithTheWork(t *testing.T) {
	if class := ClassFrom(context.Background(), ClassInteractive); class != ClassInteractive {
		t.Fatalf("a bare context resolved to %s", class)
	}
	ctx := WithClass(context.Background(), ClassBackgroundMedia)
	if class := ClassFrom(ctx, ClassInteractive); class != ClassBackgroundMedia {
		t.Fatalf("the carried class was ignored: %s", class)
	}
}

func TestYieldNeverWaitsForForegroundOrHealth(t *testing.T) {
	ResetProbes()
	t.Cleanup(ResetProbes)
	busy := atomic.Bool{}
	busy.Store(true)
	RegisterForegroundProbe("test", busy.Load)
	start := time.Now()
	if !Yield(context.Background()) {
		t.Fatal("a yield under permanent foreground load refused to make progress")
	}
	elapsed := time.Since(start)
	if elapsed > 100*time.Millisecond {
		t.Fatalf("background batch paused under foreground load: %s", elapsed)
	}
	// With the foreground idle a yield is nearly free.
	busy.Store(false)
	start = time.Now()
	if !Yield(context.Background()) {
		t.Fatal("an idle yield refused to proceed")
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("an idle yield still waited")
	}
	RegisterHealthProbe(func() bool { return false })
	start = time.Now()
	if !Yield(context.Background()) || time.Since(start) > 100*time.Millisecond {
		t.Fatal("a degraded health report paused background work")
	}
	// Cancellation stops a yield.
	busy.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if Yield(ctx) {
		t.Fatal("a cancelled yield reported progress")
	}
}

// One admission is one attempt. A lock error after the gate is held is
// reported, counted and classified as overload; the work never runs twice.
func TestWriteMakesOneAttemptAndCountsALockAfterAdmission(t *testing.T) {
	db := testDB(t)
	before := WriteGate().Admission()
	attempts := 0
	err := WithWriteTx(context.Background(), db, ClassInteractive, func(*sql.Tx) error {
		attempts++
		return errors.New("database is locked")
	})
	if attempts != 1 {
		t.Fatalf("a lock error after admission ran the write %d times", attempts)
	}
	if !Retryable(err) || !Overloaded(err) {
		t.Fatalf("lock error lost its classification: %v", err)
	}
	after := WriteGate().Admission()
	if after.BusyAfterAdmission != before.BusyAfterAdmission+1 || after.BusyAfterAdmissionByClass[ClassInteractive.String()] != before.BusyAfterAdmissionByClass[ClassInteractive.String()]+1 {
		t.Fatalf("lock error after admission not counted: %+v -> %+v", before, after)
	}
	// A constraint failure is the caller's answer, not overload.
	err = WithWriteTx(context.Background(), db, ClassInteractive, func(*sql.Tx) error {
		return errors.New("UNIQUE constraint failed: rows.id")
	})
	if Overloaded(err) || WriteGate().Admission().BusyAfterAdmission != after.BusyAfterAdmission {
		t.Fatalf("a constraint failure was treated as overload: %v", err)
	}
}
