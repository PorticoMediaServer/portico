package sqlite

import (
	"context"
	"errors"
	"math"
	"sync"

	"modernc.org/libc"
	sqlite3 "modernc.org/sqlite/lib"
)

// ErrRowBudget means the next native value/row would exceed an opt-in read
// window. Ordinary driver callers have no budget and retain existing behavior.
var ErrRowBudget = errors.New("SQLite read window exceeds materialization budget")

type rowBudgetKey struct{}

// RowBudget counts the entire read window, including metadata and peak live
// copies: SQLite value, driver value, database/sql Scan and the owner's copy.
// It never calls consumer code while the database/sql/native locks are held.
type RowBudget struct {
	mu    sync.Mutex
	bytes int64
	rows  uint32
}

func NewRowBudget(rows uint32, bytes int64) (*RowBudget, error) {
	if rows == 0 || bytes <= 0 {
		return nil, ErrRowBudget
	}
	return &RowBudget{bytes: bytes, rows: rows}, nil
}
func WithRowBudget(ctx context.Context, b *RowBudget) context.Context {
	return context.WithValue(ctx, rowBudgetKey{}, b)
}
func budgetFrom(ctx context.Context) *RowBudget {
	if ctx == nil {
		return nil
	}
	b, _ := ctx.Value(rowBudgetKey{}).(*RowBudget)
	return b
}
func (b *RowBudget) ReserveBytes(n int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n < 0 || n > b.bytes {
		return ErrRowBudget
	}
	b.bytes -= n
	return nil
}
func (b *RowBudget) lengthLimit() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return int(max(1, min(b.bytes/4, math.MaxInt32)))
}
func (b *RowBudget) metadata(c *conn, pstmt uintptr, count int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	need := int64(count) * 128
	for i := 0; i < count; i++ {
		for _, p := range []uintptr{sqlite3.Xsqlite3_column_name(c.tls, pstmt, int32(i)), sqlite3.Xsqlite3_column_decltype(c.tls, pstmt, int32(i))} {
			if p != 0 {
				n := int64(libc.Xstrlen(c.tls, p))
				if need > b.bytes || n > (b.bytes-need)/4 {
					return ErrRowBudget
				}
				need += 4 * n
			}
		}
	}
	if need < 0 || need > b.bytes {
		return ErrRowBudget
	}
	b.bytes -= need
	return nil
}
func (b *RowBudget) preflight(r *rows) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.rows == 0 {
		return ErrRowBudget
	}
	need := int64(64 + 64*len(r.columns))
	for i := range r.columns {
		kind := sqlite3.Xsqlite3_column_type(r.c.tls, r.pstmt, int32(i))
		if kind == sqlite3.SQLITE_TEXT || kind == sqlite3.SQLITE_BLOB {
			n := int64(sqlite3.Xsqlite3_column_bytes(r.c.tls, r.pstmt, int32(i)))
			if need > b.bytes || n < 0 || n > (b.bytes-need)/4 {
				return ErrRowBudget
			}
			need += 4 * n
		}
	}
	if need > b.bytes {
		return ErrRowBudget
	}
	b.bytes -= need
	b.rows--
	return nil
}
func budgetError(ctx context.Context, err error) error {
	if budgetFrom(ctx) != nil {
		var e *Error
		if errors.As(err, &e) && e.Code()&255 == sqlite3.SQLITE_TOOBIG {
			return errors.Join(ErrRowBudget, err)
		}
	}
	return err
}

// ReadBudgetSupported identifies this opt-in native pre-materialization path.
func (c *conn) ReadBudgetSupported() bool { return true }
