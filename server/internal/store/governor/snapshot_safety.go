package governor

import (
	"context"
	"database/sql/driver"
	"errors"
	"sync"
)

// Native panics cross database/sql as errors, so its borrow is released before
// Read rethrows the original value. Never format arbitrary panic payloads.
type snapshotDriverPanic struct {
	phase string
	value any
}

func (e *snapshotDriverPanic) Error() string { return "snapshot driver panic: " + e.phase }
func snapshotCall[T any](c *snapshotOwnedConn, phase string, call func() (T, error)) (out T, err error) {
	defer func() {
		if value := recover(); value != nil {
			c.safetyMu.Lock()
			if c.fault == nil {
				c.fault = &snapshotDriverPanic{phase, value}
			}
			err = c.fault
			c.safetyMu.Unlock()
		}
	}()
	return call()
}
func (c *snapshotOwnedConn) panicError() *snapshotDriverPanic {
	c.safetyMu.Lock()
	defer c.safetyMu.Unlock()
	return c.fault
}

type snapshotCloser struct {
	c      *snapshotOwnedConn
	row    bool
	close  func() error
	mu     sync.Mutex
	closed bool
}

func (c *snapshotOwnedConn) track(row bool, close func() error) *snapshotCloser {
	r := &snapshotCloser{c: c, row: row, close: close}
	c.safetyMu.Lock()
	if c.resources == nil {
		c.resources = make(map[*snapshotCloser]struct{})
	}
	c.resources[r] = struct{}{}
	c.safetyMu.Unlock()
	return r
}
func (r *snapshotCloser) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	_, err := snapshotCall(r.c, "resource close", func() (struct{}, error) { return struct{}{}, r.close() })
	if err == nil {
		r.closed = true
		r.c.safetyMu.Lock()
		delete(r.c.resources, r)
		r.c.safetyMu.Unlock()
	}
	return err
}
func (c *snapshotOwnedConn) closeResources() error {
	c.safetyMu.Lock()
	pending := make([]*snapshotCloser, 0, len(c.resources))
	for r := range c.resources {
		pending = append(pending, r)
	}
	c.safetyMu.Unlock()
	var err error
	for _, row := range []bool{true, false} {
		for _, r := range pending {
			if r.row == row {
				err = errors.Join(err, r.Close())
			}
		}
		// Do not finalize a statement beneath rows whose cleanup is unproven.
		if err != nil {
			return err
		}
	}
	return nil
}

type snapshotOwnedStmt struct {
	driver.Stmt
	c     *snapshotOwnedConn
	close *snapshotCloser
}

func (s *snapshotOwnedStmt) Close() error { return s.close.Close() }
func (s *snapshotOwnedStmt) NumInput() int {
	n, err := snapshotCall(s.c, "num input", func() (int, error) { return s.Stmt.NumInput(), nil })
	if err != nil {
		return -1
	}
	return n
}
func (s *snapshotOwnedStmt) Exec(args []driver.Value) (driver.Result, error) {
	return snapshotCall(s.c, "statement exec", func() (driver.Result, error) { return s.Stmt.Exec(args) })
}
func (s *snapshotOwnedStmt) Query(args []driver.Value) (driver.Rows, error) {
	return s.query(func() (driver.Rows, error) { return s.Stmt.Query(args) })
}
func (s *snapshotOwnedStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	return s.query(func() (driver.Rows, error) {
		native, ok := s.Stmt.(driver.StmtQueryContext)
		if !ok {
			return nil, errors.New("snapshot statement lacks context query")
		}
		return native.QueryContext(ctx, args)
	})
}
func (s *snapshotOwnedStmt) query(call func() (driver.Rows, error)) (driver.Rows, error) {
	if fault := s.c.panicError(); fault != nil {
		return nil, fault
	}
	rows, err := snapshotCall(s.c, "statement query", call)
	if rows == nil {
		return nil, err
	}
	r := &snapshotOwnedRows{Rows: rows, c: s.c, close: s.c.track(true, rows.Close)}
	columns, metadata := snapshotCall(s.c, "columns", func() ([]string, error) { return rows.Columns(), nil })
	if err != nil || metadata != nil {
		return nil, errors.Join(err, metadata, r.Close())
	}
	r.columns = append([]string(nil), columns...)
	return r, nil
}

type snapshotOwnedRows struct {
	driver.Rows
	c          *snapshotOwnedConn
	close      *snapshotCloser
	columns    []string
	afterClose func() error
}

func (r *snapshotOwnedRows) Columns() []string { return r.columns }
func (r *snapshotOwnedRows) Next(values []driver.Value) error {
	_, err := snapshotCall(r.c, "next", func() (struct{}, error) { return struct{}{}, r.Rows.Next(values) })
	return err
}
func (r *snapshotOwnedRows) Close() error {
	err := r.close.Close()
	if err == nil && r.afterClose != nil {
		err = r.afterClose()
	}
	return err
}
