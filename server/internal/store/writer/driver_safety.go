package writer

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"sync"
)

// A panic crosses database/sql only as an error. Rethrowing is the private
// frame's job AFTER database/sql has released its borrow/row/statement locks.
type driverPanicError struct {
	phase string
	value any
}

func (e *driverPanicError) Error() string { return "writer driver panic: " + e.phase }
func (e *driverPanicError) Unwrap() error {
	if err, ok := e.value.(error); ok {
		return err
	}
	return nil
}

type driverPanicLog struct {
	mu     sync.Mutex
	events []*driverPanicError
}

func (l *driverPanicLog) add(phase string, value any) *driverPanicError {
	e := &driverPanicError{phase, value}
	l.mu.Lock()
	l.events = append(l.events, e)
	first := l.events[0]
	l.mu.Unlock()
	return first
}
func (l *driverPanicLog) take() []*driverPanicError {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := l.events
	l.events = nil
	return out
}

type driverSafety struct {
	mu        sync.Mutex
	log       *driverPanicLog
	pending   *driverPanicError
	resources map[*nativeCloser]bool
}

func (c *ownedPhysicalConn) nativeResourcesRemaining() int {
	c.scope.mu.Lock()
	defer c.scope.mu.Unlock()
	return len(c.scope.resources)
}

func (s *driverSafety) fault(phase string, value any) error {
	e := s.log.add(phase, value)
	s.mu.Lock()
	if s.pending == nil {
		s.pending = e
	}
	first := s.pending
	s.mu.Unlock()
	return first
}
func (s *driverSafety) blocked() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending != nil {
		return s.pending
	}
	return nil
}
func safeDriver[T any](s *driverSafety, phase string, work bool, call func() (T, error)) (out T, err error) {
	if work {
		if err = s.blocked(); err != nil {
			return out, err
		}
	}
	defer func() {
		if value := recover(); value != nil {
			err = s.fault(phase, value)
		}
	}()
	return call()
}
func safeVoid(s *driverSafety, phase string, work bool, call func() error) error {
	_, err := safeDriver(s, phase, work, func() (struct{}, error) { return struct{}{}, call() })
	return err
}
func safeValue[T any](s *driverSafety, phase string, fallback T, call func() T) T {
	value, err := safeDriver(s, phase, true, func() (T, error) { return call(), nil })
	if err != nil {
		return fallback
	}
	return value
}

// Native resource ownership is below optional driver instrumentation. Closing
// an outer Rows/Stmt cannot hide an unclosed native object if it panics first.
type nativeCloser struct {
	scope *driverSafety
	row   bool
	close func() error
	once  sync.Once
	err   error
}

func (s *driverSafety) own(row bool, close func() error) *nativeCloser {
	c := &nativeCloser{scope: s, row: row, close: close}
	s.mu.Lock()
	if s.resources == nil {
		s.resources = make(map[*nativeCloser]bool)
	}
	s.resources[c] = true
	s.mu.Unlock()
	return c
}
func (c *nativeCloser) Close() error {
	c.once.Do(func() {
		c.err = safeVoid(c.scope, "native resource close", false, c.close)
		c.scope.mu.Lock()
		delete(c.scope.resources, c)
		c.scope.mu.Unlock()
	})
	return c.err
}
func (s *driverSafety) closeAll() error {
	s.mu.Lock()
	list := make([]*nativeCloser, 0, len(s.resources))
	for c := range s.resources {
		list = append(list, c)
	}
	s.mu.Unlock()
	var err error
	for _, row := range []bool{true, false} {
		for _, c := range list {
			if c.row == row {
				err = errors.Join(err, c.Close())
			}
		}
	}
	return err
}

type safeWriterConn struct {
	writerDriverConn
	scope *driverSafety
}

func (c *safeWriterConn) SQLiteDQS() (ddl, dml int, err error) {
	values, err := safeDriver(c.scope, "DQS", true, func() ([2]int, error) {
		ddl, dml, err := c.writerDriverConn.SQLiteDQS()
		return [2]int{ddl, dml}, err
	})
	return values[0], values[1], err
}

