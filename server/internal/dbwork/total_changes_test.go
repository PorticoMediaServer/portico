package dbwork

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/worker"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGatedWriteBookkeepingUsesStatementCounter(t *testing.T) {
	db := testDB(t)
	ctx, statements := TraceQueryPlans(context.Background())
	if _, err := ExecWrite(ctx, db, ClassInteractive, `INSERT INTO rows(value) VALUES('autocommit')`); err != nil {
		t.Fatal(err)
	}
	if err := WithWriteTx(ctx, db, ClassInteractive, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO rows(value) VALUES('transaction')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, query := range statements() {
		if strings.Contains(strings.ToLower(query), "total_changes(") {
			t.Errorf("bookkeeping dispatched a SQL meter: %s", query)
		}
	}
}

func TestTotalChangesPreservesTransactionCountAndLifetime(t *testing.T) {
	for _, kind := range []string{"ordinary", "security", "durable", "reserved"} {
		t.Run(kind, func(t *testing.T) {
			db := testDB(t)
			if _, err := db.Exec(`CREATE TABLE audit(value TEXT); CREATE TRIGGER writes AFTER INSERT ON rows BEGIN INSERT INTO audit VALUES(new.value); END;`); err != nil {
				t.Fatal(err)
			}
			ctx, queries := TraceQueryPlans(context.Background())
			var w *Write
			var err error
			switch kind {
			case "security":
				w, err = Begin(ctx, db, ClassSecurityFence)
			case "durable":
				w, err = Begin(WithDurableUser(ctx), db, ClassInteractive)
			case "reserved":
				reserved, conn, release, reserveErr := ReserveWriterConn(ctx, db, ClassInteractive)
				if reserveErr != nil {
					t.Fatal(reserveErr)
				}
				defer release()
				w, err = BeginConn(reserved, conn, ClassInteractive)
			default:
				w, err = Begin(ctx, db, ClassInteractive)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer w.Rollback()
			before, err := TotalChanges(ctx, w.Tx())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Tx().ExecContext(ctx, `INSERT INTO rows(value) VALUES('counted')`); err != nil {
				t.Fatal(err)
			}
			native, err := TotalChanges(ctx, w.Tx())
			if err != nil || native-before != 2 {
				t.Fatalf("trigger count delta=%d err=%v", native-before, err)
			}
			for _, q := range queries() {
				if strings.Contains(q, "total_changes(") {
					t.Fatalf("native write used SQL meter: %s", q)
				}
			}
			var oracle int64
			if err := w.Tx().QueryRowContext(ctx, `SELECT total_changes()`).Scan(&oracle); err != nil || native != oracle {
				t.Fatalf("native=%d SQL=%d err=%v", native, oracle, err)
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := TotalChanges(canceled, w.Tx()); !errors.Is(err, context.Canceled) {
				t.Fatal("ignored canceled caller", err)
			}
			if err := w.Commit(); err != nil {
				t.Fatal(err)
			}
			if _, err := TotalChanges(ctx, w.Tx()); !errors.Is(err, sql.ErrTxDone) {
				t.Fatal("completed transaction counter stayed live", err)
			}
		})
	}
}

func TestTotalChangesKeepsSQLFallbackForUntrackedTransactions(t *testing.T) {
	db := testDB(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO rows(value) VALUES('untracked')`); err != nil {
		t.Fatal(err)
	}
	n, err := TotalChanges(context.Background(), tx)
	if err != nil || n != 1 {
		t.Fatalf("untracked count=%d err=%v", n, err)
	}
	if _, err := TotalChanges(context.Background(), nil); err == nil {
		t.Fatal("nil transaction accepted")
	}
}

// A counter read must wait for an already-running statement, just as the old
// SQL meter did. Otherwise Commit can decide there was no change before the
// statement finishes and suppress its publication wakeup.
type blockingCounterConn struct {
	driver.Conn
	entered, release, unsafe chan struct{}
	pending                  atomic.Bool
}

func (c *blockingCounterConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	if q == "INSERT INTO rows(value) VALUES('blocked')" {
		c.pending.Store(true)
		close(c.entered)
		<-c.release
		defer c.pending.Store(false)
	}
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, q, args)
}
func (c *blockingCounterConn) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	stmt, err := c.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, q)
	if err == nil && q == "INSERT INTO rows(value) VALUES('blocked')" {
		// Force the observed cache's supported ExecContext fallback through
		// the connection barrier rather than its prepared-statement path.
		return struct{ driver.Stmt }{stmt}, nil
	}
	return stmt, err
}

func (c *blockingCounterConn) BeginTx(ctx context.Context, o driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, o)
}
func (c *blockingCounterConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, q, args)
}
func (c *blockingCounterConn) TotalChanges() (int64, bool) {
	if c.pending.Load() {
		select {
		case c.unsafe <- struct{}{}:
		default:
		}
	}
	return c.Conn.(interface{ TotalChanges() (int64, bool) }).TotalChanges()
}

func TestCommitCounterWaitsForInFlightStatement(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	c, err := writerHandle(db).Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var blocked *blockingCounterConn
	err = c.Raw(func(raw any) error {
		observed := raw.(*observedConn)
		blocked = &blockingCounterConn{Conn: observed.inner, entered: make(chan struct{}), release: make(chan struct{}), unsafe: make(chan struct{}, 1)}
		observed.inner = blocked
		return nil
	})
	c.Close()
	if err != nil {
		t.Fatal(err)
	}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(blocked.release) }) }
	defer release()
	signal := worker.NewSignal()
	defer WakeOnTables(signal, "rows")()
	w, err := Begin(ctx, db, ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { release(); w.Rollback() }()
	committing := make(chan struct{})
	if err := BeforeCommit(w.Tx(), func() error { close(committing); return nil }); err != nil {
		t.Fatal(err)
	}
	executed := make(chan error, 1)
	go func() {
		_, err := w.Tx().ExecContext(ctx, "INSERT INTO rows(value) VALUES('blocked')")
		executed <- err
	}()
	select {
	case <-blocked.entered:
	case err := <-executed:
		t.Fatalf("statement missed the barrier: %v", err)
	}
	committed := make(chan error, 1)
	go func() { committed <- w.Commit() }()
	<-committing
	select {
	case <-blocked.unsafe:
		t.Error("native counter bypassed the running statement's SQL lock")
	case <-time.After(20 * time.Millisecond):
	}
	release()
	if err := <-executed; err != nil {
		t.Fatal(err)
	}
	if err := <-committed; err != nil {
		t.Fatal(err)
	}
	if !signal.Take() {
		t.Fatal("a committed in-flight write lost its publication wakeup")
	}
}

func TestTotalChangesPreservesUnderlyingTransactionLifetime(t *testing.T) {
	for _, kind := range []string{"canceled", "committed", "rolled-back"} {
		t.Run(kind, func(t *testing.T) {
			db := testDB(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			w, err := Begin(ctx, db, ClassInteractive)
			if err != nil {
				t.Fatal(err)
			}
			defer w.Rollback()
			if kind == "committed" {
				err = w.Tx().Commit()
			} else {
				if kind == "canceled" {
					cancel()
				}
				// Finish (or join) database/sql's canceled-transaction rollback
				// without retiring the outer Write and its connection yet.
				err = w.Tx().Rollback()
			}
			if err != nil && !errors.Is(err, sql.ErrTxDone) {
				t.Fatal(err)
			}
			if _, err := TotalChanges(context.Background(), w.Tx()); !errors.Is(err, sql.ErrTxDone) {
				t.Fatal("revived ended transaction counter", err)
			}
		})
	}
}

// Signal entry to database/sql's transaction admission, so the borrowed-row
// owner can release independently. No sleeps or driver-retirement assumption.
type counterAdmissionContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (c *counterAdmissionContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Done()
}

func TestTotalChangesRejectsLogicalEndBeforeBorrowedRowsRetire(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "canceled"}[canceled], func(t *testing.T) {
			db := testDB(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			w, err := Begin(ctx, db, ClassInteractive)
			if err != nil {
				t.Fatal(err)
			}
			defer w.Rollback()
			if _, err := w.Tx().Exec(`INSERT INTO rows(value) VALUES('borrowed')`); err != nil {
				t.Fatal(err)
			}
			rows, err := w.Tx().Query(`SELECT value FROM rows`)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			if !rows.Next() {
				t.Fatal(rows.Err())
			}
			var borrowed sql.RawBytes
			if err := rows.Scan(&borrowed); err != nil {
				t.Fatal(err)
			}
			if canceled {
				cancel()
			}
			stopped := make(chan error, 2)
			go func() { stopped <- w.Tx().Rollback() }()
			go func() { stopped <- w.Tx().Rollback() }()
			if err := <-stopped; !errors.Is(err, sql.ErrTxDone) {
				rows.Close()
				<-stopped
				t.Fatal("missing logical-end marker", err)
			}
			entry := &counterAdmissionContext{Context: context.Background(), entered: make(chan struct{})}
			counted := make(chan error, 1)
			go func() { _, err := TotalChanges(entry, w.Tx()); counted <- err }()
			select {
			case err = <-counted:
				// A direct Raw getter returns here before consulting the Tx.
				rows.Close()
			case <-entry.entered:
				// Like a SQL read, a correctly fenced meter can wait behind the
				// borrowed row until its owner releases it. It must then refuse.
				rows.Close()
				err = <-counted
			}
			<-stopped
			if !errors.Is(err, sql.ErrTxDone) {
				t.Fatal("counter revived a logically ended transaction", err)
			}
		})
	}
}
