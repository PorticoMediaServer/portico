package dbwork

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/worker"
	"testing"
	"time"
)

func TestTargetedCommitWakesFollowTriggersAndIgnoreRollback(t *testing.T) {
	db, e := OpenHandle(filepath.Join(t.TempDir(), "wake.sqlite"), DefaultPolicy())
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	_, e = db.Exec(`CREATE TABLE wake_parent(id INTEGER PRIMARY KEY);CREATE TABLE wake_source(id INTEGER PRIMARY KEY,parent INTEGER REFERENCES wake_parent(id) ON DELETE CASCADE);CREATE TABLE wake_jobs(id INTEGER PRIMARY KEY);CREATE TRIGGER wake_enqueue AFTER INSERT ON wake_source BEGIN INSERT INTO wake_jobs VALUES(NEW.id);END;CREATE TABLE unrelated_jobs(id INTEGER PRIMARY KEY)`)
	if e != nil {
		t.Fatal(e)
	}
	if e = InstallWakeDependencies(context.Background(), db); e != nil {
		t.Fatal(e)
	}
	listen := func(table string) chan struct{} {
		t.Helper()
		sig := worker.NewSignal()
		unregister := WakeOnTables(sig, table)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		passes := make(chan struct{}, 8)
		go func() {
			defer close(done)
			worker.RunWith(ctx, "test.targeted", sig, nil, func(context.Context) time.Duration { passes <- struct{}{}; return 0 })
		}()
		<-passes
		t.Cleanup(func() { unregister(); cancel(); <-done })
		return passes
	}
	jobs, unrelated := listen("wake_jobs"), listen("unrelated_jobs")
	commit := func(query string) {
		t.Helper()
		if e = WithWriteTx(context.Background(), db, ClassInteractive, func(tx *sql.Tx) error {
			stmt, e := tx.Prepare(query)
			if e != nil {
				return e
			}
			defer stmt.Close()
			_, e = stmt.Exec()
			return e
		}); e != nil {
			t.Fatal(e)
		}
	}
	commit(`INSERT INTO wake_source VALUES(1,NULL)`)
	select {
	case <-jobs:
	case <-time.After(time.Second):
		t.Fatal("trigger-dependent job was not woken")
	}
	select {
	case <-unrelated:
		t.Fatal("unrelated worker woken")
	case <-time.After(20 * time.Millisecond):
	}
	held, e := Begin(context.Background(), db, ClassInteractive)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = held.Tx().Exec(`INSERT INTO wake_source VALUES(2,NULL)`); e != nil {
		t.Fatal(e)
	}
	if e = held.Rollback(); e != nil {
		t.Fatal(e)
	}
	select {
	case <-jobs:
		t.Fatal("rollback woke worker")
	case <-time.After(20 * time.Millisecond):
	}
	// A cascade dependency is included even when the direct mutation names only
	// the referenced parent. Subscriptions remain conservative for FK updates.
	source := listen("wake_source")
	commit(`INSERT INTO wake_parent VALUES(1)`)
	select {
	case <-source:
	case <-time.After(time.Second):
		t.Fatal("foreign key dependency omitted")
	}
}

func TestExecWriteWakesMountLikeWorkerButReadDoesNot(t *testing.T) {
	db := testDB(t)
	sig := worker.NewSignal()
	stopWake := WakeOnTables(sig, "rows")
	defer stopWake()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	passes := make(chan time.Time, 4)
	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.Run(ctx, "mount-like-reconcile", sig, func(context.Context) time.Duration {
			passes <- time.Now()
			return 0
		})
	}()
	<-passes // startup reconciliation
	readRows, err := db.Query(`SELECT value FROM rows`)
	if err != nil {
		t.Fatal(err)
	}
	readRows.Close()
	select {
	case <-passes:
		t.Fatal("read woke a targeted worker")
	case <-time.After(30 * time.Millisecond):
	}
	if _, err := ExecWrite(context.Background(), db, ClassInteractive, `UPDATE rows SET value='unchanged' WHERE id=-1`); err != nil {
		t.Fatal(err)
	}
	select {
	case <-passes:
		t.Fatal("no-op write woke a targeted worker")
	case <-time.After(30 * time.Millisecond):
	}
	start := time.Now()
	if _, err := ExecWrite(context.Background(), db, ClassInteractive, `INSERT INTO rows(value) VALUES('mount desired state')`); err != nil {
		t.Fatal(err)
	}
	select {
	case <-passes:
		if elapsed := time.Since(start); elapsed > 1500*time.Millisecond {
			t.Fatalf("mount-like worker reconciled after %s", elapsed)
		}
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("ExecWrite did not wake the mount-like worker")
	}
	cancel()
	<-done
}

