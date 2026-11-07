package governor

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// SnapshotForecast is trusted composition data, not an issued grant or proof.
// NativeRowBytes is a prospective maximum actual variable-row byte total, derived from closed native byte preflights or
// an existing legal whole-row content bound. It is NOT columns*max-value-size.
// MaxQueries is the finite complete proof recipe, bounded by its raw-row budget.
// Native VM/cache/workspace and separately retained output remain separately
// funded; neither callback completion nor byte comparison refunds those holds.
type SnapshotForecast struct {
	NativeRowBytes int64
	MaxQueries     uint16
}

// ReadForecasted keeps the existing ledger/native lifetime/consumer retirement
// path. A domain must use exact-key byte points before payload Query in THIS
// snapshot and compare every actual variable-column length after copy. The
// forecast alone proves neither row identity nor current disclosure authority.
// Existing Read callers retain their prior conservative reservation and8calls.
func (s *Snapshots) ReadForecasted(parent context.Context, r Request, budget time.Duration, f SnapshotForecast, read func(*ReadScope) error) (SnapshotResult, error) {
	if s == nil || parent == nil || f.NativeRowBytes <= 0 || f.NativeRowBytes > s.NativeRowReservation() || f.MaxQueries == 0 || uint32(f.MaxQueries) > r.Budget.RawRows || f.MaxQueries > 4096 {
		return SnapshotResult{}, ErrSnapshotBudget
	}
	return s.read(parent, r, budget, f, read)
}

// QueryExactBytes is the closed-domain two-point protocol: scalar octet lengths
// first, then payload, with identical bindings in the same immutable snapshot.
// variableColumns maps each length to its payload column. All unlisted payload
// columns must be fixed-size primitives. Callers supply COMPILED matching PK
// points/mapping, never a provider-selected SQL ID or expression. HN certifies
// the native builtin/point shape; this helper is no registration/capability API.
func (s *ReadScope) QueryExactBytes(lengthID, payloadID stmt.ID, variableColumns []int, args ...any) ([]any, error) {
	if s == nil || lengthID == payloadID || len(variableColumns) == 0 || len(variableColumns) > 128 {
		return nil, ErrSnapshotBudget
	}
	// Freeze the projection and mutable byte bindings before either query.
	var projection [128]int
	copy(projection[:], variableColumns)
	variableColumns = projection[:len(variableColumns)]
	var seen [128]bool
	for _, col := range variableColumns {
		if col < 0 || col >= len(seen) || seen[col] {
			return nil, ErrSnapshotBudget
		}
		seen[col] = true
	}
	frozen, err := s.freezePointArgs(args)
	if err != nil {
		return nil, err
	}
	sizes, err := s.Query(lengthID, frozen...)
	if err != nil {
		return nil, err
	}
	if len(sizes) == 0 {
		return nil, sql.ErrNoRows
	}
	if len(sizes) != 1 || len(sizes[0]) != len(variableColumns) {
		return nil, ErrSnapshotBudget
	}
	var total int64
	for i := range variableColumns {
		if sizes[0][i] == nil {
			continue
		}
		n, ok := sizes[0][i].(int64)
		if !ok || n < 0 || n > s.nativeRowBytes-total {
			return nil, ErrSnapshotBudget
		}
		total += n
	}
	values, err := s.Query(payloadID, frozen...)
	if err != nil {
		return nil, err
	}
	if len(values) != 1 || len(values[0]) > 128 {
		return nil, ErrSnapshotBudget
	}
	row := values[0]
	for i, v := range row {
		if seen[i] {
			continue
		}
		switch v.(type) {
		case nil, int64, float64, bool, time.Time:
		default:
			return nil, ErrSnapshotBudget
		}
	}
	for i, col := range variableColumns {
		if col >= len(row) {
			return nil, ErrSnapshotBudget
		}
		want := sizes[0][i]
		if want == nil {
			if row[col] != nil {
				return nil, ErrSnapshotBudget
			}
			continue
		}
		var actual int
		switch v := row[col].(type) {
		case string:
			actual = len(v)
		case []byte:
			actual = len(v)
		default:
			return nil, ErrSnapshotBudget
		}
		if int64(actual) != want.(int64) {
			return nil, errors.Join(ErrSnapshotBudget, ErrGrant)
		}
	}
	return row, nil
}

// The native point pair shares an owned argument vector. Charge bounded
// metadata and BLOB clones before allocating; strings are immutable views.
func (s *ReadScope) freezePointArgs(args []any) ([]any, error) {
	s.op.Lock()
	defer s.op.Unlock()
	if s.closed.Load() {
		return nil, ErrSnapshotClosed
	}
	if err := s.ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.authority.Check(s.grant, s.expected); err != nil {
		return nil, err
	}
	if len(args) > 128 {
		return nil, ErrSnapshotBudget
	}
	if err := s.charge(64 + 32*int64(len(args))); err != nil {
		return nil, err
	}
	for _, a := range args {
		if b, ok := a.([]byte); ok {
			if int64(len(b)) > s.expected.Need[MemoryBytes]/3 {
				return nil, ErrSnapshotBudget
			}
			if err := s.charge(3 * int64(len(b))); err != nil {
				return nil, err
			}
		}
	}
	frozen := append([]any(nil), args...)
	for i, a := range frozen {
		if b, ok := a.([]byte); ok {
			owned := make([]byte, len(b))
			copy(owned, b)
			frozen[i] = owned
		}
	}
	return frozen, nil
}
