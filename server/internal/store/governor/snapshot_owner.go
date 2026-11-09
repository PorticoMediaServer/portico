package governor

import (
	"context"
	"database/sql/driver"
	"errors"
	"sync"
)

// This wrapper owns disposal, not connection policy. The supplied connector is
// the shared sqliteconn connector; all settings still come from it and Assert.
// Ownership exists before database/sql can receive ErrBadConn and discard.
type snapshotOwnedConnector struct {
	driver.Connector
	mu     sync.Mutex
	failed map[*snapshotOwnedConn]error
}

func (c *snapshotOwnedConnector) Connect(ctx context.Context) (driver.Conn, error) {
	c.mu.Lock()
	blocked := len(c.failed) != 0
	c.mu.Unlock()
	if blocked {
		return nil, errors.New("snapshot physical disposal pending")
	}
	raw, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &snapshotOwnedConn{Conn: raw, owner: c}, nil
}
func (c *snapshotOwnedConnector) retry() error {
	c.mu.Lock()
	pending := make([]*snapshotOwnedConn, 0, len(c.failed))
	for p := range c.failed {
		pending = append(pending, p)
	}
	c.mu.Unlock()
	var err error
	for _, p := range pending {
		err = errors.Join(err, p.Close())
	}
	return err
}

type snapshotOwnedConn struct {
	driver.Conn
	owner     *snapshotOwnedConnector
	closeMu   sync.Mutex
	closed    bool
	safetyMu  sync.Mutex
	fault     *snapshotDriverPanic
	resources map[*snapshotCloser]struct{}
	keeper    *CheckpointKeeper // immutable once assigned before physical publication
}

func (c *snapshotOwnedConn) Close() error {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	if c.closed {
		return nil
	}
	var err error
	if c.keeper != nil {
		err = c.keeper.beforeNativeClose()
	}
	if err == nil {
		err = c.closeResources()
	}
	if err == nil {
		_, err = snapshotCall(c, "connection close", func() (struct{}, error) { return struct{}{}, c.Conn.Close() })
	}
	c.owner.mu.Lock()
	defer c.owner.mu.Unlock()
	if err != nil {
		c.owner.failed[c] = err
	} else {
		c.closed = true
		delete(c.owner.failed, c)
	}
	return err
}

func (c *snapshotOwnedConn) SQLiteMainFileIdentity() (uint64, uint64, error) {
	value, err := snapshotCall(c, "main file identity", func() ([2]uint64, error) {
		getter, ok := c.Conn.(interface {
			SQLiteMainFileIdentity() (uint64, uint64, error)
		})
		if !ok {
			return [2]uint64{}, errors.New("native main-file identity unavailable")
		}
		device, inode, err := getter.SQLiteMainFileIdentity()
		return [2]uint64{device, inode}, err
	})
	return value[0], value[1], err
}
func (c *snapshotOwnedConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	if native, ok := c.Conn.(driver.ExecerContext); ok {
		result, err := snapshotCall(c, "exec", func() (driver.Result, error) { return native.ExecContext(ctx, q, args) })
		return result, err
	}
	return nil, driver.ErrSkip
}
func (c *snapshotOwnedConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	// Retain the prepared handle before stepping; Conn.QueryContext would hide it
	// if a scalar panicked before returning Rows.
	st, err := c.PrepareContext(ctx, q)
	if err != nil {
		return nil, err
	}
	rows, err := st.(*snapshotOwnedStmt).QueryContext(ctx, args)
	if err != nil {
		return nil, errors.Join(err, c.closeResources())
	}
	rows.(*snapshotOwnedRows).afterClose = st.Close
	return rows, nil
}
func (c *snapshotOwnedConn) Prepare(q string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), q)
}
func (c *snapshotOwnedConn) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	if fault := c.panicError(); fault != nil {
		return nil, fault
	}
	st, err := snapshotCall(c, "prepare", func() (driver.Stmt, error) {
		if native, ok := c.Conn.(driver.ConnPrepareContext); ok {
			return native.PrepareContext(ctx, q)
		}
		return c.Conn.Prepare(q)
	})
	// A failed prepare may return a typed-nil statement behind a non-nil driver
	// interface. Tracking it would close a nil handle during snapshot cleanup, so
	// only a successful prepare may own native statement state.
	if err != nil {
		return nil, err
	}
	if st != nil {
		st = &snapshotOwnedStmt{Stmt: st, c: c, close: c.track(false, st.Close)}
	}
	return st, nil
}
func (c *snapshotOwnedConn) ResetSession(ctx context.Context) error {
	if native, ok := c.Conn.(driver.SessionResetter); ok {
		return native.ResetSession(ctx)
	}
	return nil
}
func (c *snapshotOwnedConn) IsValid() bool {
	if native, ok := c.Conn.(driver.Validator); ok {
		return native.IsValid()
	}
	return true
}
func (c *snapshotOwnedConn) ReadBudgetSupported() bool {
	native, ok := c.Conn.(interface{ ReadBudgetSupported() bool })
	return ok && native.ReadBudgetSupported()
}

// Snapshot owners expose only read-only limit queries after physical Connect.
func (c *snapshotOwnedConn) SQLiteLimit(id int, newVal int) (int, error) {
	if newVal != -1 {
		return 0, errors.New("snapshot pool limits are fixed")
	}
	native, ok := c.Conn.(interface{ SQLiteLimit(int, int) (int, error) })
	if !ok {
		return 0, errors.New("native limit query unsupported")
	}
	return native.SQLiteLimit(id, -1)
}

// SQLiteDQS forwards the helper's read-only policy evidence. Enforcement and
// physical initialization remain in sqliteconn; there is no setter here.
func (c *snapshotOwnedConn) SQLiteDQS() (ddl int, dml int, err error) {
	native, ok := c.Conn.(interface{ SQLiteDQS() (int, int, error) })
	if !ok {
		return 0, 0, errors.New("native DQS getter unsupported")
	}
	return native.SQLiteDQS()
}
