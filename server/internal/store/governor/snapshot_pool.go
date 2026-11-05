package governor

import (
	"context"
	"database/sql"
	"database/sql/driver"
)

// Only the constructor ever holds a resize-capable DB handle. Fixed pool/slot
// sizing is required to refuse excess reads before database/sql can queue an
// asynchronous replacement. No idle/lifetime resizing or raw DB is retained.
type snapshotPool struct {
	acquire  func(context.Context) (*sql.Conn, error)
	shutdown func() error
	status   func() sql.DBStats
}

func newSnapshotPool(connector driver.Connector, count int) snapshotPool {
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(count)
	db.SetMaxIdleConns(count)
	return snapshotPool{acquire: db.Conn, shutdown: db.Close, status: db.Stats}
}
func (p snapshotPool) Conn(ctx context.Context) (*sql.Conn, error) { return p.acquire(ctx) }
func (p snapshotPool) Close() error                                { return p.shutdown() }
func (p snapshotPool) Stats() sql.DBStats                          { return p.status() }
