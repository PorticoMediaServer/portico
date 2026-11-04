package dbwork

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
)

const totalChangesSQL = `SELECT total_changes()`

type nativeCounterKey struct{}

// TotalChanges reads the trigger-inclusive counter through the SQL transaction
// API, so logical completion and outstanding-row locks retain their SQL fence.
// For tracked writes the observed driver can answer from SQLite's native scalar
// without dispatching a statement; other transactions keep the SQL path.
func TotalChanges(ctx context.Context, tx *sql.Tx) (int64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if tx == nil {
		return 0, errors.New("dbwork: nil transaction")
	}
	if _, tracked := liveWrites.Load(tx); tracked {
		ctx = context.WithValue(ctx, nativeCounterKey{}, true)
	}
	var total int64
	err := tx.QueryRowContext(ctx, totalChangesSQL).Scan(&total)
	return total, err
}

func connectionTotalChanges(ctx context.Context, conn *sql.Conn) (int64, error) {
	var total int64
	ctx = context.WithValue(ctx, nativeCounterKey{}, true)
	err := conn.QueryRowContext(ctx, totalChangesSQL).Scan(&total)
	return total, err
}

// database/sql holds the same connection lock here as for every driver query.
// Answering the opt-in bookkeeping request before SQL dispatch avoids a VM and
// read lease without bypassing the Tx's logical lifetime or its own row locks.
func nativeCounterResult(ctx context.Context, query string, inner driver.Conn) (driver.Rows, bool, error) {
	requested, _ := ctx.Value(nativeCounterKey{}).(bool)
	if !requested || query != totalChangesSQL {
		return nil, false, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, true, err
	}
	if counter, ok := inner.(interface{ TotalChanges() (int64, bool) }); ok {
		if total, known := counter.TotalChanges(); known {
			return &nativeCounterRows{total: total}, true, nil
		}
	}
	return nil, false, nil
}

type nativeCounterRows struct {
	total int64
	done  bool
}

func (*nativeCounterRows) Columns() []string { return []string{"total_changes()"} }
func (r *nativeCounterRows) Close() error    { r.done = true; return nil }
func (r *nativeCounterRows) Next(values []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done, values[0] = true, r.total
	return nil
}