func TestExecWriteTracksKnownRootsAndKeepsUnknownBroadcast(t *testing.T) {
	db := testDB(t)
	if _, err := db.Exec(`CREATE TABLE unrelated_jobs(id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	rows, unrelated := worker.NewSignal(), worker.NewSignal()
	directRows, directUnrelated := worker.NewSignal(), worker.NewSignal()
	for _, stop := range []func(){WakeOnTables(rows, "rows"), WakeOnTables(unrelated, "unrelated_jobs"), WakeOnDirectWrites(directRows, "rows"), WakeOnDirectWrites(directUnrelated, "unrelated_jobs")} {
		defer stop()
	}
	for _, tc := range []struct {
		name, query     string
		wantErr, change bool
		unknown         bool
	}{
		{"insert", `INSERT INTO rows(id,value) VALUES(1,'first')`, false, true, false},
		{"update", `UPDATE rows SET value='second' WHERE id=1`, false, true, false},
		{"no change", `UPDATE rows SET value='absent' WHERE id=-1`, false, false, false},
		{"failure", `INSERT INTO rows(id,value) VALUES(1,'duplicate')`, true, false, false},
		{"CTE", `WITH value(id) AS (SELECT 2) INSERT INTO rows(id,value) SELECT id,'cte' FROM value`, false, true, true},
		{"multiple statements", `INSERT INTO rows(id,value) VALUES(3,'multi'); INSERT INTO unrelated_jobs VALUES(1)`, false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ExecWrite(ctx, db, ClassInteractive, tc.query)
			if (err != nil) != tc.wantErr {
				t.Fatalf("write error %v, wantErr %v", err, tc.wantErr)
			}
			if got := rows.Take(); got != tc.change {
				t.Fatalf("source wake %v, want %v", got, tc.change)
			}
			if got := directRows.Take(); got != tc.change {
				t.Fatalf("direct source wake %v, want %v", got, tc.change)
			}
			if got := unrelated.Take(); got != tc.unknown {
				t.Fatalf("unrelated wake %v, want unknown broadcast %v", got, tc.unknown)
			}
			if got := directUnrelated.Take(); got != tc.unknown {
				t.Fatalf("direct unrelated wake %v, want unknown broadcast %v", got, tc.unknown)
			}
		})
	}
	if err := WithWriteTxContext(ctx, db, ClassInteractive, func(inner context.Context, _ *sql.Tx) error {
		_, err := ExecWrite(inner, db, ClassInteractive, `INSERT INTO rows(id,value) VALUES(4,'nested')`)
		if rows.Take() || directRows.Take() || unrelated.Take() || directUnrelated.Take() {
			t.Fatal("nested write notified before its actual transaction committed")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if !rows.Take() || unrelated.Take() || directRows.Take() || directUnrelated.Take() {
		t.Fatal("nested write lost its transaction scope or gained direct notifications")
	}
}

func TestExecWriteTracksTriggerAndForeignKeyDependents(t *testing.T) {
	db := testDB(t)
	_, err := db.Exec(`CREATE TABLE scope_parent(id INTEGER PRIMARY KEY);CREATE TABLE scope_child(id INTEGER PRIMARY KEY,parent INTEGER REFERENCES scope_parent(id) ON DELETE CASCADE);CREATE TABLE scope_source(id INTEGER PRIMARY KEY);CREATE TABLE unrelated_jobs(id INTEGER PRIMARY KEY);CREATE TRIGGER scope_enqueue AFTER INSERT ON scope_source BEGIN INSERT INTO scope_child VALUES(NEW.id,1);END;INSERT INTO scope_parent VALUES(1)`)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := InstallWakeDependencies(ctx, db); err != nil {
		t.Fatal(err)
	}
	child, unrelated := worker.NewSignal(), worker.NewSignal()
	stopChild, stopUnrelated := WakeOnTables(child, "scope_child"), WakeOnTables(unrelated, "unrelated_jobs")
	defer stopChild()
	defer stopUnrelated()
	for _, query := range []string{`INSERT INTO scope_source VALUES(7)`, `DELETE FROM scope_parent WHERE id=1`} {
		if _, err := ExecWrite(ctx, db, ClassInteractive, query); err != nil {
			t.Fatal(err)
		}
		if !child.Take() || unrelated.Take() {
			t.Fatal("dependent write was lost or broadcast to an unrelated subscriber")
		}
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM scope_child`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("foreign-key delete did not actually cascade: count %d, error %v", count, err)
	}
}

