package sqliteconn

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"

	"github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// ReadLimits is a fixed native bound for a dedicated reader pool. Both fields
// must be positive, or both zero to leave all pre-existing native limits alone.
// LengthBytes bounds a SQLite value/row; Columns bounds statement width. Budget
// Columns*LengthBytes native bytes before SQL, separately from Go row copies.
// These limits are not a total connection memory cap or a per-query grant.
type ReadLimits struct {
	LengthBytes int32
	Columns     int32
}

type nativeLimiter interface {
	SQLiteLimit(id int, newVal int) (int, error)
}

type limitConnector struct {
	driver.Connector
	limits ReadLimits
}

func (c *limitConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	limiter, ok := conn.(nativeLimiter)
	if !ok {
		return nil, errors.Join(fmt.Errorf("%w: physical connection lacks native limits", ErrPolicy), conn.Close())
	}
	for _, limit := range []struct{ id, value int }{
		{sqlite3.SQLITE_LIMIT_LENGTH, int(c.limits.LengthBytes)},
		{sqlite3.SQLITE_LIMIT_COLUMN, int(c.limits.Columns)},
	} {
		if _, err := limiter.SQLiteLimit(limit.id, limit.value); err != nil {
			return nil, errors.Join(err, conn.Close())
		}
		// SQLite silently clamps above its compiled hard limit. Refuse rather
		// than report the requested setting as the actual memory envelope.
		actual, err := limiter.SQLiteLimit(limit.id, -1)
		if err != nil || actual != limit.value {
			return nil, errors.Join(fmt.Errorf("%w: native limit %d: got %d want %d", ErrPolicy, limit.id, actual, limit.value), err, conn.Close())
		}
	}
	return conn, nil
}

func readLimits(conn *sql.Conn) (ReadLimits, error) {
	length, err := sqlite.Limit(conn, sqlite3.SQLITE_LIMIT_LENGTH, -1)
	if err != nil {
		return ReadLimits{}, err
	}
	columns, err := sqlite.Limit(conn, sqlite3.SQLITE_LIMIT_COLUMN, -1)
	return ReadLimits{LengthBytes: int32(length), Columns: int32(columns)}, err
}
