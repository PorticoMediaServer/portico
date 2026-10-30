package dbwork

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func readerScopeForTest(t *testing.T, db *sql.DB) *readerScope {
	t.Helper()
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	scope := checkpointReaderScope(conn)
	if scope == nil {
		t.Fatal("missing observed driver reader scope")
	}
	return scope
}

// pinnedRead opens a read transaction outside the snapshot helpers, as an
// external process or a raw caller would: no lifetime cap applies, so it can
// pin its WAL frame boundary for as long as the test needs.
func pinnedRead(ctx context.Context, db *sql.DB) (*sql.Tx, func(), error) {
	tx, err := ReadHandle(ctx, db).BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, nil, err
	}
	return tx, func() { _ = tx.Rollback() }, nil
}

func waitReaderCondition(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !fn() {
		if time.Now().After(deadline) {
			t.Fatal("reader state did not converge")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestReaderPressurePreservesExistingSnapshotAndWriterPriority(t *testing.T) {
	db := readerFixture(t)
	ctx := context.Background()
	scope := readerScopeForTest(t, db)
	tx, closeRead, err := BeginRead(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	defer closeRead()
	reopen, drained, backoff := scope.pause()
	if reopen == nil || backoff != 0 {
		t.Fatal("could not pause readers")
	}
	defer reopen(true)
	// Existing snapshots keep querying while new reads queue. Security-fence
	// writes must not wait for the reader gap they are allowed to write through.
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM sample`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	writeCtx, cancelWrite := context.WithTimeout(ctx, time.Second)
	defer cancelWrite()
	if _, err = ExecWrite(writeCtx, db, ClassSecurityFence, `INSERT INTO sample VALUES(3)`); err != nil {
		t.Fatalf("pressure blocked security writer: %v", err)
	}
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM sample`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("snapshot changed during pressure: count%d err%v", count, err)
	}
	done := make(chan error, 1)
	go func() { var n int; done <- db.QueryRowContext(ctx, `SELECT count(*) FROM sample`).Scan(&n) }()
	waitReaderCondition(t, func() bool { return scope.stats().AdmissionWaiting == 1 })
	fenceCtx := WithClass(ctx, ClassSecurityFence)
	if err = QueryRow(fenceCtx, db, `SELECT count(*) FROM sample`).Scan(&count); err != nil {
		t.Fatalf("pressure blocked new security preflight read: %v", err)
	}
	fence, finishFence, err := BeginRead(fenceCtx, db)
	if err != nil {
		t.Fatalf("pressure blocked new security snapshot: %v", err)
	}
	if stats := scope.stats(); stats.Snapshots != 2 {
		t.Fatalf("security bypass lost lifetime tracking: %+v", stats)
	}
	if err = fence.QueryRowContext(fenceCtx, `SELECT count(*) FROM sample`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("security preflight retained stale authority state: count%d", count)
	}
	finishFence()

	select {
	case err = <-done:
		t.Fatalf("new read entered pressure: %v", err)
	default:
	}
	closeRead()
	select {
	case <-drained:
	default:
		t.Fatal("finished snapshot did not drain")
	}
	reopen(true)
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued reader did not resume")
	}
	if stats := scope.stats(); stats.AdmissionPaused || stats.AdmissionWaiting != 0 || stats.Snapshots != 0 || stats.ImplicitRows != 0 {
		t.Fatalf("pressure retained reader state: %+v", stats)
	}
}

func TestReaderPressureCancellationRecoveryAndDatabaseIsolation(t *testing.T) {
	db := readerFixture(t)
	other := readerFixture(t)
	scope := readerScopeForTest(t, db)
	reopen, _, _ := scope.pause()
	defer reopen(true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { var n int; done <- db.QueryRowContext(ctx, `SELECT count(*) FROM sample`).Scan(&n) }()
	waitReaderCondition(t, func() bool { return scope.stats().AdmissionWaiting == 1 })
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting reader did not observe cancellation: %v", err)
	}
	var count int
	if err := other.QueryRow(`SELECT count(*) FROM sample`).Scan(&count); err != nil {
		t.Fatalf("another database inherited pressure: %v", err)
	}
	reopen(true)
	if err := db.QueryRow(`SELECT count(*) FROM sample`).Scan(&count); err != nil {
		t.Fatalf("normal reads did not recover: %v", err)
	}
}

func TestCheckpointHardPressureDrainsOverlappingReadersAndShrinksPhysicalWAL(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db := readerFixture(t)
	ctx := context.Background()
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	first, finishFirst, err := pinnedRead(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	defer finishFirst()
	var count int
	if err = first.QueryRow(`SELECT count(*) FROM sample`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	// Real frames, not a forged counter: keep the old read mark while one atomic
	// write creates >192MiB. Cached statements and snapshot helpers use the
	// actual registered SQLite driver and all three physical pools.
	if _, err = ExecWrite(ctx, db, ClassInteractive, `CREATE TABLE large(id INTEGER PRIMARY KEY, body BLOB); WITH RECURSIVE ids(id) AS (VALUES(1) UNION ALL SELECT id+1 FROM ids WHERE id<25000) INSERT INTO large SELECT id,zeroblob(8192) FROM ids`); err != nil {
		t.Fatal(err)
	}
	second, finishSecond, err := pinnedRead(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	defer finishSecond()
	if err = second.QueryRow(`SELECT count(*) FROM sample`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	scope := readerScopeForTest(t, db)
	stats := copyWALForTest(t, ctx, db)
	if stats.LogFrames < CheckpointHardFrames || stats.FileBytes < 192<<20 {
		t.Fatalf("fixture lacks hard WAL pressure: %+v", stats)
	}
	WriteGate().ResetPeak()
	// A genuinely pinned snapshot cannot be evicted. One bounded attempt must
	// resume readers and back off without ever reserving the writer gate.
	failed := Checkpoint(ctx, db)
	if failed.Err != nil || failed.Outcome != "reader-timeout" || failed.DrainMs < 200 || failed.DrainMs > 500 || failed.BackoffMs < 900 {
		t.Fatalf("pinned-reader drain was not bounded/backed off: %+v", failed)
	}
	if gate := WriteGate().Stats().ByClass[ClassMaintenance.String()]; gate.MaxHeldMilli != 0 || gate.Active {
		t.Fatalf("pinned-reader drain reserved writer gate: %+v", gate)
	}
	if stats := scope.stats(); stats.AdmissionPaused {
		t.Fatalf("failed checkpoint retained admission pause: %+v", stats)
	}
	if retry := Checkpoint(ctx, db); retry.Err != nil || retry.Outcome != "reader-backoff" || retry.BackoffMs == 0 {
		t.Fatalf("repeated pressure ignored backoff: %+v", retry)
	}
	// Wait exactly the production retry boundary, rather than inserting a
	// fixture-only delay into any read or writer.
	<-time.After(scope.pressureBackoff() + time.Millisecond)
	cancelCtx, cancelCheckpoint := context.WithCancel(ctx)
	cancelledResult := make(chan CheckpointResult, 1)
	go func() { cancelledResult <- Checkpoint(cancelCtx, db) }()
	waitReaderCondition(t, func() bool { return scope.stats().AdmissionPaused })
	cancelCheckpoint()
	if cancelled := <-cancelledResult; !errors.Is(cancelled.Err, context.Canceled) {
		t.Fatalf("checkpoint drain ignored cancellation: %+v", cancelled)
	}
	if stats := scope.stats(); stats.AdmissionPaused {
		t.Fatalf("cancelled checkpoint retained pause: %+v", stats)
	}
	<-time.After(scope.pressureBackoff() + time.Millisecond)
	done := make(chan CheckpointResult, 1)
	go func() { done <- Checkpoint(ctx, db) }()
	waitReaderCondition(t, func() bool { return scope.stats().AdmissionPaused })
	if WriteGate().Stats().ByClass[ClassMaintenance.String()].Active {
		t.Fatal("reader drain acquired writer gate")
	}
	newRead := make(chan error, 1)
	go func() { var n int; newRead <- db.QueryRow(`SELECT count(*) FROM large`).Scan(&n) }()
	waitReaderCondition(t, func() bool { return scope.stats().AdmissionWaiting != 0 })
	finishFirst()
	if err = second.QueryRow(`SELECT count(*) FROM sample`).Scan(&count); err != nil {
		t.Fatalf("remaining snapshot could not finish: %v", err)
	}
	finishSecond()
	result := <-done
	if result.Err != nil || result.Outcome != "truncated" || result.BeforeLogFrames < CheckpointHardFrames || result.LogFrames != 0 || result.Checkpoint != 0 {
		t.Fatalf("overlap pressure did not reset: %+v", result)
	}
	if err = <-newRead; err != nil {
		t.Fatalf("new read failed instead of resuming: %v", err)
	}
	if actual := walFileBytes(ctx, db); actual != 0 {
		t.Fatalf("logical reset retained%d physical WAL bytes", actual)
	}
	t.Logf("hard checkpoint reset%d frames from%dMiB after%dms drain (%dms total)", result.BeforeLogFrames, stats.FileBytes>>20, result.DrainMs, result.DurationMs)
	t.Run("continuous-snapshot-churn", func(t *testing.T) {
		// Fixture generation belongs to the runner's budget, not the timed
		// reader/checkpoint exercise. Keep its four old boundaries until the
		// large write commits; every later request gets the unchanged10s budget.
		fixtureCtx := ctx
		if deadline, ok := t.Deadline(); ok {
			var cancelFixture context.CancelFunc
			fixtureCtx, cancelFixture = context.WithDeadline(ctx, deadline)
			defer cancelFixture()
		}
		initial := make([]*sql.Tx, 4)
		finishInitial := make([]func(), 4)
		defer func() {
			for _, finish := range finishInitial {
				if finish != nil {
					finish()
				}
			}
		}()
		for worker := range initial {
			var err error
			initial[worker], finishInitial[worker], err = pinnedRead(fixtureCtx, db)
			if err != nil {
				t.Fatal(err)
			}
			var count int
			if err = initial[worker].QueryRowContext(fixtureCtx, `SELECT count(*) FROM sample`).Scan(&count); err != nil {
				t.Fatal(err)
			}
		}
		// Change every payload byte without spending the race budget on random
		// number generation. These are real writes to every large-table page.
		if _, err := ExecWrite(fixtureCtx, db, ClassInteractive, `UPDATE large SET body=?`, bytes.Repeat([]byte{1}, 8192)); err != nil {
			t.Fatal(err)
		}
		churnCtx, stopChurn := context.WithTimeout(ctx, 10*time.Second)
		defer stopChurn()
		failures := make(chan error, 4)
		var workers sync.WaitGroup
		defer func() { stopChurn(); workers.Wait() }()
		var completed atomic.Int64
		for worker := 0; worker < 4; worker++ {
			workers.Add(1)
			go func(worker int) {
				defer workers.Done()
				tx, finish := initial[worker], finishInitial[worker]
				var err error
				for {
					if churnCtx.Err() != nil {
						finish()
						return
					}
					var total int64
					err = tx.QueryRowContext(churnCtx, `SELECT count(*) FROM sample`).Scan(&total)
					// Real finite SQL work staggers request completion, with no
					// sleeps and no carried snapshot between requests. Varying
					// bounded row ranges (4-10 rows of rewritten pages) naturally
					// overlap four healthy readers. The ranges stay small: the
					// property is continuous overlap, not throughput, and a request
					// must finish well inside the 250 ms reader drain even under the
					// race detector on a loaded machine, where hex-encoding hundreds
					// of 8 KiB rows took about three seconds.
					for statement := 0; statement < 4 && err == nil; statement++ {
						err = tx.QueryRowContext(churnCtx, `SELECT sum(length(hex(body))) FROM large WHERE id BETWEEN ? AND ?`, 1+worker*512, 4+worker*514).Scan(&total)
					}
					finish() // Release before admitting the next request.
					if err != nil {
						if churnCtx.Err() == nil {
							failures <- err
						}
						return
					}
					completed.Add(1)
					tx, finish, err = BeginRead(churnCtx, db)
					if err != nil {
						if churnCtx.Err() == nil {
							failures <- err
						}
						return
					}
				}
			}(worker)
		}
		waitReaderCondition(t, func() bool { return completed.Load() >= 12 && scope.stats().Snapshots > 0 })
		if _, err := ExecWrite(churnCtx, db, ClassInteractive, `INSERT INTO sample VALUES(4)`); err != nil {
			stopChurn()
			workers.Wait()
			t.Fatal(err)
		}
		before := completed.Load()
		reset := Checkpoint(churnCtx, db)
		if reset.Err != nil || reset.Outcome != "truncated" || reset.BeforeLogFrames < CheckpointHardFrames || reset.LogFrames != 0 || reset.Checkpoint != 0 || reset.DrainMs > CheckpointReaderDrainDeadline.Milliseconds() {
			stopChurn()
			workers.Wait()
			t.Fatalf("continuous healthy readers prevented reset: %+v completed%d", reset, completed.Load())
		}
		waitReaderCondition(t, func() bool { return completed.Load() >= before+8 })
		if _, err := ExecWrite(churnCtx, db, ClassSecurityFence, `INSERT INTO sample VALUES(5)`); err != nil {
			stopChurn()
			workers.Wait()
			t.Fatal(err)
		}
		stopChurn()
		workers.Wait()
		select {
		case err := <-failures:
			t.Fatal(err)
		default:
		}
		// The security writer ran AFTER the proven zero-frame reset. Its new
		// committed tail may still be awaiting PASSIVE copying, so final zero
		// backlog would test a quiet database rather than writer progress.
		// Retain both the physical ceiling and a bound on logical frames.
		if current := copyWALForTest(t, ctx, db); current.LastError != "" || current.LogFrames >= CheckpointTruncateFrames || current.BacklogFrames >= CheckpointTruncateFrames || current.FileBytes >= 1<<20 {
			t.Fatalf("churn checkpoint did not converge physical WAL: %+v", current)
		}
		t.Logf("four continually reopening readers completed%d snapshots; hard reset%d frames after%dms drain (%dms total); writers progressed before/after", completed.Load(), reset.BeforeLogFrames, reset.DrainMs, reset.DurationMs)
	})
}

// A new read kept waiting behind a WAL reset's reader pause for longer than
// ReaderPressureWait is refused with retry guidance instead of holding its
// request to its own deadline; a security read still passes.
func TestReadPauseRefusesANewReadWithRetryGuidance(t *testing.T) {
	db := readerFixture(t)
	scope := readerScopeForTest(t, db)
	reopen, _, _ := scope.pause()
	if reopen == nil {
		t.Fatal("pause unavailable")
	}
	defer reopen(true)
	var n int
	started := time.Now()
	err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM sample`).Scan(&n)
	if !errors.Is(err, ErrReadPressure) || time.Since(started) < ReaderPressureWait {
		t.Fatalf("paused read: %v after %s", err, time.Since(started))
	}
	if scope.stats().AdmissionRefused != 1 {
		t.Fatal("refusal not counted")
	}
	if err = db.QueryRowContext(WithClass(context.Background(), ClassSecurityFence), `SELECT count(*) FROM sample`).Scan(&n); err != nil {
		t.Fatalf("security read waited behind the pause: %v", err)
	}
}
