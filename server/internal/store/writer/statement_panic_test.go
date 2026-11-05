package writer

import (
	"context"
	"database/sql/driver"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

type panicScanner struct{ value any }

func (s panicScanner) Scan(any) error { panic(s.value) }

type queryPanicConn struct {
	testDriverConn
	mode               string
	primary, secondary any
	closed             *atomic.Int64
	clockFault         *atomic.Bool
}

func (c *queryPanicConn) ExecContext(ctx context.Context, q string, a []driver.NamedValue) (driver.Result, error) {
	if q == "COMMIT" && c.mode == "commit-clock" {
		if _, err := c.testDriverConn.ExecContext(ctx, q, a); err != nil {
			return nil, err
		}
		c.clockFault.Store(true)
		panic(c.primary)
	}
	if strings.Contains(q, "UPDATE search_revision SET revision=?") && c.mode == "exec" {
		panic(c.primary)
	}
	r, err := c.testDriverConn.ExecContext(ctx, q, a)
	if r != nil && (c.mode == "affected" || c.mode == "insertid") {
		r = &queryPanicResult{r, c.mode, c.primary}
	}
	return r, err
}

type queryPanicResult struct {
	driver.Result
	mode  string
	value any
}

func (r *queryPanicResult) RowsAffected() (int64, error) {
	if r.mode == "affected" {
		panic(r.value)
	}
	return r.Result.RowsAffected()
}
func (r *queryPanicResult) LastInsertId() (int64, error) {
	if r.mode == "insertid" {
		panic(r.value)
	}
	return r.Result.LastInsertId()
}

func (c *queryPanicConn) QueryContext(ctx context.Context, q string, a []driver.NamedValue) (driver.Rows, error) {
	if !strings.Contains(q, "SELECT revision FROM search_revision") {
		return c.testDriverConn.QueryContext(ctx, q, a)
	}
	if c.mode == "query" || c.mode == "queryRow" {
		panic(c.primary)
	}
	if c.mode == "prepare" {
		return nil, driver.ErrSkip
	}
	rows, err := c.testDriverConn.QueryContext(ctx, q, a)
	if err != nil {
		return nil, err
	}
	return &queryPanicRows{Rows: rows, mode: c.mode, primary: c.primary, secondary: c.secondary}, nil
}
func (c *queryPanicConn) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	st, err := c.testDriverConn.PrepareContext(ctx, q)
	if err != nil {
		return nil, err
	}
	if c.mode == "prepare" && strings.Contains(q, "SELECT revision FROM search_revision") {
		return &queryPanicStmt{Stmt: st, value: c.primary}, nil
	}
	return st, nil
}
func (c *queryPanicConn) Close() error { err := c.testDriverConn.Close(); c.closed.Add(1); return err }

type queryPanicStmt struct {
	driver.Stmt
	value any
}

func (s *queryPanicStmt) QueryContext(context.Context, []driver.NamedValue) (driver.Rows, error) {
	panic(s.value)
}

type queryPanicRows struct {
	driver.Rows
	mode               string
	primary, secondary any
}

func (r *queryPanicRows) Columns() []string {
	if r.mode == "columns" {
		panic(r.primary)
	}
	return r.Rows.Columns()
}
func (r *queryPanicRows) Next(v []driver.Value) error {
	if r.mode == "next" {
		panic(r.primary)
	}
	return r.Rows.Next(v)
}
func (r *queryPanicRows) Close() error {
	if r.secondary != nil {
		panic(r.secondary)
	}
	if r.mode == "close" {
		panic(r.primary)
	}
	return r.Rows.Close()
}