func (c *safeWriterConn) Prepare(q string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), q)
}
func (c *safeWriterConn) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	st, err := safeDriver(c.scope, "prepare", true, func() (driver.Stmt, error) { return c.writerDriverConn.PrepareContext(ctx, q) })
	// A failed prepare may return a typed-nil statement behind a non-nil driver
	// interface. Wrapping it would register a closer that dereferences nil, so only
	// a successful prepare may own native statement state.
	if err != nil {
		return nil, err
	}
	if st != nil {
		st = &safeWriterStmt{Stmt: st, scope: c.scope}
	}
	return st, nil
}
func (c *safeWriterConn) ExecContext(ctx context.Context, q string, a []driver.NamedValue) (driver.Result, error) {
	r, err := safeDriver(c.scope, "exec", true, func() (driver.Result, error) { return c.writerDriverConn.ExecContext(ctx, q, a) })
	if r != nil {
		r = &safeWriterResult{r, c.scope}
	}
	return r, err
}
func (c *safeWriterConn) QueryContext(ctx context.Context, q string, a []driver.NamedValue) (driver.Rows, error) {
	r, err := safeDriver(c.scope, "query", true, func() (driver.Rows, error) { return c.writerDriverConn.QueryContext(ctx, q, a) })
	if r != nil {
		var metadataErr error
		r, metadataErr = newSafeWriterRows(r, c.scope, false)
		err = errors.Join(err, metadataErr)
	}
	return r, err
}
func (c *safeWriterConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}
func (c *safeWriterConn) BeginTx(ctx context.Context, o driver.TxOptions) (driver.Tx, error) {
	t, err := safeDriver(c.scope, "begin tx", true, func() (driver.Tx, error) { return c.writerDriverConn.BeginTx(ctx, o) })
	if t != nil {
		t = &safeWriterTx{t, c.scope}
	}
	return t, err
}
func (c *safeWriterConn) Ping(ctx context.Context) error {
	return safeVoid(c.scope, "ping", true, func() error { return c.writerDriverConn.Ping(ctx) })
}
func (c *safeWriterConn) ResetSession(ctx context.Context) error {
	return safeVoid(c.scope, "reset", true, func() error { return c.writerDriverConn.ResetSession(ctx) })
}
func (c *safeWriterConn) IsValid() bool {
	return safeValue(c.scope, "valid", false, c.writerDriverConn.IsValid)
}
func (c *safeWriterConn) Close() error {
	return safeVoid(c.scope, "connection close", false, c.writerDriverConn.Close)
}

type safeWriterStmt struct {
	driver.Stmt
	scope *driverSafety
	owned *nativeCloser
}

func (s *safeWriterStmt) Close() error {
	if s.owned != nil {
		return s.owned.Close()
	}
	return safeVoid(s.scope, "statement close", false, s.Stmt.Close)
}
func (s *safeWriterStmt) NumInput() int { return safeValue(s.scope, "num input", -1, s.Stmt.NumInput) }
func namedValues(a []driver.NamedValue) ([]driver.Value, error) {
	v := make([]driver.Value, len(a))
	for i, x := range a {
		if x.Name != "" {
			return nil, fmt.Errorf("named values forbidden")
		}
		v[i] = x.Value
	}
	return v, nil
}
func legacyNamed(a []driver.Value) []driver.NamedValue {
	v := make([]driver.NamedValue, len(a))
	for i, x := range a {
		v[i] = driver.NamedValue{Ordinal: i + 1, Value: x}
	}
	return v
}
func (s *safeWriterStmt) Exec(a []driver.Value) (driver.Result, error) {
	return s.ExecContext(context.Background(), legacyNamed(a))
}
func (s *safeWriterStmt) Query(a []driver.Value) (driver.Rows, error) {
	return s.QueryContext(context.Background(), legacyNamed(a))
}
func (s *safeWriterStmt) ExecContext(ctx context.Context, a []driver.NamedValue) (driver.Result, error) {
	r, err := safeDriver(s.scope, "statement exec", true, func() (driver.Result, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if x, ok := s.Stmt.(driver.StmtExecContext); ok {
			return x.ExecContext(ctx, a)
		}
		v, err := namedValues(a)
		if err != nil {
			return nil, err
		}
		return s.Stmt.Exec(v)
	})
	if r != nil {
		r = &safeWriterResult{r, s.scope}
	}
	return r, err
}
func (s *safeWriterStmt) QueryContext(ctx context.Context, a []driver.NamedValue) (driver.Rows, error) {
	r, err := safeDriver(s.scope, "statement query", true, func() (driver.Rows, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if x, ok := s.Stmt.(driver.StmtQueryContext); ok {
			return x.QueryContext(ctx, a)
		}
		v, err := namedValues(a)
		if err != nil {
			return nil, err
		}
		return s.Stmt.Query(v)
	})
	if r != nil {
		var metadataErr error
		r, metadataErr = newSafeWriterRows(r, s.scope, s.owned != nil)
		err = errors.Join(err, metadataErr)
	}
	return r, err
}

