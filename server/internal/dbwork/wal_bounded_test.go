package dbwork

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCheckpointPinnedReaderBoundsGateRestoresPolicyAndRecovers(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := OpenHandle(filepath.Join(t.TempDir(), "checkpoint.sqlite"), DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err = ExecWrite(ctx, db, ClassInteractive, `CREATE TABLE sample(id INTEGER PRIMARY KEY, body BLOB)`); err != nil {
		t.Fatal(err)
	}
	if _, err = ExecWrite(ctx, db, ClassInteractive, `INSERT INTO sample VALUES(0,'seed')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	reader, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Rollback()
	var count int
	if err = reader.QueryRow(`SELECT count(*) FROM sample`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	// One atomic fixture write creates >64MiB of frames behind the reader.
	if _, err = ExecWrite(ctx, db, ClassBackgroundMedia, `WITH RECURSIVE ids(id) AS (VALUES(1) UNION ALL SELECT id+1 FROM ids WHERE id<9000) INSERT INTO sample SELECT id,zeroblob(8192) FROM ids`); err != nil {
		t.Fatal(err)
	}
	if stats := copyWALForTest(t, ctx, db); stats.LogFrames < CheckpointTruncateFrames {
		t.Fatalf("fixture has only%d WAL frames", stats.LogFrames)
	}
	control := context.WithValue(WithClass(ctx, ClassMaintenance), checkpointReaderBypassKey{}, true)
	pool := ReadHandle(control, db)
	spare, err := pool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer spare.Close()
	// Leave one control slot beside the pinned reader, the spare and the
	// foreground loans. This is the existing public-pool headroom checkpoint
	// control uses; the sole background connection is not a recovery resource.
	foreground := []*sql.Conn{}
	for i := 0; i < pool.Stats().MaxOpenConnections-3; i++ {
		loanCtx, cancel := context.WithTimeout(ctx, time.Second)
		conn, err := db.Conn(loanCtx)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		foreground = append(foreground, conn)
	}
	policyConn, err := pool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = policyConn.ExecContext(ctx, `PRAGMA busy_timeout=3210`); err != nil {
		t.Fatal(err)
	}
	policyConn.Close()
	defer func() {
		for _, conn := range foreground {
			conn.Close()
		}
	}()
	WriteGate().ResetPeak()
	done := make(chan CheckpointResult, 1)
	start := time.Now()
	go func() { done <- Checkpoint(ctx, db) }()
	fenceCtx, cancelFence := context.WithTimeout(ctx, 500*time.Millisecond)
	free, err := WriteGate().Acquire(fenceCtx, ClassSecurityFence)
	cancelFence()
	if err != nil {
		t.Fatalf("blocked reader pinned security writer: %v", err)
	}
	free()
	result := <-done
	// A pinned tail cannot be safely reset. PASSIVE must yield without taking
	// the gate, rather than attempting TRUNCATE and hoping its busy timeout
	// also bounds the page copying that follows a reader releasing its lock.
	if result.Err != nil || result.Mode != "passive" || result.Checkpoint >= result.LogFrames || time.Since(start) > 500*time.Millisecond {
		t.Fatalf("blocked checkpoint was not bounded: %+v duration%s", result, time.Since(start))
	}
	if hold := WriteGate().Stats().ByClass[ClassMaintenance.String()].MaxHeldMilli; hold != 0 {
		t.Fatalf("maintenance held gate for%dms", hold)
	}
	assertBusy := func() {
		t.Helper()
		var actual int
		if err := pool.QueryRow(`PRAGMA busy_timeout`).Scan(&actual); err != nil {
			t.Fatal(err)
		}
		if actual != 3210 {
			t.Fatalf("checkpoint returned busy_timeout%d instead of original3210", actual)
		}
	}
	assertBusy()
	cancelCtx, cancel := context.WithCancel(ctx)
	timer := time.AfterFunc(10*time.Millisecond, cancel)
	start = time.Now()
	cancelled := Checkpoint(cancelCtx, db)
	timer.Stop()
	cancel()
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("context cancellation inherited long WAL busy wait")
	}
	if cancelled.Err != nil && !errors.Is(cancelled.Err, context.Canceled) {
		t.Fatalf("unexpected cancelled checkpoint: %v", cancelled.Err)
	}
	assertBusy()
	if err = reader.Rollback(); err != nil {
		t.Fatal(err)
	}
	// Keep the released reader's slot borrowed so policy checks still inspect
	// the exact control connection rather than an unrelated pooled connection.
	releasedReader, err := pool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer releasedReader.Close()
	recovered := Checkpoint(ctx, db)
	if recovered.Err != nil || recovered.Busy != 0 || recovered.Mode != "truncate" || recovered.LogFrames != 0 {
		t.Fatalf("checkpoint did not recover after reader drained: %+v", recovered)
	}
	assertBusy()
	if err = db.QueryRow(`SELECT count(*) FROM sample`).Scan(&count); err != nil || count != 9001 {
		t.Fatalf("checkpoint changed committed rows: count%d error%v", count, err)
	}
	var integrity string
	if err = db.QueryRow(`PRAGMA quick_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("database integrity after checkpoints: %q %v", integrity, err)
	}
}

type checkpointCleanupConnector struct{ conn *checkpointCleanupConn }

func (c checkpointCleanupConnector) Connect(context.Context) (driver.Conn, error) { return c.conn, nil }
func (c checkpointCleanupConnector) Driver() driver.Driver                        { return checkpointCleanupDriver{c.conn} }

type checkpointCleanupDriver struct{ conn *checkpointCleanupConn }

func (d checkpointCleanupDriver) Open(string) (driver.Conn, error) { return d.conn, nil }

type checkpointCleanupConn struct {
	busy        int
	failRestore bool
	closed      atomic.Int32
	checkpoint  func()
}

func (c *checkpointCleanupConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare unsupported")
}
func (c *checkpointCleanupConn) Begin() (driver.Tx, error) {
	return nil, errors.New("begin unsupported")
}
func (c *checkpointCleanupConn) Close() error { c.closed.Add(1); return nil }
func (c *checkpointCleanupConn) ExecContext(ctx context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if query == "PRAGMA busy_timeout=3210" && c.failRestore {
		return nil, errors.New("injected restore failure")
	}
	if query == "PRAGMA busy_timeout=3210" {
		c.busy = 3210
	} else if query == "PRAGMA busy_timeout=50" {
		c.busy = 50
	}
	return driver.RowsAffected(0), nil
}
func (c *checkpointCleanupConn) QueryContext(ctx context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if query == "PRAGMA busy_timeout" {
		return &checkpointRows{values: []driver.Value{int64(c.busy)}}, nil
	}
	if query == "PRAGMA page_size" {
		return &checkpointRows{values: []driver.Value{int64(4096)}}, nil
	}
	if query == "PRAGMA database_list" {
		return &checkpointRows{values: []driver.Value{int64(0), "main", ""}}, nil
	}
	if strings.Contains(query, "wal_checkpoint") {
		if c.checkpoint != nil {
			c.checkpoint()
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		return &checkpointRows{values: []driver.Value{int64(0), int64(0), int64(0)}}, nil
	}
	return nil, errors.New("unexpected query")
}

type checkpointRows struct {
	values []driver.Value
	done   bool
}

func (r *checkpointRows) Columns() []string {
	names := make([]string, len(r.values))
	for i := range names {
		names[i] = "value"
	}
	return names
}
func (r *checkpointRows) Close() error { return nil }
func (r *checkpointRows) Next(out []driver.Value) error {
	if r.done {
		return io.EOF
	}
	copy(out, r.values)
	r.done = true
	return nil
}

func TestCheckpointCancelledContextRestoresExactPolicyIndependently(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	conn := &checkpointCleanupConn{busy: 3210, checkpoint: cancel}
	db := sql.OpenDB(checkpointCleanupConnector{conn})
	defer db.Close()
	result := Checkpoint(ctx, db)
	if !errors.Is(result.Err, context.Canceled) || conn.busy != 3210 || conn.closed.Load() != 0 {
		t.Fatalf("cancelled checkpoint did not restore reusable policy: %+v busy%d closed%d", result, conn.busy, conn.closed.Load())
	}
}

func TestCheckpointRestorationFailureDiscardsConnection(t *testing.T) {
	conn := &checkpointCleanupConn{busy: 3210, failRestore: true}
	db := sql.OpenDB(checkpointCleanupConnector{conn})
	defer db.Close()
	result := Checkpoint(context.Background(), db)
	if result.Err == nil || !strings.Contains(result.Err.Error(), "restore checkpoint busy timeout") || conn.closed.Load() != 1 || db.Stats().Idle != 0 {
		t.Fatalf("altered connection returned to pool: %+v closed%d pool%+v", result, conn.closed.Load(), db.Stats())
	}
}
