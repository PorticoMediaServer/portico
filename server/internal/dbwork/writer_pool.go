package dbwork

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"sync/atomic"
)

// splitConnector owns both pool lifetimes. database/sql calls its Close when
// the public handle closes, so tests and failed startup cannot leak writers.
type splitConnector struct {
	dsn    string
	driver *splitDriver
}
type splitDriver struct {
	driver.Driver
	dsn           string
	wal           *walLedger
	writer        *sql.DB
	background    *sql.DB
	closeComplete atomic.Bool
}

func (c *splitConnector) Connect(ctx context.Context) (driver.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.driver.Open(c.dsn)
}
func (c *splitConnector) Driver() driver.Driver { return c.driver }
func (c *splitConnector) Close() error {
	backgroundErr := c.driver.background.Close()
	writerErr := c.driver.writer.Close()
	c.driver.closeComplete.Store(true)
	if backgroundErr != nil {
		return backgroundErr
	}
	return writerErr
}

// ConnectionsDrained reports whether a closed handle has released every
// physical connection, including borrowed writer and background connections.
// The caller must first establish that the public handle is closed. Close can
// return while borrowed connections still hold SQLite file locks.
func ConnectionsDrained(db *sql.DB) bool {
	if db == nil {
		return true
	}
	if db.Stats().OpenConnections != 0 {
		return false
	}
	if d, ok := db.Driver().(*splitDriver); ok {
		return d.closeComplete.Load() && d.writer.Stats().OpenConnections == 0 && d.background.Stats().OpenConnections == 0
	}
	return true
}

func writerHandle(db *sql.DB) *sql.DB {
	if d, ok := db.Driver().(*splitDriver); ok {
		return d.writer
	}
	return db // Handles supplied by embedders still use their own policy.
}

// ReadHandle selects the single-connection background read pool for classified
// background work. The public handle remains the six-connection foreground
// pool. Checkpoint control uses its existing reader-admission bypass and the
// public pool: the background connection may hold the long read that recovery
// must cancel, so waiting for that connection would prevent recovery itself.
func ReadHandle(ctx context.Context, db *sql.DB) *sql.DB {
	if db == nil {
		return nil
	}
	if d, ok := db.Driver().(*splitDriver); ok && ClassFrom(ctx, ClassInteractive).Background() && ctx.Value(checkpointReaderBypassKey{}) == nil {
		return d.background
	}
	return db
}
