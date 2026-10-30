package sqlite

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"

	sqlite3 "modernc.org/sqlite/lib"
)

func progressTestConn(t *testing.T, tick func(int64)) *conn {
	t.Helper()
	d := &Driver{}
	if err := d.RegisterScalarFunction("progress_tick", 1, func(_ *FunctionContext, args []driver.Value) (driver.Value, error) {
		n := args[0].(int64)
		if tick != nil {
			tick(n)
		}
		return n, nil
	}); err != nil {
		t.Fatal(err)
	}
	opened, err := d.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	c := opened.(*conn)
	t.Cleanup(func() { c.Close() })
	return c
}

func TestCanceledIdleInterruptCannotBeLostAtVMActivation(t *testing.T) {
	var iterations atomic.Int64
	c := progressTestConn(t, func(_ int64) { iterations.Add(1) })
	s, err := newStmt(c, `WITH RECURSIVE n(x) AS (SELECT progress_tick(1) UNION ALL SELECT progress_tick(x+1) FROM n WHERE x<50000) SELECT sum(x) FROM n`)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	scope := c.watchProgress(ctx)
	// Reproduce cancellation while the VM is idle, after parameter preparation
	// but before execution. SQLite can clear this idle interrupt on activation.
	cancel()
	c.interrupt(c.db)
	_, err = c.step(s.pstmt)
	scope.restore()
	var sqliteErr *Error
	if !errors.As(err, &sqliteErr) || sqliteErr.Code()&0xff != sqlite3.SQLITE_INTERRUPT {
		t.Fatalf("idle cancellation lost: %v, iterations=%d", err, iterations.Load())
	}
	if iterations.Load() > cancellationProgressOps {
		t.Fatalf("canceled VM exhausted work: %d", iterations.Load())
	}
	c.reset(s.pstmt)
	rows, err := c.QueryContext(context.Background(), `SELECT 42`, nil)
	if err != nil {
		t.Fatal("next request inherited canceled handler", err)
	}
	var value [1]driver.Value
	if err := rows.Next(value[:]); err != nil || value[0] != int64(42) {
		t.Fatal(value, err)
	}
	rows.Close()
	token := c.progressToken
	s.Close()
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, exists := progressStates.Load(token); exists {
		t.Fatal("connection closure retained callback state")
	}
}

func TestRowIterationCancellationStopsWorkAfterQueryReturns(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var iterations atomic.Int64
	c := progressTestConn(t, func(_ int64) { iterations.Add(1); once.Do(func() { close(started); <-release }) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const query = `WITH RECURSIVE n(x) AS (SELECT progress_tick(1) UNION ALL SELECT progress_tick(x+1) FROM n WHERE x<50000) SELECT 1 UNION ALL SELECT sum(x) FROM n`
	rows, err := c.QueryContext(ctx, query, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if rows != nil {
			rows.Close()
		}
	}()
	var value [1]driver.Value
	if err := rows.Next(value[:]); err != nil || value[0] != int64(1) {
		t.Fatal(value, err)
	}
	result := make(chan error, 1)
	go func() { result <- rows.Next(value[:]) }()
	<-started
	cancel()
	close(release)
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("later Next ignored cancellation: %v", err)
	}
	if iterations.Load() > cancellationProgressOps {
		t.Fatalf("canceled Next exhausted work: %d", iterations.Load())
	}
	rows.Close()
	rows = nil
	next, err := c.QueryContext(context.Background(), `SELECT 9`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := next.Next(value[:]); err != nil || value[0] != int64(9) {
		t.Fatal(value, err)
	}
	if err := next.Next(value[:]); err != io.EOF {
		t.Fatal(err)
	}
	next.Close()
}

func TestQueryAndExecProgressErrorsMatchCallerCancellation(t *testing.T) {
	for _, mode := range []string{"query", "exec", "query-script", "exec-script"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var once sync.Once
			var iterations atomic.Int64
			c := progressTestConn(t, func(_ int64) { iterations.Add(1); once.Do(cancel) })
			query := `WITH RECURSIVE n(x) AS (SELECT progress_tick(1) UNION ALL SELECT progress_tick(x+1) FROM n WHERE x<50000) SELECT sum(x) FROM n`
			if mode == "query-script" || mode == "exec-script" {
				query += "; SELECT 7"
			}
			var err error
			if mode == "query" || mode == "query-script" {
				_, err = c.QueryContext(ctx, query, nil)
			} else {
				_, err = c.ExecContext(ctx, query, nil)
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("progress cancellation leaked SQLite status as caller error: %v", err)
			}
			if iterations.Load() > cancellationProgressOps {
				t.Fatalf("canceled VM exhausted work: %d", iterations.Load())
			}
			next, err := c.QueryContext(context.Background(), `SELECT 99`, nil)
			if err != nil {
				t.Fatal(err)
			}
			var value [1]driver.Value
			if err := next.Next(value[:]); err != nil || value[0] != int64(99) {
				t.Fatal(value, err)
			}
			next.Close()
		})
	}
}

func TestProgressTokenWrapCannotReplaceAnotherConnectionsState(t *testing.T) {
	saved := nextProgressToken.Load()
	defer nextProgressToken.Store(saved)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := progressTestConn(t, nil)
	nextProgressToken.Store(^uint64(0))
	firstScope := first.watchProgress(ctx)
	if first.progressToken == 0 {
		t.Fatal("reserved zero token used")
	}
	second := progressTestConn(t, nil)
	nextProgressToken.Store(^uint64(0))
	secondScope := second.watchProgress(ctx)
	if second.progressToken == 0 || second.progressToken == first.progressToken {
		t.Fatal("wrapped token replaced a live connection")
	}
	firstScope.restore()
	secondScope.restore()
	firstToken, secondToken := first.progressToken, second.progressToken
	first.Close()
	if _, found := progressStates.Load(firstToken); found {
		t.Fatal("first token leaked")
	}
	if state, found := progressStates.Load(secondToken); !found || state != second.progress {
		t.Fatal("closing first erased second callback state")
	}
	second.Close()
}

func TestVMOperationsCountEveryFinishedStatementExactly(t *testing.T) {
	c := progressTestConn(t, nil)
	if c.VMOperations() != 0 {
		t.Fatalf("unused connection counted work: %d", c.VMOperations())
	}
	// Many tiny statements: each is far below any sampling interval, and
	// every one must still count.
	if _, err := c.ExecContext(context.Background(), `CREATE TABLE t(x)`, nil); err != nil {
		t.Fatal(err)
	}
	before := c.VMOperations()
	for i := 0; i < 200; i++ {
		if _, err := c.ExecContext(context.Background(), `INSERT INTO t VALUES (?)`, []driver.NamedValue{{Ordinal: 1, Value: int64(i)}}); err != nil {
			t.Fatal(err)
		}
	}
	small := c.VMOperations() - before
	if small < 200*5 {
		t.Fatalf("200 small inserts counted %d VM steps", small)
	}
	// A read's steps count once, whether or not the reader asked for them.
	before = c.VMOperations()
	r, err := c.QueryContext(context.Background(), `WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<20000) SELECT sum(x) FROM n`, nil)
	if err != nil {
		t.Fatal(err)
	}
	dest := make([]driver.Value, 1)
	for r.Next(dest) == nil {
	}
	reported := r.(*rows).VMSteps()
	r.Close()
	if spent := c.VMOperations() - before; spent < 20000 || spent != reported {
		t.Fatalf("a 20,000-row recursion counted %d (reported %d)", spent, reported)
	}
}