type safeWriterRows struct {
	driver.Rows
	scope      *driverSafety
	owned      *nativeCloser
	afterClose func() error
	columns    []string
}

// Capture while Query still has an error return. Columns is invariant for a
// statement: never call a driver again from Scan's locked error formatter.
func newSafeWriterRows(rows driver.Rows, scope *driverSafety, owned bool) (driver.Rows, error) {
	r := &safeWriterRows{Rows: rows, scope: scope}
	if owned {
		r.owned = scope.own(true, rows.Close)
	}
	columns, err := safeDriver(scope, "columns", true, func() ([]string, error) { return rows.Columns(), nil })
	if err != nil {
		return nil, errors.Join(err, r.Close())
	}
	r.columns = append([]string(nil), columns...)
	return r, nil
}
func (r *safeWriterRows) Columns() []string {
	return r.columns
}
func (r *safeWriterRows) Next(v []driver.Value) error {
	return safeVoid(r.scope, "next", true, func() error { return r.Rows.Next(v) })
}
func (r *safeWriterRows) Close() error {
	var err error
	if r.owned != nil {
		err = r.owned.Close()
	} else {
		err = safeVoid(r.scope, "rows close", false, r.Rows.Close)
	}
	if r.afterClose != nil {
		err = errors.Join(err, r.afterClose())
	}
	return err
}

type safeWriterResult struct {
	driver.Result
	scope *driverSafety
}

func (r *safeWriterResult) LastInsertId() (int64, error) {
	return safeDriver(r.scope, "last insert id", true, r.Result.LastInsertId)
}
func (r *safeWriterResult) RowsAffected() (int64, error) {
	return safeDriver(r.scope, "rows affected", true, r.Result.RowsAffected)
}

type safeWriterTx struct {
	driver.Tx
	scope *driverSafety
}

func (t *safeWriterTx) Commit() error { return safeVoid(t.scope, "driver commit", true, t.Tx.Commit) }
func (t *safeWriterTx) Rollback() error {
	return safeVoid(t.scope, "driver rollback", false, t.Tx.Rollback)
}

// Own prepared state even for a one-shot query. This prevents a panic during
// execution or an outer Close from hiding native statements from quarantine.
func (c *ownedPhysicalConn) Prepare(q string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), q)
}
func (c *ownedPhysicalConn) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	st, err := safeDriver(c.scope, "native prepare", true, func() (driver.Stmt, error) { return c.writerDriverConn.PrepareContext(ctx, q) })
	// Same typed-nil guard: a failed prepare must never register a native closer it
	// cannot close. The driver error is returned unchanged to the caller.
	if err != nil {
		return nil, err
	}
	if st != nil {
		st = &safeWriterStmt{Stmt: st, scope: c.scope, owned: c.scope.own(false, st.Close)}
	}
	return st, nil
}
func (c *ownedPhysicalConn) QueryContext(ctx context.Context, q string, a []driver.NamedValue) (driver.Rows, error) {
	st, err := c.PrepareContext(ctx, q)
	if err != nil {
		return nil, err
	}
	rows, err := st.(driver.StmtQueryContext).QueryContext(ctx, a)
	if err != nil {
		return nil, errors.Join(err, st.Close())
	}
	rows.(*safeWriterRows).afterClose = st.Close
	return rows, nil
}
func (c *ownedPhysicalConn) ExecContext(ctx context.Context, q string, a []driver.NamedValue) (driver.Result, error) {
	st, err := c.PrepareContext(ctx, q)
	if err != nil {
		return nil, err
	}
	r, err := st.(driver.StmtExecContext).ExecContext(ctx, a)
	return r, errors.Join(err, st.Close())
}

func panicFrom(err error) *driverPanicError {
	var fault *driverPanicError
	if errors.As(err, &fault) {
		return fault
	}
	return nil
}
func (t *transaction) raiseDriverPanic(err error) {
	events := t.writer.driverPanics.take()
	primary := panicFrom(err)
	if primary == nil && len(events) > 0 {
		primary = events[0]
	}
	if primary == nil {
		return
	}
	for _, fault := range events {
		if fault == primary {
			continue
		}
		t.cleanupPanics = append(t.cleanupPanics, terminalPanic{fault.phase, fault.value})
	}
	panic(primary.value)
}