func TestStatementPanicHardTimeout(t *testing.T) {
	for _, mode := range []string{"query", "queryRow", "scanRow", "scanRows", "prepare", "next", "columns", "close", "scanRow-secondary", "query-retire", "exec", "affected", "insertid", "commit-clock"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStatementPanicChild$", "-test.count=1", "-test.timeout=12s")
			cmd.Env = append(os.Environ(), "PORTICO_WRITER_PANIC_CASE="+mode)
			cmd.WaitDelay = time.Second
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("isolated %s (timeout=%v): %v\n%s", mode, ctx.Err(), err, output)
			}
		})
	}
}
func TestStatementPanicChild(t *testing.T) {
	mode := os.Getenv("PORTICO_WRITER_PANIC_CASE")
	if mode == "" {
		t.Skip("subprocess control")
	}
	cfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 256, Synchronous: sqliteconn.Normal}
	path := filepath.Join(t.TempDir(), "tiny.sqlite")
	connector, err := sqliteconn.Connector(path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	w := &executor{config: cfg, now: time.Now}
	var closes atomic.Int64
	var clockFault atomic.Bool
	primary := errors.New("original body panic")
	secondary := errors.New("secondary cleanup panic")
	var native *ownedPhysicalConn
	w.openPool(connector, func(conn driver.Conn) driver.Conn {
		native = conn.(*ownedPhysicalConn)
		which := mode
		var second any
		if mode == "scanRow-secondary" {
			which = "scanRow"
			second = secondary
		}
		if mode == "query-retire" {
			which = "query"
		}
		return &queryPanicConn{testDriverConn: conn.(testDriverConn), mode: which, primary: primary, secondary: second, closed: &closes, clockFault: &clockFault}
	})
	defer w.close()
	if _, err := w.db.ExecContext(t.Context(), "CREATE TABLE search_revision(singleton INTEGER PRIMARY KEY,revision INTEGER NOT NULL);INSERT INTO search_revision VALUES(1,1)"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(time.Hour)
	w.now = func() time.Time {
		if clockFault.Load() {
			panic(secondary)
		}
		return now
	}
	custody := &testCustody{}
	var tx *transaction
	wantState := rolledBack
	if mode == "commit-clock" {
		wantState = commitUnknown
	}
	custody.retireHook = func() {
		if tx == nil || !tx.done || tx.result.state != wantState || closes.Load() != 1 {
			t.Errorf("retirement before frozen outcome/physical return: tx%+v closes%d", tx, closes.Load())
		}
		if mode == "query-retire" {
			panic(secondary)
		}
	}
	tx, err = w.begin(t.Context(), InventoryAccept, testRecipe(), custody)
	if err != nil {
		t.Fatal(err)
	}
	registry, steps := turnStatements(t)
	turn, err := newCommandTurn(tx, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	// A failed query/scan must not commit an earlier synthetic effect. This
	// tiny frame fixture is not a registered domain command/receipt.
	if _, err := tx.conn.ExecContext(tx.ctx, "UPDATE search_revision SET revision=revision+10 WHERE singleton=1"); err != nil {
		t.Fatal(err)
	}
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		defer tx.rollback(nil)
		switch mode {
		case "commit-clock":
			_, _ = tx.commit()
		case "exec", "affected", "insertid":
			result, err := turn.exec(t.Context(), steps[1], int64(2), int64(1))
			if err != nil {
				t.Fatal(err)
			}
			if mode == "affected" {
				_, _ = result.RowsAffected()
			} else if mode == "insertid" {
				_, _ = result.LastInsertId()
			}
		case "query", "query-retire":
			_, _ = turn.query(t.Context(), steps[0], int64(1))
		case "scanRows", "next", "columns", "close":
			rows, err := turn.query(t.Context(), steps[0], int64(1))
			if err != nil {
				t.Fatal(err)
			}
			if mode == "columns" {
				_, _ = rows.Columns()
			} else if mode == "close" {
				_ = rows.Close()
			} else if rows.Next() {
				_ = rows.Scan(panicScanner{primary})
			}
		default:
			row, err := turn.queryRow(t.Context(), steps[0], int64(1))
			if err != nil {
				t.Fatal(err)
			}
			if mode == "scanRow" || mode == "scanRow-secondary" {
				_ = row.Scan(panicScanner{primary})
			} else {
				var revision int64
				_ = row.Scan(&revision)
			}
		}
	}()
	if recovered != primary {
		t.Fatalf("primary replaced or swallowed: got%v want%v", recovered, primary)
	}
	if !tx.done || tx.result.state != wantState || closes.Load() != 1 || custody.returns != 1 {
		t.Fatalf("stranded terminal/custody: done%v state%v closes%d retirement%d", tx.done, tx.result.state, closes.Load(), custody.returns)
	}
	if !w.mu.TryLock() {
		t.Fatal("writer mutex stranded")
	}
	w.mu.Unlock()
	if stats := w.db.Stats(); stats.InUse != 0 || stats.OpenConnections != 0 {
		t.Fatalf("SQL borrow stranded: %+v", stats)
	}
	if inventory, ok := any(native).(interface{ nativeResourcesRemaining() int }); ok && inventory.nativeResourcesRemaining() != 0 {
		t.Fatalf("native rows/statements stranded: %d", inventory.nativeResourcesRemaining())
	}
	wantRevision := 1
	if mode == "commit-clock" {
		wantRevision = 11
	}
	if got := readRevision(t, path); got != wantRevision {
		t.Fatalf("wrong physical effect after panic: got%d want%d", got, wantRevision)
	}
	if mode == "scanRow-secondary" || mode == "query-retire" || mode == "commit-clock" {
		found := false
		for _, p := range tx.cleanupPanics {
			if p.value == secondary {
				found = true
			}
		}
		if !found {
			t.Fatalf("secondary lost: %+v", tx.cleanupPanics)
		}
	}
}
