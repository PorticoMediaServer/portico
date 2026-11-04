package dbwork

import (
	"context"
	"database/sql"
	"errors"
)

// TotalChanges reads the connection's cumulative changed-row count, including
// trigger and foreign-key effects. Active gated writes use the native counter
// under their connection's SQL lock, without preparing a bookkeeping statement.
// Other transactions and drivers retain the SQL counter and its error behavior.
func TotalChanges(ctx context.Context, tx *sql.Tx) (int64, error) {
	return transactionTotalChanges(ctx, tx, false)
}

func transactionTotalChanges(ctx context.Context, tx *sql.Tx, ending bool) (int64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if tx == nil {
		return 0, errors.New("dbwork: nil transaction")
	}
	if value, ok := liveWrites.Load(tx); ok {
		w := value.(*Write)
		w.mu.Lock()
		conn, active := w.counterConn, !w.done || ending
		w.mu.Unlock()
		if active && conn != nil {
			if total, err := connectionTotalChanges(ctx, conn); err == nil {
				return total, nil
			}
			// SQL retains the transaction's own finished/canceled error when a
			// concurrent retirement made its retained connection unavailable.
		}
	}
	var total int64
	err := tx.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&total)
	return total, err
}

func connectionTotalChanges(ctx context.Context, conn *sql.Conn) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	var total int64
	var known bool
	if err := conn.Raw(func(inner any) error {
		// Raw takes database/sql's connection lock, just like a statement.
		// Reading an unlocked cache could miss an in-flight write and suppress
		// its commit notification. Never retain a native getter outside Raw.
		if err := ctx.Err(); err != nil {
			return err
		}
		if observed, ok := inner.(*observedConn); ok {
			inner = observed.inner
		}
		if counter, ok := inner.(interface{ TotalChanges() (int64, bool) }); ok {
			total, known = counter.TotalChanges()
		}
		return nil
	}); err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if known {
		return total, nil
	}
	// Unsupported drivers keep their existing SQL path. This must be outside
	// Raw, since SQL needs to acquire that same connection lock.
	err := conn.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&total)
	return total, err
}