func TestExecWritePostCountFailureBroadcastsWithoutReplaying(t *testing.T) {
	conn := &postCountFailureConn{}
	db := sql.OpenDB(postCountFailureConnector{conn})
	defer db.Close()
	source, unrelated := worker.NewSignal(), worker.NewSignal()
	direct := worker.NewSignal()
	for _, stop := range []func(){WakeOnTables(source, "rows"), WakeOnTables(unrelated, "unrelated_jobs"), WakeOnDirectWrites(direct, "unrelated_jobs")} {
		defer stop()
	}
	result, err := ExecWrite(context.Background(), db, ClassInteractive, `INSERT INTO rows(value) VALUES('committed')`)
	if err != nil || result == nil || conn.executed != 1 || conn.counts != 2 {
		t.Fatalf("committed statement was failed or replayed: err %v, executions %d, counts %d", err, conn.executed, conn.counts)
	}
	if !source.Take() || !unrelated.Take() || !direct.Take() {
		t.Fatal("a committed write with unknown post-count did not broadcast conservatively")
	}
}

// This public-handle driver commits once, then fails only the post-write count.
// ExecWrite must not retry a statement whose commit already happened.
type postCountFailureConn struct{ counts, executed int }

func (c *postCountFailureConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (c *postCountFailureConn) Close() error { return nil }
func (c *postCountFailureConn) Begin() (driver.Tx, error) {
	return nil, errors.New("unexpected transaction")
}
func (c *postCountFailureConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	c.executed++
	return driver.RowsAffected(1), nil
}
func (c *postCountFailureConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	c.counts++
	if c.counts > 1 {
		return nil, errors.New("post-write count unavailable")
	}
	return &postCountFailureRows{}, nil
}

type postCountFailureConnector struct{ conn *postCountFailureConn }

func (c postCountFailureConnector) Connect(context.Context) (driver.Conn, error) { return c.conn, nil }
func (c postCountFailureConnector) Driver() driver.Driver                        { return c }
func (c postCountFailureConnector) Open(string) (driver.Conn, error)             { return c.conn, nil }

type postCountFailureRows struct{ read bool }

func (*postCountFailureRows) Columns() []string { return []string{"total_changes()"} }
func (*postCountFailureRows) Close() error      { return nil }
func (r *postCountFailureRows) Next(values []driver.Value) error {
	if r.read {
		return io.EOF
	}
	r.read, values[0] = true, int64(0)
	return nil
}

// WakeOnTableWrites wakes only for a write that names one of its tables: not
// for a write whose scope couldn't be proven, nor for an unrelated table.
func TestWakeOnTableWritesIsExact(t *testing.T) {
	signal := worker.NewSignal()
	unregister := WakeOnTableWrites(signal, "inventory_runs")
	defer unregister()
	for _, statement := range []string{"WITH x AS (SELECT 1) UPDATE jobs SET status='x'", "UPDATE jobs SET processed=1", "DELETE FROM library_sources WHERE id='x'"} {
		changes := &changeSet{}
		changes.note(statement)
		notifyCommit(changes)
		if signal.Take() {
			t.Fatalf("%q woke an exact subscriber", statement)
		}
	}
	changes := &changeSet{}
	changes.note("WITH x AS (SELECT 1) SELECT 1")
	changes.note("UPDATE inventory_runs SET discovered=discovered+1 WHERE job_id=?")
	notifyDirectWrite(changes)
	if !signal.Take() {
		t.Fatal("a write naming the table didn't wake it")
	}
}
