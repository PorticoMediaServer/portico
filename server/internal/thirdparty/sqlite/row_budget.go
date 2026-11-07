package sqlite

import (
	"context"
	"errors"
	"sync"

	"modernc.org/libc"
	sqlite3 "modernc.org/sqlite/lib"
)

// ErrRowBudget means the next native value/row would exceed an opt-in read
// window. Ordinary driver callers have no budget and retain existing behavior.
var ErrRowBudget = errors.New("SQLite read window exceeds materialization budget")

type rowBudgetKey struct{}

// RowBudget counts Go allocations over a read window: driver, Scan and owner
// payload copies plus metadata. It does not cap native VM/workspace memory.
// An exact native-row forecast can additionally refuse excessive variable
// octets before Go copies; trusted same-snapshot preflight and reservation are
// separate. Without that opt-in the pool still reserves columns*value limit.
// It never calls consumer code while the database/sql/native locks are held.
type RowBudget struct {
	mu             sync.Mutex
	bytes          int64
	rows           uint32
	nativeRowBytes int64
	limitNativeRow bool
}

func NewRowBudget(rows uint32, bytes int64) (*RowBudget, error) {
	if rows == 0 || bytes <= 0 {
		return nil, ErrRowBudget
	}
	return &RowBudget{bytes: bytes, rows: rows}, nil
}

// NewRowBudgetWithNativeLimit keeps Go-copy accounting and adds a per-row
// variable-octet refusal bound. It changes no SQLite pool limits and does not
// authorize arbitrary SQL/native VM allocations. Zero permits scalar/NULL rows.
func NewRowBudgetWithNativeLimit(rows uint32, bytes, nativeRowBytes int64) (*RowBudget, error) {
	if nativeRowBytes < 0 {
		return nil, ErrRowBudget
	}
	b, err := NewRowBudget(rows, bytes)
	if err != nil {
		return nil, err
	}
	b.nativeRowBytes, b.limitNativeRow = nativeRowBytes, true
	return b, nil
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
func (b *RowBudget) metadata(c *conn, pstmt uintptr, count int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	need := int64(count) * 128
	for i := 0; i < count; i++ {
		for _, p := range []uintptr{sqlite3.Xsqlite3_column_name(c.tls, pstmt, int32(i)), sqlite3.Xsqlite3_column_decltype(c.tls, pstmt, int32(i))} {
			if p != 0 {
				n := int64(libc.Xstrlen(c.tls, p))
				if need > b.bytes || n > (b.bytes-need)/3 {
					return ErrRowBudget
				}
				need += 3 * n
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
	var nativeBytes int64
	for i := range r.columns {
		kind := sqlite3.Xsqlite3_column_type(r.c.tls, r.pstmt, int32(i))
		if kind == sqlite3.SQLITE_TEXT || kind == sqlite3.SQLITE_BLOB {
			n := int64(sqlite3.Xsqlite3_column_bytes(r.c.tls, r.pstmt, int32(i)))
			if b.limitNativeRow {
				if n < 0 || n > b.nativeRowBytes-nativeBytes {
					return ErrRowBudget
				}
				nativeBytes += n
			}
			if need > b.bytes || n < 0 || n > (b.bytes-need)/3 {
				return ErrRowBudget
			}
			need += 3 * n
		}
	}
	if need > b.bytes {
		return ErrRowBudget
	}
	b.bytes -= need
	b.rows--
	return nil
}

// ReadBudgetSupported identifies the opt-in Go pre-copy check.
func (c *conn) ReadBudgetSupported() bool { return true }
